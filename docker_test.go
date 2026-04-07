package docker

import (
	"context"
	"net"
	"testing"

	"github.com/coredns/coredns/plugin/pkg/dnstest"
	"github.com/coredns/coredns/plugin/pkg/fall"
	"github.com/coredns/coredns/plugin/test"
	"github.com/miekg/dns"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestDocker(t *testing.T) {
	d := &Docker{
		Next:      test.ErrorHandler(),
		ttl:       DefaultTTL,
		connected: true,
		zones:     []string{"docker."},
		records: map[string][]net.IP{
			"web.docker.":          {net.ParseIP("172.17.0.2")},
			"db.docker.":           {net.ParseIP("172.17.0.3")},
			"ipv6.docker.":         {net.ParseIP("2001:db8::1")},
			"multi.docker.":        {net.ParseIP("172.17.0.4"), net.ParseIP("172.17.0.5")},
			"myproj.mysvc.docker.": {net.ParseIP("172.17.0.6")},
		},
		srvs: map[string][]srvRecord{
			"_http._tcp.web.docker.": {
				{target: "web.docker.", port: 80},
			},
			"_tcp._tcp.db.docker.": {
				{target: "db.docker.", port: 5432},
			},
			"_udp._udp.db.docker.": {
				{target: "db.docker.", port: 5432},
			},
		},
	}

	var cases = []test.Case{
		{
			Qname: "web.docker.",
			Qtype: dns.TypeA,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.A("web.docker.	30	IN	A	172.17.0.2"),
			},
		},
		{
			Qname: "db.docker.",
			Qtype: dns.TypeA,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.A("db.docker.	30	IN	A	172.17.0.3"),
			},
		},
		{
			Qname: "ipv6.docker.",
			Qtype: dns.TypeAAAA,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.AAAA("ipv6.docker.	30	IN	AAAA	2001:db8::1"),
			},
		},
		{
			Qname: "multi.docker.",
			Qtype: dns.TypeA,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.A("multi.docker.	30	IN	A	172.17.0.4"),
				test.A("multi.docker.	30	IN	A	172.17.0.5"),
			},
		},
		{
			Qname: "myproj.mysvc.docker.",
			Qtype: dns.TypeA,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.A("myproj.mysvc.docker.	30	IN	A	172.17.0.6"),
			},
		},
		{
			Qname: "_http._tcp.web.docker.",
			Qtype: dns.TypeSRV,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.SRV("_http._tcp.web.docker.	30	IN	SRV	10 10 80 web.docker."),
			},
		},
		{
			Qname: "_tcp._tcp.db.docker.",
			Qtype: dns.TypeSRV,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.SRV("_tcp._tcp.db.docker.	30	IN	SRV	10 10 5432 db.docker."),
			},
		},
		{
			Qname: "_udp._udp.db.docker.",
			Qtype: dns.TypeSRV,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.SRV("_udp._udp.db.docker.	30	IN	SRV	10 10 5432 db.docker."),
			},
		},
		{
			// NODATA: AAAA query for IPv4-only container returns empty answer
			Qname:  "web.docker.",
			Qtype:  dns.TypeAAAA,
			Rcode:  dns.RcodeSuccess,
			Answer: []dns.RR{},
		},
		{
			// NODATA: A query for IPv6-only container returns empty answer
			Qname:  "ipv6.docker.",
			Qtype:  dns.TypeA,
			Rcode:  dns.RcodeSuccess,
			Answer: []dns.RR{},
		},
		{
			Qname:  "nonexistent.docker.",
			Qtype:  dns.TypeA,
			Rcode:  dns.RcodeNameError, // NXDOMAIN: no fallthrough configured
			Answer: []dns.RR{},
		},
	}

	ctx := context.Background()

	for i, tc := range cases {
		r := tc.Msg()
		w := dnstest.NewRecorder(&test.ResponseWriter{})

		_, err := d.ServeDNS(ctx, w, r)
		if err != tc.Error {
			t.Errorf("Test %d: expected no error, got %v", i, err)
			continue
		}

		if w.Msg == nil {
			if tc.Rcode != dns.RcodeSuccess || len(tc.Answer) != 0 {
				t.Errorf("Test %d: nil message", i)
			}
			continue
		}

		if err := test.SortAndCheck(w.Msg, tc); err != nil {
			t.Errorf("Test %d: %v", i, err)
		}
	}
}

