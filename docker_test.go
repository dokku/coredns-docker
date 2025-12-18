package docker

import (
	"context"
	"net"
	"testing"

	"github.com/coredns/coredns/plugin/pkg/dnstest"
	"github.com/coredns/coredns/plugin/test"
	"github.com/miekg/dns"
)

func TestDocker(t *testing.T) {
	d := &Docker{
		Next:   test.ErrorHandler(),
		ttl:    DefaultTTL,
		domain: "docker.",
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
			Qname:  "nonexistent.docker.",
			Qtype:  dns.TypeA,
			Rcode:  dns.RcodeServerFailure, // Because Next is ErrorHandler
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
		domain:      "docker.",
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
