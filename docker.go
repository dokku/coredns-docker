package docker

import (
	"context"
	"net"
	"strconv"
	"strings"
	"sync"

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

	ttl    uint32
	client *client.Client
	domain string

	mu      sync.RWMutex
	records map[string][]net.IP
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
	d.mu.RUnlock()

	if !ok {
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

	if !found && (qtype == dns.TypeA || qtype == dns.TypeAAAA) {
		// NODATA
		w.WriteMsg(m)
		return dns.RcodeSuccess, nil
	}

	if !found {
		// For other types, we don't have records
		return plugin.NextOrFailure(d.Name(), d.Next, ctx, w, r)
	}

	w.WriteMsg(m)
	requestSuccessCount.WithLabelValues(metrics.WithServer(ctx)).Inc()
	return dns.RcodeSuccess, nil
}

// Name implements the Handler interface.
func (d *Docker) Name() string { return pluginName }

func (d *Docker) startEventLoop(ctx context.Context) {
	log.Infof("Starting Docker event loop")

	// Initial sync
	d.syncRecords(ctx)

	filter := filters.NewArgs()
	filter.Add("type", "container")
	filter.Add("event", "start")
	filter.Add("event", "die")
	filter.Add("event", "destroy")
	filter.Add("event", "stop")
	filter.Add("event", "create")
	filter.Add("event", "restart")

	msgs, errs := d.client.Events(ctx, events.ListOptions{
		Filters: filter,
	})

	for {
		select {
		case <-ctx.Done():
			return
		case err := <-errs:
			log.Errorf("Docker event error: %v", err)
			// TODO: exponential backoff reconnect
			return
		case msg := <-msgs:
			log.Debugf("Docker event: %s %s %s", msg.Type, msg.Action, msg.Actor.ID)
			d.syncRecords(ctx)
		}
	}
}

func (d *Docker) syncRecords(ctx context.Context) {
	containers, err := d.client.ContainerList(ctx, container.ListOptions{})
	if err != nil {
		log.Errorf("Failed to list containers: %v", err)
		return
	}

	newRecords := make(map[string][]net.IP)

	for _, c := range containers {
		inspect, err := d.client.ContainerInspect(ctx, c.ID)
		if err != nil {
			log.Errorf("Failed to inspect container %s: %v", c.ID, err)
			continue
		}

		networkName := string(inspect.HostConfig.NetworkMode)
		if networkName == "" || networkName == "default" {
			networkName = "bridge"
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

		for _, name := range names {
			if name == "" {
				continue
			}
			fqdn := strings.ToLower(name + "." + d.domain)
			if !strings.HasSuffix(fqdn, ".") {
				fqdn += "."
			}
			newRecords[fqdn] = append(newRecords[fqdn], ip)
		}
	}

	d.mu.Lock()
	d.records = newRecords
	d.mu.Unlock()
	log.Debugf("Synced %d records", len(newRecords))
}
