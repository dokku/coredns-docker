package docker

import (
	"context"
	"net"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/metrics"
	"github.com/coredns/coredns/plugin/pkg/fall"
	clog "github.com/coredns/coredns/plugin/pkg/log"
	"github.com/coredns/coredns/request"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/miekg/dns"
)

const pluginName = "docker"

var (
	log = clog.NewWithPlugin(pluginName)
)

// DefaultTTL is the default TTL for DNS records.
const DefaultTTL = uint32(30)

// Docker is a plugin that serves records for Docker containers
type Docker struct {
	Next plugin.Handler

	Fall fall.F

	ttl         uint32
	client      *client.Client
	zones       []string
	labelPrefix string
	maxBackoff  time.Duration
	networks    []string
	hostMode    bool
	hostModePTR bool

	mu           sync.RWMutex
	records      map[string][]net.IP
	srvs         map[string][]srvRecord
	ptrs         map[string][]string // reverse-arpa FQDN -> container FQDNs
	connected    bool
	lastSyncTime time.Time
}

type srvRecord struct {
	target string
	port   uint16
}

// ServeDNS implements the plugin.Handler interface.
func (d *Docker) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	start := time.Now()
	state := request.Request{W: w, Req: r}
	defer func() {
		requestDuration.WithLabelValues(metrics.WithServer(ctx), dns.TypeToString[state.QType()]).Observe(time.Since(start).Seconds())
	}()
	qname := strings.ToLower(state.Name())
	qtype := state.QType()
	log.Debugf("Query: qname=%s qtype=%s", qname, dns.TypeToString[qtype])

	// Handle PTR queries (reverse DNS) before zone check.
	// PTR queries use in-addr.arpa/ip6.arpa zones which are outside our configured zones.
	if qtype == dns.TypePTR {
		d.mu.RLock()
		ptrs, ptrOk := d.ptrs[qname]
		isConnected := d.connected
		d.mu.RUnlock()

		if !ptrOk {
			log.Debugf("No PTR records for %s, passing to next plugin", qname)
			requestFallthroughCount.WithLabelValues(metrics.WithServer(ctx)).Inc()
			return plugin.NextOrFailure(d.Name(), d.Next, ctx, w, r)
		}

		log.Debugf("PTR lookup for %s: %d record(s), connected=%t", qname, len(ptrs), isConnected)

		m := new(dns.Msg)
		m.SetReply(r)
		m.Authoritative = true
		m.Compress = true

		ttl := d.ttl
		if !isConnected && ttl > 5 {
			ttl = 5
			requestStaleCount.WithLabelValues(metrics.WithServer(ctx)).Inc()
		}

		for _, fqdn := range ptrs {
			m.Answer = append(m.Answer, &dns.PTR{
				Hdr: dns.RR_Header{
					Name:   state.QName(),
					Rrtype: dns.TypePTR,
					Class:  dns.ClassINET,
					Ttl:    ttl,
				},
				Ptr: fqdn,
			})
		}

		log.Debugf("Response for %s PTR: %d answer(s)", qname, len(m.Answer))

		if err := w.WriteMsg(m); err != nil {
			log.Errorf("Failed to write message: %v", err)
			requestFailedCount.WithLabelValues(metrics.WithServer(ctx)).Inc()
		} else {
			requestSuccessCount.WithLabelValues(metrics.WithServer(ctx)).Inc()
		}
		return dns.RcodeSuccess, nil
	}

	zone := plugin.Zones(d.zones).Matches(qname)
	if zone == "" {
		log.Debugf("Query %s not in zones [%s], passing to next plugin", qname, strings.Join(d.zones, ", "))
		requestFallthroughCount.WithLabelValues(metrics.WithServer(ctx)).Inc()
		return plugin.NextOrFailure(d.Name(), d.Next, ctx, w, r)
	}

	// Handle SOA query at zone apex
	if qtype == dns.TypeSOA && qname == zone {
		log.Debugf("SOA query at zone apex for %s", zone)
		m := new(dns.Msg)
		m.SetReply(r)
		m.Authoritative = true
		m.Answer = []dns.RR{d.soa(zone)}
		if err := w.WriteMsg(m); err != nil {
			log.Errorf("Failed to write message: %v", err)
			requestFailedCount.WithLabelValues(metrics.WithServer(ctx)).Inc()
		} else {
			requestSuccessCount.WithLabelValues(metrics.WithServer(ctx)).Inc()
		}
		return dns.RcodeSuccess, nil
	}

	// Handle NS query at zone apex
	if qtype == dns.TypeNS && qname == zone {
		log.Debugf("NS query at zone apex for %s", zone)
		m := new(dns.Msg)
		m.SetReply(r)
		m.Authoritative = true
		m.Answer = []dns.RR{d.ns(zone)}
		if err := w.WriteMsg(m); err != nil {
			log.Errorf("Failed to write message: %v", err)
			requestFailedCount.WithLabelValues(metrics.WithServer(ctx)).Inc()
		} else {
			requestSuccessCount.WithLabelValues(metrics.WithServer(ctx)).Inc()
		}
		return dns.RcodeSuccess, nil
	}

	d.mu.RLock()
	ips, ok := d.records[qname]
	srvs, srvOk := d.srvs[qname]
	if !ok && !srvOk {
		// Try wildcard: replace the first non-underscore label with *
		// This handles both plain names (foo.web.docker. → *.web.docker.)
		// and SRV-prefixed names (_http._tcp.foo.web.docker. → _http._tcp.*.web.docker.)
		labels := dns.SplitDomainName(qname)
		wildcardIdx := 0
		for wildcardIdx < len(labels) && strings.HasPrefix(labels[wildcardIdx], "_") {
			wildcardIdx++
		}
		if wildcardIdx < len(labels)-1 {
			wildcardLabels := make([]string, len(labels))
			copy(wildcardLabels, labels)
			wildcardLabels[wildcardIdx] = "*"
			wildcardName := strings.Join(wildcardLabels, ".") + "."
			ips, ok = d.records[wildcardName]
			srvs, srvOk = d.srvs[wildcardName]
			if ok || srvOk {
				log.Debugf("Wildcard match for %s via %s", qname, wildcardName)
			}
		}
	}
	isConnected := d.connected
	d.mu.RUnlock()
	log.Debugf("Lookup results for %s: A/AAAA records=%d, SRV records=%d, connected=%t", qname, len(ips), len(srvs), isConnected)

	if !ok && !srvOk {
		if d.Fall.Through(qname) {
			log.Debugf("No records found for %s, falling through to next plugin", qname)
			requestFallthroughCount.WithLabelValues(metrics.WithServer(ctx)).Inc()
			return plugin.NextOrFailure(d.Name(), d.Next, ctx, w, r)
		}

		log.Debugf("No records found for %s, returning NXDOMAIN", qname)
		m := new(dns.Msg)
		m.SetReply(r)
		m.Authoritative = true
		m.Rcode = dns.RcodeNameError
		m.Ns = []dns.RR{d.soa(zone)}
		if err := w.WriteMsg(m); err != nil {
			log.Errorf("Failed to write message: %v", err)
			requestFailedCount.WithLabelValues(metrics.WithServer(ctx)).Inc()
		} else {
			requestSuccessCount.WithLabelValues(metrics.WithServer(ctx)).Inc()
		}
		return dns.RcodeSuccess, nil
	}

	m := new(dns.Msg)
	m.SetReply(r)
	m.Authoritative = true
	m.Compress = true
	m.Rcode = dns.RcodeSuccess

	ttl := d.ttl
	if !isConnected && ttl > 5 {
		ttl = 5
		requestStaleCount.WithLabelValues(metrics.WithServer(ctx)).Inc()
	}

	header := dns.RR_Header{
		Name:   state.QName(),
		Rrtype: qtype,
		Class:  dns.ClassINET,
		Ttl:    ttl,
	}

	found := false
	if qtype == dns.TypeSRV {
		for _, srv := range srvs {
			m.Answer = append(m.Answer, &dns.SRV{
				Hdr:      header,
				Priority: 10,
				Weight:   10,
				Port:     srv.port,
				Target:   srv.target,
			})
			found = true
		}
	} else {
		for _, ip := range ips {
			if qtype == dns.TypeA && ip.To4() != nil {
				m.Answer = append(m.Answer, &dns.A{
					Hdr: header,
					A:   ip,
				})
				found = true
			} else if qtype == dns.TypeAAAA && ip.To4() == nil {
				m.Answer = append(m.Answer, &dns.AAAA{
					Hdr:  header,
					AAAA: ip,
				})
				found = true
			}
		}
	}
	log.Debugf("Response for %s %s: %d answer(s)", qname, dns.TypeToString[qtype], len(m.Answer))

	if !found && (qtype == dns.TypeA || qtype == dns.TypeAAAA || qtype == dns.TypeSRV) {
		// NODATA
		log.Debugf("NODATA response for %s type %s: name exists but no matching records", qname, dns.TypeToString[qtype])
		m.Ns = []dns.RR{d.soa(zone)}
		if err := w.WriteMsg(m); err != nil {
			log.Errorf("Failed to write message: %v", err)
			requestFailedCount.WithLabelValues(metrics.WithServer(ctx)).Inc()
		} else {
			requestSuccessCount.WithLabelValues(metrics.WithServer(ctx)).Inc()
		}
		return dns.RcodeSuccess, nil
	}

	if !found {
		if d.Fall.Through(qname) {
			log.Debugf("No handler for type %s on %s, falling through to next plugin", dns.TypeToString[qtype], qname)
			requestFallthroughCount.WithLabelValues(metrics.WithServer(ctx)).Inc()
			return plugin.NextOrFailure(d.Name(), d.Next, ctx, w, r)
		}

		// NODATA: name exists but no records for this query type
		log.Debugf("No handler for type %s on %s, returning NODATA", dns.TypeToString[qtype], qname)
		m.Ns = []dns.RR{d.soa(zone)}
		if err := w.WriteMsg(m); err != nil {
			log.Errorf("Failed to write message: %v", err)
			requestFailedCount.WithLabelValues(metrics.WithServer(ctx)).Inc()
		} else {
			requestSuccessCount.WithLabelValues(metrics.WithServer(ctx)).Inc()
		}
		return dns.RcodeSuccess, nil
	}

	if err := w.WriteMsg(m); err != nil {
		log.Errorf("Failed to write message: %v", err)
		requestFailedCount.WithLabelValues(metrics.WithServer(ctx)).Inc()
	} else {
		requestSuccessCount.WithLabelValues(metrics.WithServer(ctx)).Inc()
	}
	return dns.RcodeSuccess, nil
}

