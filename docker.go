package docker

import (
	"context"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/plugin/metrics"
	clog "github.com/coredns/coredns/plugin/pkg/log"
	"github.com/coredns/coredns/request"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
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

	ttl         uint32
	client      *client.Client
	domain      string
	labelPrefix string
	maxBackoff  time.Duration
	networks    []string

	mu        sync.RWMutex
	records   map[string][]net.IP
	srvs      map[string][]srvRecord
	connected bool
}

type srvRecord struct {
	target string
	port   uint16
}

// ServeDNS implements the plugin.Handler interface.
func (d *Docker) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	state := request.Request{W: w, Req: r}
	qname := strings.ToLower(state.Name())
	qtype := state.QType()

	if plugin.Zones([]string{d.domain}).Matches(qname) == "" {
		return plugin.NextOrFailure(d.Name(), d.Next, ctx, w, r)
	}

	d.mu.RLock()
	ips, ok := d.records[qname]
	srvs, srvOk := d.srvs[qname]
	d.mu.RUnlock()

	if !ok && !srvOk {
		return plugin.NextOrFailure(d.Name(), d.Next, ctx, w, r)
	}

	m := new(dns.Msg)
	m.SetReply(r)
	m.Authoritative = true
	m.Compress = true
	m.Rcode = dns.RcodeSuccess

	header := dns.RR_Header{
		Name:   state.QName(),
		Rrtype: qtype,
		Class:  dns.ClassINET,
		Ttl:    d.ttl,
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

	if !found && (qtype == dns.TypeA || qtype == dns.TypeAAAA || qtype == dns.TypeSRV) {
		// NODATA
		if err := w.WriteMsg(m); err != nil {
			log.Errorf("Failed to write message: %v", err)
		}
		return dns.RcodeSuccess, nil
	}

	if !found {
		// For other types, we don't have records
		return plugin.NextOrFailure(d.Name(), d.Next, ctx, w, r)
	}

	if err := w.WriteMsg(m); err != nil {
		log.Errorf("Failed to write message: %v", err)
	}
	requestSuccessCount.WithLabelValues(metrics.WithServer(ctx)).Inc()
	return dns.RcodeSuccess, nil
}

// Name implements the Handler interface.
func (d *Docker) Name() string { return pluginName }

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
	containers, err := d.client.ContainerList(ctx, container.ListOptions{})
	if err != nil {
		log.Errorf("Failed to list containers: %v", err)
		return
	}

	newRecords, newSrvs := generateRecords(ctx, GenerateRecordsInput{
		Containers:  containers,
		Domain:      d.domain,
		Inspector:   d.client,
		LabelPrefix: d.labelPrefix,
		Networks:    d.networks,
	})

	d.mu.Lock()
	d.records = newRecords
	d.srvs = newSrvs
	d.mu.Unlock()
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
	// Domain is the domain to generate records for.
	Domain string
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

		networkName := string(inspect.HostConfig.NetworkMode)
		if networkName == "" || networkName == "default" {
			networkName = "bridge"
		}

		if len(input.Networks) > 0 {
			found := false
			for _, n := range input.Networks {
				if n == networkName {
					found = true
					break
				}
			}
			if !found {
				log.Debugf("Container %s not on any allowed network (on %s)", c.ID, networkName)
				continue
			}
		}

		network, ok := inspect.NetworkSettings.Networks[networkName]
		if !ok {
			log.Debugf("Container %s not on network %s", c.ID, networkName)
			continue
		}

		ip := net.ParseIP(network.IPAddress)
		if ip == nil {
			continue
		}

		// Collect all names
		names := []string{
			strings.TrimPrefix(inspect.Name, "/"),
		}
		names = append(names, network.Aliases...)
		names = append(names, network.DNSNames...)

		// Add Compose project/service alias if present
		project := inspect.Config.Labels["com.docker.compose.project"]
		service := inspect.Config.Labels["com.docker.compose.service"]
		if project != "" && service != "" {
			names = append(names, project+"."+service)
		}

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
				continue
			}
			port, err := strconv.Atoi(v)
			if err != nil {
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

		for _, name := range names {
			if name == "" {
				continue
			}
			fqdn := strings.ToLower(name + "." + input.Domain)
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

	return newRecords, newSrvs
}