func TestDockerEmptyPrefix(t *testing.T) {
	d := &Docker{
		Next:        test.ErrorHandler(),
		ttl:         DefaultTTL,
		connected:   true,
		zones:       []string{"docker."},
		labelPrefix: "",
		srvs: map[string][]srvRecord{
			"_http._tcp.web.docker.": {
				{target: "web.docker.", port: 80},
			},
		},
	}

	tc := test.Case{
		Qname: "_http._tcp.web.docker.",
		Qtype: dns.TypeSRV,
		Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.SRV("_http._tcp.web.docker.	30	IN	SRV	10 10 80 web.docker."),
		},
	}

	r := tc.Msg()
	w := dnstest.NewRecorder(&test.ResponseWriter{})

	_, err := d.ServeDNS(context.Background(), w, r)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if err := test.SortAndCheck(w.Msg, tc); err != nil {
		t.Errorf("error: %v", err)
	}
}

func TestDockerFallthrough(t *testing.T) {
	d := &Docker{
		Next:      test.ErrorHandler(),
		ttl:       DefaultTTL,
		connected: true,
		zones:     []string{"docker."},
		Fall:      fall.Root,
		records: map[string][]net.IP{
			"web.docker.": {net.ParseIP("172.17.0.2")},
		},
		srvs: map[string][]srvRecord{},
	}

	var cases = []test.Case{
		{
			// Existing record still resolves normally
			Qname: "web.docker.",
			Qtype: dns.TypeA,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.A("web.docker.	30	IN	A	172.17.0.2"),
			},
		},
		{
			// Nonexistent name falls through to ErrorHandler (SERVFAIL)
			Qname:  "nonexistent.docker.",
			Qtype:  dns.TypeA,
			Rcode:  dns.RcodeServerFailure,
			Answer: []dns.RR{},
		},
	}

	ctx := context.Background()

	for i, tc := range cases {
		r := tc.Msg()
		w := dnstest.NewRecorder(&test.ResponseWriter{})

		_, err := d.ServeDNS(ctx, w, r)
		if err != tc.Error {
			t.Errorf("Test %d: expected error %v, got %v", i, tc.Error, err)
			continue
		}

		if w.Msg == nil {
			if tc.Rcode != dns.RcodeSuccess || len(tc.Answer) != 0 {
				t.Errorf("Test %d: nil message", i)
			}
			continue
		}

		if err := test.SortAndCheck(w.Msg, tc); err != nil {
			t.Errorf("Test %d: %v", i, err)
		}
	}
}

func TestDockerFallthroughZoneSpecific(t *testing.T) {
	d := &Docker{
		Next:      test.ErrorHandler(),
		ttl:       DefaultTTL,
		connected: true,
		zones:     []string{"docker."},
		Fall:      fall.F{Zones: []string{"other."}},
		records: map[string][]net.IP{
			"web.docker.": {net.ParseIP("172.17.0.2")},
		},
		srvs: map[string][]srvRecord{},
	}

	// Nonexistent name in docker. zone does NOT match fallthrough zone "other."
	// so it should return NXDOMAIN, not fall through
	tc := test.Case{
		Qname:  "nonexistent.docker.",
		Qtype:  dns.TypeA,
		Rcode:  dns.RcodeNameError,
		Answer: []dns.RR{},
	}

	r := tc.Msg()
	w := dnstest.NewRecorder(&test.ResponseWriter{})

	_, err := d.ServeDNS(context.Background(), w, r)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if err := test.SortAndCheck(w.Msg, tc); err != nil {
		t.Errorf("error: %v", err)
	}
}