// Name implements the Handler interface.
func (d *Docker) Name() string { return pluginName }

// ns returns a synthetic NS record for the given zone.
func (d *Docker) ns(zone string) *dns.NS {
	return &dns.NS{
		Hdr: dns.RR_Header{Name: zone, Rrtype: dns.TypeNS, Class: dns.ClassINET, Ttl: d.ttl},
		Ns:  "ns.dns." + zone,
	}
}

// soa returns a synthetic SOA record for the given zone.
func (d *Docker) soa(zone string) *dns.SOA {
	return &dns.SOA{
		Hdr:     dns.RR_Header{Name: zone, Rrtype: dns.TypeSOA, Class: dns.ClassINET, Ttl: d.ttl},
		Ns:      "ns.dns." + zone,
		Mbox:    "hostmaster." + zone,
		Serial:  uint32(time.Now().Unix()),
		Refresh: 7200,
		Retry:   1800,
		Expire:  86400,
		Minttl:  d.ttl,
	}
}

func (d *Docker) startEventLoop(ctx context.Context) {
	log.Infof("Starting Docker event loop")

	filter := filters.NewArgs()
	filter.Add("type", "container")
	filter.Add("event", "start")
	filter.Add("event", "die")
	filter.Add("event", "destroy")
	filter.Add("event", "stop")
	filter.Add("event", "create")
	filter.Add("event", "restart")

	backoff := 1 * time.Second

	for {
		// Sync records whenever we (re)connect
		d.syncRecords(ctx)

		msgs, errs := d.client.Events(ctx, events.ListOptions{
			Filters: filter,
		})

		d.mu.Lock()
		d.connected = true
		d.mu.Unlock()
		connectedGauge.Set(1)
		log.Debugf("Connected to Docker daemon")

		stopped := false
		for !stopped {
			select {
			case <-ctx.Done():
				return
			case err := <-errs:
				if err != nil && err != context.Canceled {
					log.Errorf("Docker event error: %v", err)
				}
				d.mu.Lock()
				d.connected = false
				d.mu.Unlock()
				connectedGauge.Set(0)
				stopped = true
			case msg := <-msgs:
				log.Debugf("Docker event: %s %s %s", msg.Type, msg.Action, msg.Actor.ID)
				d.syncRecords(ctx)
				backoff = 1 * time.Second // Reset backoff on successful event
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
			log.Infof("Attempting to reconnect to Docker daemon after %v...", backoff)
			backoff *= 2
			if backoff > d.maxBackoff {
				backoff = d.maxBackoff
			}
		}
	}
}

func (d *Docker) syncRecords(ctx context.Context) {
	syncStart := time.Now()
	containers, err := d.client.ContainerList(ctx, container.ListOptions{})
	if err != nil {
		log.Errorf("Failed to list containers: %v", err)
		syncErrorCount.Inc()
		return
	}
	log.Debugf("Found %d running containers", len(containers))

	newRecords, newSrvs, newPtrs := generateRecords(ctx, GenerateRecordsInput{
		Containers:  containers,
		Zones:       d.zones,
		Inspector:   d.client,
		LabelPrefix: d.labelPrefix,
		Networks:    d.networks,
		HostMode:    d.hostMode,
		HostModePTR: d.hostModePTR,
	})

	d.mu.Lock()
	d.records = newRecords
	d.srvs = newSrvs
	d.ptrs = newPtrs
	d.lastSyncTime = time.Now()
	d.mu.Unlock()
	lastSyncTimestamp.Set(float64(time.Now().Unix()))
	syncDuration.Observe(time.Since(syncStart).Seconds())
	recordsCount.Set(float64(len(newRecords)))
	srvRecordsCount.Set(float64(len(newSrvs)))
	ptrRecordsCount.Set(float64(len(newPtrs)))
	containersCount.Set(float64(len(containers)))
	log.Debugf("Synced %d records, %d SRV records, and %d PTR records", len(newRecords), len(newSrvs), len(newPtrs))
}

// ContainerInspector is an interface for inspecting containers.
type ContainerInspector interface {
	ContainerInspect(ctx context.Context, containerID string) (container.InspectResponse, error)
}

// GenerateRecordsInput is the input for the generateRecords function.
type GenerateRecordsInput struct {
	// Client is the Docker client to use.
	Inspector ContainerInspector
	// Containers is the list of containers to generate records for.
	Containers []container.Summary
	// Zones is the list of domains to generate records for.
	Zones []string
	// LabelPrefix is the label prefix to generate records for.
	LabelPrefix string
	// Networks is the list of networks to generate records for.
	Networks []string
	// HostMode enables host-bound IP/port resolution instead of container IPs.
	// When true, A/AAAA and SRV records are derived from each container's
	// published host port bindings (NetworkSettings.Ports).
	HostMode bool
	// HostModePTR enables PTR record emission while HostMode is active.
	// Off by default because multiple containers may share a host IP
	// (especially the loopback fallback), making reverse lookups noisy.
	HostModePTR bool
}

// generateRecords generates the records for the containers.
func generateRecords(ctx context.Context, input GenerateRecordsInput) (map[string][]net.IP, map[string][]srvRecord, map[string][]string) {
	newRecords := make(map[string][]net.IP)
	newSrvs := make(map[string][]srvRecord)
	newPtrs := make(map[string][]string)

	for _, c := range input.Containers {
		inspect, err := input.Inspector.ContainerInspect(ctx, c.ID)
		if err != nil {
			log.Errorf("Failed to inspect container %s: %v", c.ID, err)
			continue
		}

		// Guard against nil pointer fields in the inspect response
		if inspect.ContainerJSONBase == nil || inspect.ContainerJSONBase.HostConfig == nil {
			log.Debugf("Container %s has incomplete inspect response, skipping", c.ID)
			continue
		}
		if inspect.NetworkSettings == nil || inspect.NetworkSettings.Networks == nil {
			log.Debugf("Container %s has no network settings, skipping", c.ID)
			continue
		}
		if inspect.Config == nil {
			inspect.Config = &container.Config{Labels: map[string]string{}}
		}

		// Determine the primary network name from NetworkMode
		primaryNetworkName := string(inspect.HostConfig.NetworkMode)
		if primaryNetworkName == "" || primaryNetworkName == "default" {
			primaryNetworkName = "bridge"
		}

		// Determine which networks to process
		type networkEntry struct {
			name     string
			settings *network.EndpointSettings
		}
		var networksToProcess []networkEntry

		if len(input.Networks) > 0 {
			// Filter mode: check ALL attached networks against the allowed list
			allowedSet := make(map[string]bool, len(input.Networks))
			for _, n := range input.Networks {
				allowedSet[n] = true
			}
			// Sort network names for deterministic output
			netNames := make([]string, 0, len(inspect.NetworkSettings.Networks))
			for netName := range inspect.NetworkSettings.Networks {
				netNames = append(netNames, netName)
			}
			sort.Strings(netNames)
			for _, netName := range netNames {
				netSettings := inspect.NetworkSettings.Networks[netName]
				if allowedSet[netName] && netSettings != nil {
					networksToProcess = append(networksToProcess, networkEntry{name: netName, settings: netSettings})
				}
			}
			if len(networksToProcess) == 0 {
				log.Debugf("Container %s not on any allowed network", c.ID)
				continue
			}
		} else {
			// No filter: use the primary network only
			netSettings, ok := inspect.NetworkSettings.Networks[primaryNetworkName]
			if !ok || netSettings == nil {
				log.Debugf("Container %s not on network %s", c.ID, primaryNetworkName)
				continue
			}
			networksToProcess = []networkEntry{{name: primaryNetworkName, settings: netSettings}}
		}

		// Compute per-container data once
		baseName := strings.TrimPrefix(inspect.Name, "/")

		project := inspect.Config.Labels["com.docker.compose.project"]
		service := inspect.Config.Labels["com.docker.compose.service"]

		// Parse hostname label for additional DNS names
		hostnameLabel := input.LabelPrefix + "/hostname"
		if input.LabelPrefix == "" {
			hostnameLabel = "hostname"
		}
		var hostnameNames []string
		if hostnameValue, ok := inspect.Config.Labels[hostnameLabel]; ok && hostnameValue != "" {
			for _, h := range strings.Split(hostnameValue, ",") {
				h = strings.TrimSpace(h)
				if h != "" {
					hostnameNames = append(hostnameNames, h)
				}
			}
		}

		// Parse wildcard label
		wildcardLabel := input.LabelPrefix + "/wildcard"
		if input.LabelPrefix == "" {
			wildcardLabel = "wildcard"
		}
		enableWildcard := inspect.Config.Labels[wildcardLabel] == "true"

		// Add SRV records based on labels
		srvPrefix := input.LabelPrefix + "/srv."
		if input.LabelPrefix == "" {
			srvPrefix = "srv."
		}

		if input.HostMode {
			// In host mode, IPs and ports come from the container's host port
			// bindings (NetworkSettings.Ports) rather than its internal network
			// IP. This is for setups where CoreDNS runs outside Docker and the
			// container networks aren't directly reachable.
			type hostBinding struct {
				ip   net.IP
				port uint16
			}

			// Collect bindings keyed by container port spec (e.g. "80/tcp").
			bindingsByPort := make(map[string][]hostBinding)
			var uniqueHostIPs []net.IP
			seenHostIP := make(map[string]bool)
			for portSpec, bindings := range inspect.NetworkSettings.Ports {
				for _, b := range bindings {
					hostIPStr := b.HostIP
					if hostIPStr == "" || hostIPStr == "0.0.0.0" {
						hostIPStr = "127.0.0.1"
					} else if hostIPStr == "::" {
						hostIPStr = "::1"
					}
					ip := net.ParseIP(hostIPStr)
					if ip == nil {
						log.Debugf("Container %s has unparseable host IP %q for port %s", c.ID, b.HostIP, portSpec)
						continue
					}
					hp, err := strconv.Atoi(b.HostPort)
					if err != nil || hp < 1 || hp > 65535 {
						log.Debugf("Container %s has invalid host port %q for port %s", c.ID, b.HostPort, portSpec)
						continue
					}
					portStr := string(portSpec)
					bindingsByPort[portStr] = append(bindingsByPort[portStr], hostBinding{
						ip:   ip,
						port: uint16(hp),
					})
					key := ip.String()
					if !seenHostIP[key] {
						seenHostIP[key] = true
						uniqueHostIPs = append(uniqueHostIPs, ip)
					}
				}
			}

			if len(uniqueHostIPs) == 0 {
				log.Debugf("Container %s has no host port bindings, skipping in host mode", c.ID)
				continue
			}

			// Deterministic IP ordering so record output is stable.
			sort.Slice(uniqueHostIPs, func(i, j int) bool {
				return uniqueHostIPs[i].String() < uniqueHostIPs[j].String()
			})

			// In host mode there is only one logical destination, so union
			// names across all selected networks.
			names := []string{baseName}
			for _, ne := range networksToProcess {
				names = append(names, ne.settings.Aliases...)
				names = append(names, ne.settings.DNSNames...)
			}
			if project != "" && service != "" {
				names = append(names, project+"."+service)
			}
			names = append(names, hostnameNames...)

			// Deduplicate names (same rules as the default path).
			seen := make(map[string]bool, len(names))
			uniqueNames := names[:0]
			for _, name := range names {
				lower := strings.ToLower(name)
				if lower == "" || seen[lower] {
					continue
				}
				seen[lower] = true
				uniqueNames = append(uniqueNames, name)
			}
			names = uniqueNames

			// Build host-mode SRV entries from labels, translating each label's
			// container port into its bound host port(s). Multiple bindings for
			// the same container port produce multiple SRV records.
			hostSrvs := make(map[string][]uint16)
			hasSrvLabel := false
			for k, v := range inspect.Config.Labels {
				if !strings.HasPrefix(k, srvPrefix) {
					continue
				}
				parts := strings.Split(strings.TrimPrefix(k, srvPrefix), ".")
				if len(parts) != 2 {
					log.Debugf("Container %s has invalid SRV label %s", c.ID, k)
					continue
				}
				containerPort, err := strconv.Atoi(v)
				if err != nil {
					log.Debugf("Container %s has invalid SRV port %s", c.ID, v)
					continue
				}
				if containerPort < 1 || containerPort > 65535 {
					log.Debugf("Container %s has SRV port %d not in valid range 1-65535", c.ID, containerPort)
					continue
				}
				hasSrvLabel = true
				// parts[0] = "_tcp"/"_udp", parts[1] = "_http" etc.
				proto := strings.TrimPrefix(strings.ToLower(parts[0]), "_")
				lookup := strconv.Itoa(containerPort) + "/" + proto
				bindingsForPort, ok := bindingsByPort[lookup]
				if !ok || len(bindingsForPort) == 0 {
					log.Debugf("Container %s has SRV label %s=%d but no host binding for %s in host mode", c.ID, k, containerPort, lookup)
					continue
				}
				srvKey := strings.ToLower(parts[1] + "." + parts[0])
				for _, b := range bindingsForPort {
					hostSrvs[srvKey] = append(hostSrvs[srvKey], b.port)
				}
			}

			// Fallback: if no SRV labels were present at all, derive SRV
			// records from the bound port specs themselves, mirroring the
			// default code path but using host ports.
			if !hasSrvLabel {
				for portStr, bindings := range bindingsByPort {
					parts := strings.Split(portStr, "/")
					cp, err := strconv.Atoi(parts[0])
					if err != nil {
						log.Debugf("Container %s has invalid port %s", c.ID, portStr)
						continue
					}
					if cp < 1 || cp > 65535 {
						log.Debugf("Container %s has port %d not in valid range 1-65535", c.ID, cp)
						continue
					}
					var srvKeys []string
					if len(parts) > 1 {
						proto := strings.ToLower(parts[1])
						srvKeys = []string{"_" + proto + "._" + proto}
					} else {
						srvKeys = []string{"_tcp._tcp", "_udp._udp"}
					}
					for _, srvKey := range srvKeys {
						for _, b := range bindings {
							hostSrvs[srvKey] = append(hostSrvs[srvKey], b.port)
						}
					}
				}
			}

			// Emit records for each (name, zone) pair.
			for _, name := range names {
				for _, zone := range input.Zones {
					fqdn := strings.ToLower(name + "." + zone)
					if !strings.HasSuffix(fqdn, ".") {
						fqdn += "."
					}

					for _, ip := range uniqueHostIPs {
						if !slices.ContainsFunc(newRecords[fqdn], ip.Equal) {
							newRecords[fqdn] = append(newRecords[fqdn], ip)
						}
						if enableWildcard {
							wildcardFqdn := "*." + fqdn
							if !slices.ContainsFunc(newRecords[wildcardFqdn], ip.Equal) {
								newRecords[wildcardFqdn] = append(newRecords[wildcardFqdn], ip)
							}
						}

						if input.HostModePTR {
							arpa, arpaErr := dns.ReverseAddr(ip.String())
							if arpaErr == nil && !slices.Contains(newPtrs[arpa], fqdn) {
								newPtrs[arpa] = append(newPtrs[arpa], fqdn)
							}
						}
					}

					for srvKey, ports := range hostSrvs {
						srvName := srvKey + "." + fqdn
						for _, port := range ports {
							isDupSrv := slices.ContainsFunc(newSrvs[srvName], func(existing srvRecord) bool {
								return existing.target == fqdn && existing.port == port
							})
							if !isDupSrv {
								newSrvs[srvName] = append(newSrvs[srvName], srvRecord{
									target: fqdn,
									port:   port,
								})
							}

							if enableWildcard {
								wildcardSrvName := srvKey + ".*." + fqdn
								isDupWildcardSrv := slices.ContainsFunc(newSrvs[wildcardSrvName], func(existing srvRecord) bool {
									return existing.target == fqdn && existing.port == port
								})
								if !isDupWildcardSrv {
									newSrvs[wildcardSrvName] = append(newSrvs[wildcardSrvName], srvRecord{
										target: fqdn,
										port:   port,
									})
								}
							}
						}
					}
				}
			}

			continue
		}

		containerSrvs := make(map[string]uint16)
		for k, v := range inspect.Config.Labels {
			if !strings.HasPrefix(k, srvPrefix) {
				continue
			}
			parts := strings.Split(strings.TrimPrefix(k, srvPrefix), ".")
			if len(parts) != 2 {
				log.Debugf("Container %s has invalid SRV label %s", c.ID, k)
				continue
			}
			port, err := strconv.Atoi(v)
			if err != nil {
				log.Debugf("Container %s has invalid SRV port %s", c.ID, v)
				continue
			}
			if port < 1 || port > 65535 {
				log.Debugf("Container %s has SRV port %d not in valid range 1-65535", c.ID, port)
				continue
			}

			// key: _service._proto
			containerSrvs[strings.ToLower(parts[1]+"."+parts[0])] = uint16(port)
		}

		// Fallback to NetworkSettings.Ports if no labels found
		if len(containerSrvs) == 0 {
			for p := range inspect.NetworkSettings.Ports {
				portStr := string(p)
				parts := strings.Split(portStr, "/")
				port, err := strconv.Atoi(parts[0])
				if err != nil {
					log.Debugf("Container %s has invalid port %s", c.ID, portStr)
					continue
				}
				if port < 1 || port > 65535 {
					log.Debugf("Container %s has port %d not in valid range 1-65535", c.ID, port)
					continue
				}

				if len(parts) > 1 {
					// key: _proto._proto
					proto := strings.ToLower(parts[1])
					containerSrvs["_"+proto+"._"+proto] = uint16(port)
				} else {
					containerSrvs["_tcp._tcp"] = uint16(port)
					containerSrvs["_udp._udp"] = uint16(port)
				}
			}
		}

		// Generate records for each matching network
		for _, ne := range networksToProcess {
			ip := net.ParseIP(ne.settings.IPAddress)
			if ip == nil {
				log.Debugf("Container %s has invalid IP address %s on network %s", c.ID, ne.settings.IPAddress, ne.name)
				continue
			}

			arpa, arpaErr := dns.ReverseAddr(ip.String())
			if arpaErr != nil {
				log.Debugf("Container %s has IP %s that cannot be reversed: %v", c.ID, ip.String(), arpaErr)
			}

			names := []string{baseName}
			names = append(names, ne.settings.Aliases...)
			names = append(names, ne.settings.DNSNames...)

			if project != "" && service != "" {
				names = append(names, project+"."+service)
			}

			names = append(names, hostnameNames...)

			// Deduplicate names (case-insensitive, preserving insertion order) to
			// avoid producing duplicate records when a container's name overlaps
			// with its aliases or DNSNames (common on Docker 25+ user-defined
			// networks where Aliases and DNSNames contain the container name).
			seen := make(map[string]bool, len(names))
			uniqueNames := names[:0]
			for _, name := range names {
				lower := strings.ToLower(name)
				if lower == "" || seen[lower] {
					continue
				}
				seen[lower] = true
				uniqueNames = append(uniqueNames, name)
			}
			names = uniqueNames

			for _, name := range names {
				for _, zone := range input.Zones {
					fqdn := strings.ToLower(name + "." + zone)
					if !strings.HasSuffix(fqdn, ".") {
						fqdn += "."
					}
					// Dedup at the record level to also cover the edge case where
					// two networks assign the same IP to the same container.
					if !slices.ContainsFunc(newRecords[fqdn], ip.Equal) {
						newRecords[fqdn] = append(newRecords[fqdn], ip)
					}
					if arpaErr == nil && !slices.Contains(newPtrs[arpa], fqdn) {
						newPtrs[arpa] = append(newPtrs[arpa], fqdn)
					}

					if enableWildcard {
						wildcardFqdn := "*." + fqdn
						if !slices.ContainsFunc(newRecords[wildcardFqdn], ip.Equal) {
							newRecords[wildcardFqdn] = append(newRecords[wildcardFqdn], ip)
						}
					}

					for srvKey, port := range containerSrvs {
						srvName := srvKey + "." + fqdn
						isDupSrv := slices.ContainsFunc(newSrvs[srvName], func(existing srvRecord) bool {
							return existing.target == fqdn && existing.port == port
						})
						if !isDupSrv {
							newSrvs[srvName] = append(newSrvs[srvName], srvRecord{
								target: fqdn,
								port:   port,
							})
						}

						if enableWildcard {
							wildcardSrvName := srvKey + ".*." + fqdn
							isDupWildcardSrv := slices.ContainsFunc(newSrvs[wildcardSrvName], func(existing srvRecord) bool {
								return existing.target == fqdn && existing.port == port
							})
							if !isDupWildcardSrv {
								newSrvs[wildcardSrvName] = append(newSrvs[wildcardSrvName], srvRecord{
									target: fqdn,
									port:   port,
								})
							}
						}
					}
				}
			}
		}
	}

	return newRecords, newSrvs, newPtrs
}
