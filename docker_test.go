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
			"web.docker.":   {net.ParseIP("172.17.0.2")},
			"db.docker.":    {net.ParseIP("172.17.0.3")},
			"ipv6.docker.":  {net.ParseIP("2001:db8::1")},
			"multi.docker.": {net.ParseIP("172.17.0.4"), net.ParseIP("172.17.0.5")},
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