func TestDockerMultiZone(t *testing.T) {
	d := &Docker{
		Next:      test.ErrorHandler(),
		ttl:       DefaultTTL,
		connected: true,
		zones:     []string{"docker.", "internal."},
		records: map[string][]net.IP{
			"web.docker.":   {net.ParseIP("172.17.0.2")},
			"web.internal.": {net.ParseIP("172.17.0.2")},
		},
		srvs: map[string][]srvRecord{
			"_http._tcp.web.docker.": {
				{target: "web.docker.", port: 80},
			},
			"_http._tcp.web.internal.": {
				{target: "web.internal.", port: 80},
			},
		},
	}

	var cases = []test.Case{
		{
			Qname: "web.docker.",
			Qtype: dns.TypeA,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.A("web.docker.	30	IN	A	172.17.0.2"),
			},
		},
		{
			Qname: "web.internal.",
			Qtype: dns.TypeA,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.A("web.internal.	30	IN	A	172.17.0.2"),
			},
		},
		{
			Qname: "_http._tcp.web.docker.",
			Qtype: dns.TypeSRV,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.SRV("_http._tcp.web.docker.	30	IN	SRV	10 10 80 web.docker."),
			},
		},
		{
			Qname: "_http._tcp.web.internal.",
			Qtype: dns.TypeSRV,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.SRV("_http._tcp.web.internal.	30	IN	SRV	10 10 80 web.internal."),
			},
		},
		{
			// Query for a zone not in the configured list falls through
			Qname:  "web.other.",
			Qtype:  dns.TypeA,
			Rcode:  dns.RcodeServerFailure,
			Answer: []dns.RR{},
		},
	}

	ctx := context.Background()

	for i, tc := range cases {
		r := tc.Msg()
		w := dnstest.NewRecorder(&test.ResponseWriter{})

		_, err := d.ServeDNS(ctx, w, r)
		if err != tc.Error {
			t.Errorf("Test %d: expected error %v, got %v", i, tc.Error, err)
			continue
		}

		if w.Msg == nil {
			if tc.Rcode != dns.RcodeSuccess || len(tc.Answer) != 0 {
				t.Errorf("Test %d: nil message", i)
			}
			continue
		}

		if err := test.SortAndCheck(w.Msg, tc); err != nil {
			t.Errorf("Test %d: %v", i, err)
		}
	}
}

func TestDockerStaleTTL(t *testing.T) {
	d := &Docker{
		Next:      test.ErrorHandler(),
		ttl:       DefaultTTL,
		connected: false,
		zones:     []string{"docker."},
		records: map[string][]net.IP{
			"web.docker.": {net.ParseIP("172.17.0.2")},
		},
		srvs: map[string][]srvRecord{
			"_http._tcp.web.docker.": {
				{target: "web.docker.", port: 80},
			},
		},
	}

	var cases = []test.Case{
		{
			Qname: "web.docker.",
			Qtype: dns.TypeA,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.A("web.docker.	5	IN	A	172.17.0.2"),
			},
		},
		{
			Qname: "_http._tcp.web.docker.",
			Qtype: dns.TypeSRV,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.SRV("_http._tcp.web.docker.	5	IN	SRV	10 10 80 web.docker."),
			},
		},
	}

	ctx := context.Background()

	for i, tc := range cases {
		r := tc.Msg()
		w := dnstest.NewRecorder(&test.ResponseWriter{})

		_, err := d.ServeDNS(ctx, w, r)
		if err != tc.Error {
			t.Errorf("Test %d: expected no error, got %v", i, err)
			continue
		}

		if err := test.SortAndCheck(w.Msg, tc); err != nil {
			t.Errorf("Test %d: %v", i, err)
		}
	}
}

