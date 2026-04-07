package docker

import (
	"context"
	"net"
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

	mu           sync.RWMutex
	records      map[string][]net.IP
	srvs         map[string][]srvRecord
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

	zone := plugin.Zones(d.zones).Matches(qname)
	if zone == "" {
		log.Debugf("Query %s not in zones [%s], passing to next plugin", qname, strings.Join(d.zones, ", "))
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
	isConnected := d.connected
	d.mu.RUnlock()
	log.Debugf("Lookup results for %s: A/AAAA records=%d, SRV records=%d, connected=%t", qname, len(ips), len(srvs), isConnected)

	if !ok && !srvOk {
		if d.Fall.Through(qname) {
			log.Debugf("No records found for %s, falling through to next plugin", qname)
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
		}
		return dns.RcodeSuccess, nil
	}

	if !found {
		if d.Fall.Through(qname) {
			log.Debugf("No handler for type %s on %s, falling through to next plugin", dns.TypeToString[qtype], qname)
			return plugin.NextOrFailure(d.Name(), d.Next, ctx, w, r)
		}

		// NODATA: name exists but no records for this query type
		log.Debugf("No handler for type %s on %s, returning NODATA", dns.TypeToString[qtype], qname)
		m.Ns = []dns.RR{d.soa(zone)}
		if err := w.WriteMsg(m); err != nil {
			log.Errorf("Failed to write message: %v", err)
			requestFailedCount.WithLabelValues(metrics.WithServer(ctx)).Inc()
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

	newRecords, newSrvs := generateRecords(ctx, GenerateRecordsInput{
		Containers:  containers,
		Zones:       d.zones,
		Inspector:   d.client,
		LabelPrefix: d.labelPrefix,
		Networks:    d.networks,
	})

	d.mu.Lock()
	d.records = newRecords
	d.srvs = newSrvs
	d.lastSyncTime = time.Now()
	d.mu.Unlock()
	lastSyncTimestamp.Set(float64(time.Now().Unix()))
	syncDuration.Observe(time.Since(syncStart).Seconds())
	recordsCount.Set(float64(len(newRecords)))
	srvRecordsCount.Set(float64(len(newSrvs)))
	containersCount.Set(float64(len(containers)))
	log.Debugf("Synced %d records and %d SRV records", len(newRecords), len(newSrvs))
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
}

// generateRecords generates the records for the containers.
func generateRecords(ctx context.Context, input GenerateRecordsInput) (map[string][]net.IP, map[string][]srvRecord) {
	newRecords := make(map[string][]net.IP)
	newSrvs := make(map[string][]srvRecord)

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

		// Add SRV records based on labels
		srvPrefix := input.LabelPrefix + "/srv."
		if input.LabelPrefix == "" {
			srvPrefix = "srv."
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

			names := []string{baseName}
			names = append(names, ne.settings.Aliases...)
			names = append(names, ne.settings.DNSNames...)

			if project != "" && service != "" {
				names = append(names, project+"."+service)
			}

			for _, name := range names {
				if name == "" {
					continue
				}
				for _, zone := range input.Zones {
					fqdn := strings.ToLower(name + "." + zone)
					if !strings.HasSuffix(fqdn, ".") {
						fqdn += "."
					}
					newRecords[fqdn] = append(newRecords[fqdn], ip)

					for srvKey, port := range containerSrvs {
						srvName := srvKey + "." + fqdn
						newSrvs[srvName] = append(newSrvs[srvName], srvRecord{
							target: fqdn,
							port:   port,
						})
					}
				}
			}
		}
	}

	return newRecords, newSrvs
}