func TestDockerStaleTTLLowTTL(t *testing.T) {
	d := &Docker{
		Next:      test.ErrorHandler(),
		ttl:       3,
		connected: false,
		zones:     []string{"docker."},
		records: map[string][]net.IP{
			"web.docker.": {net.ParseIP("172.17.0.2")},
		},
		srvs: map[string][]srvRecord{},
	}

	tc := test.Case{
		Qname: "web.docker.",
		Qtype: dns.TypeA,
		Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.A("web.docker.	3	IN	A	172.17.0.2"),
		},
	}

	r := tc.Msg()
	w := dnstest.NewRecorder(&test.ResponseWriter{})

	_, err := d.ServeDNS(context.Background(), w, r)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if err := test.SortAndCheck(w.Msg, tc); err != nil {
		t.Errorf("error: %v", err)
	}
}

func TestDockerConnectedNormalTTL(t *testing.T) {
	d := &Docker{
		Next:      test.ErrorHandler(),
		ttl:       DefaultTTL,
		connected: true,
		zones:     []string{"docker."},
		records: map[string][]net.IP{
			"web.docker.": {net.ParseIP("172.17.0.2")},
		},
		srvs: map[string][]srvRecord{},
	}

	tc := test.Case{
		Qname: "web.docker.",
		Qtype: dns.TypeA,
		Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.A("web.docker.	30	IN	A	172.17.0.2"),
		},
	}

	r := tc.Msg()
	w := dnstest.NewRecorder(&test.ResponseWriter{})

	_, err := d.ServeDNS(context.Background(), w, r)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if err := test.SortAndCheck(w.Msg, tc); err != nil {
		t.Errorf("error: %v", err)
	}
}

func getHistogramSampleCount(obs prometheus.Observer) uint64 {
	metric := obs.(prometheus.Metric)
	m := &dto.Metric{}
	_ = metric.Write(m)
	return m.GetHistogram().GetSampleCount()
}

func TestServeDNSRequestDurationMetric(t *testing.T) {
	d := &Docker{
		Next:      test.ErrorHandler(),
		ttl:       DefaultTTL,
		connected: true,
		zones:     []string{"docker."},
		records: map[string][]net.IP{
			"web.docker.": {net.ParseIP("172.17.0.2")},
		},
		srvs: map[string][]srvRecord{},
	}

	obs := requestDuration.WithLabelValues("", "A")
	beforeCount := getHistogramSampleCount(obs)

	m := new(dns.Msg)
	m.SetQuestion("web.docker.", dns.TypeA)
	w := dnstest.NewRecorder(&test.ResponseWriter{})

	_, err := d.ServeDNS(context.Background(), w, m)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	afterCount := getHistogramSampleCount(obs)
	if afterCount != beforeCount+1 {
		t.Errorf("expected requestDuration sample count to increase by 1, before=%d after=%d", beforeCount, afterCount)
	}
}

func TestServeDNSStaleRequestMetric(t *testing.T) {
	d := &Docker{
		Next:      test.ErrorHandler(),
		ttl:       DefaultTTL,
		connected: false,
		zones:     []string{"docker."},
		records: map[string][]net.IP{
			"web.docker.": {net.ParseIP("172.17.0.2")},
		},
		srvs: map[string][]srvRecord{},
	}

	beforeCount := testutil.ToFloat64(requestStaleCount.WithLabelValues(""))

	m := new(dns.Msg)
	m.SetQuestion("web.docker.", dns.TypeA)
	w := dnstest.NewRecorder(&test.ResponseWriter{})

	_, err := d.ServeDNS(context.Background(), w, m)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	afterCount := testutil.ToFloat64(requestStaleCount.WithLabelValues(""))
	if afterCount != beforeCount+1 {
		t.Errorf("expected stale_requests_total to increment by 1, before=%f after=%f", beforeCount, afterCount)
	}
}
