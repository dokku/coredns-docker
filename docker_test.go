package docker

import (
	"bytes"
	"context"
	"errors"
	golog "log"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	clog "github.com/coredns/coredns/plugin/pkg/log"

	"github.com/coredns/coredns/plugin/pkg/dnstest"
	"github.com/docker/docker/client"
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
		ptrs: map[string][]string{
			"2.0.17.172.in-addr.arpa.":                                                      {"web.docker."},
			"3.0.17.172.in-addr.arpa.":                                                      {"db.docker."},
			"1.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.8.b.d.0.1.0.0.2.ip6.arpa.": {"ipv6.docker."},
			"4.0.17.172.in-addr.arpa.":                                                      {"multi.docker."},
			"5.0.17.172.in-addr.arpa.":                                                      {"multi.docker."},
			"6.0.17.172.in-addr.arpa.":                                                      {"myproj.mysvc.docker."},
		},
		txts: map[string][][]string{
			// TXT on a name that also has an A record (web.docker.)
			"web.docker.": {{"v=spf1 -all"}},
			// Keyed TXT at a TXT-only FQDN — no A record here. Proves that
			// txtOk participates in the ServeDNS "does this name exist"
			// check so the query does not fall through to NXDOMAIN.
			"info.web.docker.": {{"version=1.0.0"}},
			// Multiple TXT RRs on the same FQDN — response contains N answers.
			"multi-txt.docker.": {{"one"}, {"two"}},
			// One TXT RR containing two character-strings — response contains
			// a single answer whose Txt slice has length 2.
			"multi-str.docker.": {{"part1", "part2"}},
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
			// TXT query on a name that has both A and TXT records
			Qname: "web.docker.",
			Qtype: dns.TypeTXT,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.TXT("web.docker.	30	IN	TXT	\"v=spf1 -all\""),
			},
		},
		{
			// TXT query on a TXT-only FQDN (no A record). This specifically
			// verifies that txtOk participates in the ServeDNS existence
			// check — without it, the name would look nonexistent.
			Qname: "info.web.docker.",
			Qtype: dns.TypeTXT,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.TXT("info.web.docker.	30	IN	TXT	\"version=1.0.0\""),
			},
		},
		{
			// NODATA: A query on a TXT-only FQDN. Name exists (via TXT)
			// but has no A record, so the response is empty with SOA in
			// the authority section. Proves the new NODATA condition
			// correctly handles "name exists only for TXT".
			Qname:  "info.web.docker.",
			Qtype:  dns.TypeA,
			Rcode:  dns.RcodeSuccess,
			Answer: []dns.RR{},
			Ns: []dns.RR{
				test.SOA("docker. 30 IN SOA ns.dns.docker. hostmaster.docker. 0 7200 1800 86400 30"),
			},
		},
		{
			// Multiple TXT RRs on the same FQDN → multiple answers.
			Qname: "multi-txt.docker.",
			Qtype: dns.TypeTXT,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.TXT("multi-txt.docker.	30	IN	TXT	\"one\""),
				test.TXT("multi-txt.docker.	30	IN	TXT	\"two\""),
			},
		},
		{
			// One TXT RR with two character-strings → one answer whose
			// text-rendered form contains both strings.
			Qname: "multi-str.docker.",
			Qtype: dns.TypeTXT,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.TXT("multi-str.docker.	30	IN	TXT	\"part1\" \"part2\""),
			},
		},
		{
			// NODATA: TXT query on a name that has only A records.
			// Proves TXT is included in the NODATA condition so queries
			// for unsupported types return NOERROR with SOA, not NXDOMAIN.
			Qname:  "db.docker.",
			Qtype:  dns.TypeTXT,
			Rcode:  dns.RcodeSuccess,
			Answer: []dns.RR{},
			Ns: []dns.RR{
				test.SOA("docker. 30 IN SOA ns.dns.docker. hostmaster.docker. 0 7200 1800 86400 30"),
			},
		},
		{
			// NXDOMAIN for a TXT query on an entirely unknown name.
			Qname:  "nope.docker.",
			Qtype:  dns.TypeTXT,
			Rcode:  dns.RcodeNameError,
			Answer: []dns.RR{},
			Ns: []dns.RR{
				test.SOA("docker. 30 IN SOA ns.dns.docker. hostmaster.docker. 0 7200 1800 86400 30"),
			},
		},
		{
			// NODATA: AAAA query for IPv4-only container returns empty answer with SOA in authority
			Qname:  "web.docker.",
			Qtype:  dns.TypeAAAA,
			Rcode:  dns.RcodeSuccess,
			Answer: []dns.RR{},
			Ns: []dns.RR{
				test.SOA("docker. 30 IN SOA ns.dns.docker. hostmaster.docker. 0 7200 1800 86400 30"),
			},
		},
		{
			// NODATA: A query for IPv6-only container returns empty answer with SOA in authority
			Qname:  "ipv6.docker.",
			Qtype:  dns.TypeA,
			Rcode:  dns.RcodeSuccess,
			Answer: []dns.RR{},
			Ns: []dns.RR{
				test.SOA("docker. 30 IN SOA ns.dns.docker. hostmaster.docker. 0 7200 1800 86400 30"),
			},
		},
		{
			// NXDOMAIN with SOA in authority
			Qname:  "nonexistent.docker.",
			Qtype:  dns.TypeA,
			Rcode:  dns.RcodeNameError,
			Answer: []dns.RR{},
			Ns: []dns.RR{
				test.SOA("docker. 30 IN SOA ns.dns.docker. hostmaster.docker. 0 7200 1800 86400 30"),
			},
		},
		{
			// SOA query at zone apex
			Qname: "docker.",
			Qtype: dns.TypeSOA,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.SOA("docker. 30 IN SOA ns.dns.docker. hostmaster.docker. 0 7200 1800 86400 30"),
			},
		},
		{
			// NS query at zone apex
			Qname: "docker.",
			Qtype: dns.TypeNS,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.NS("docker. 30 IN NS ns.dns.docker."),
			},
		},
		{
			// NS query for non-apex name returns NODATA with SOA in authority
			Qname:  "web.docker.",
			Qtype:  dns.TypeNS,
			Rcode:  dns.RcodeSuccess,
			Answer: []dns.RR{},
			Ns: []dns.RR{
				test.SOA("docker. 30 IN SOA ns.dns.docker. hostmaster.docker. 0 7200 1800 86400 30"),
			},
		},
		{
			// NODATA: MX query for existing name returns empty answer with SOA in authority
			Qname:  "web.docker.",
			Qtype:  dns.TypeMX,
			Rcode:  dns.RcodeSuccess,
			Answer: []dns.RR{},
			Ns: []dns.RR{
				test.SOA("docker. 30 IN SOA ns.dns.docker. hostmaster.docker. 0 7200 1800 86400 30"),
			},
		},
		{
			// PTR record for IPv4
			Qname: "2.0.17.172.in-addr.arpa.",
			Qtype: dns.TypePTR,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.PTR("2.0.17.172.in-addr.arpa. 30 IN PTR web.docker."),
			},
		},
		{
			// PTR record for IPv6
			Qname: "1.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.8.b.d.0.1.0.0.2.ip6.arpa.",
			Qtype: dns.TypePTR,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.PTR("1.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.8.b.d.0.1.0.0.2.ip6.arpa. 30 IN PTR ipv6.docker."),
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

func TestDockerPTR(t *testing.T) {
	d := &Docker{
		Next:      test.ErrorHandler(),
		ttl:       DefaultTTL,
		connected: true,
		zones:     []string{"docker."},
		records: map[string][]net.IP{
			"web.docker.": {net.ParseIP("172.17.0.2")},
			"app.docker.": {net.ParseIP("172.17.0.2")},
		},
		srvs: map[string][]srvRecord{},
		ptrs: map[string][]string{
			"2.0.17.172.in-addr.arpa.": {"web.docker.", "app.docker."},
		},
	}

	var cases = []test.Case{
		{
			// PTR with multiple FQDNs (sorted alphabetically for SortAndCheck)
			Qname: "2.0.17.172.in-addr.arpa.",
			Qtype: dns.TypePTR,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.PTR("2.0.17.172.in-addr.arpa. 30 IN PTR app.docker."),
				test.PTR("2.0.17.172.in-addr.arpa. 30 IN PTR web.docker."),
			},
		},
		{
			// Unknown reverse IP passes to next plugin (SERVFAIL from ErrorHandler)
			Qname:  "9.9.9.9.in-addr.arpa.",
			Qtype:  dns.TypePTR,
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
			t.Errorf("Test %d (%s): expected error %v, got %v", i, tc.Qname, tc.Error, err)
			continue
		}

		if w.Msg == nil {
			if tc.Rcode != dns.RcodeSuccess || len(tc.Answer) != 0 {
				t.Errorf("Test %d (%s): nil message", i, tc.Qname)
			}
			continue
		}

		if err := test.SortAndCheck(w.Msg, tc); err != nil {
			t.Errorf("Test %d (%s): %v", i, tc.Qname, err)
		}
	}
}

func TestDockerPTRStaleTTL(t *testing.T) {
	d := &Docker{
		Next:      test.ErrorHandler(),
		ttl:       DefaultTTL,
		connected: false,
		zones:     []string{"docker."},
		records:   map[string][]net.IP{},
		srvs:      map[string][]srvRecord{},
		ptrs: map[string][]string{
			"2.0.17.172.in-addr.arpa.": {"web.docker."},
		},
	}

	tc := test.Case{
		Qname: "2.0.17.172.in-addr.arpa.",
		Qtype: dns.TypePTR,
		Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.PTR("2.0.17.172.in-addr.arpa. 5 IN PTR web.docker."),
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
		ptrs: map[string][]string{},
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

func TestDockerWildcard(t *testing.T) {
	d := &Docker{
		Next:      test.ErrorHandler(),
		ttl:       DefaultTTL,
		connected: true,
		zones:     []string{"docker."},
		records: map[string][]net.IP{
			"web.docker.":   {net.ParseIP("172.17.0.2")},
			"*.web.docker.": {net.ParseIP("172.17.0.2")},
			"db.docker.":    {net.ParseIP("172.17.0.3")},
		},
		srvs: map[string][]srvRecord{
			"_http._tcp.web.docker.": {
				{target: "web.docker.", port: 80},
			},
			"_http._tcp.*.web.docker.": {
				{target: "web.docker.", port: 80},
			},
		},
		ptrs: map[string][]string{},
	}

	var cases = []test.Case{
		{
			// Wildcard A match: anything.web.docker. matches *.web.docker.
			Qname: "anything.web.docker.",
			Qtype: dns.TypeA,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.A("anything.web.docker.	30	IN	A	172.17.0.2"),
			},
		},
		{
			// Exact match takes precedence over wildcard
			Qname: "web.docker.",
			Qtype: dns.TypeA,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.A("web.docker.	30	IN	A	172.17.0.2"),
			},
		},
		{
			// Deep subdomain does NOT match wildcard per RFC 4592
			Qname:  "deep.sub.web.docker.",
			Qtype:  dns.TypeA,
			Rcode:  dns.RcodeNameError,
			Answer: []dns.RR{},
			Ns: []dns.RR{
				test.SOA("docker. 30 IN SOA ns.dns.docker. hostmaster.docker. 0 7200 1800 86400 30"),
			},
		},
		{
			// Wildcard NODATA: AAAA query for IPv4-only wildcard
			Qname:  "anything.web.docker.",
			Qtype:  dns.TypeAAAA,
			Rcode:  dns.RcodeSuccess,
			Answer: []dns.RR{},
			Ns: []dns.RR{
				test.SOA("docker. 30 IN SOA ns.dns.docker. hostmaster.docker. 0 7200 1800 86400 30"),
			},
		},
		{
			// Wildcard SRV match
			Qname: "_http._tcp.anything.web.docker.",
			Qtype: dns.TypeSRV,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.SRV("_http._tcp.anything.web.docker.	30	IN	SRV	10 10 80 web.docker."),
			},
		},
		{
			// No wildcard NXDOMAIN: query where no wildcard exists
			Qname:  "anything.db.docker.",
			Qtype:  dns.TypeA,
			Rcode:  dns.RcodeNameError,
			Answer: []dns.RR{},
			Ns: []dns.RR{
				test.SOA("docker. 30 IN SOA ns.dns.docker. hostmaster.docker. 0 7200 1800 86400 30"),
			},
		},
	}

	ctx := context.Background()

	for i, tc := range cases {
		r := tc.Msg()
		w := dnstest.NewRecorder(&test.ResponseWriter{})

		_, err := d.ServeDNS(ctx, w, r)
		if err != tc.Error {
			t.Errorf("Test %d (%s): expected no error, got %v", i, tc.Qname, err)
			continue
		}

		if w.Msg == nil {
			if tc.Rcode != dns.RcodeSuccess || len(tc.Answer) != 0 {
				t.Errorf("Test %d (%s): nil message", i, tc.Qname)
			}
			continue
		}

		if err := test.SortAndCheck(w.Msg, tc); err != nil {
			t.Errorf("Test %d (%s): %v", i, tc.Qname, err)
		}
	}
}

func TestDockerCNAME(t *testing.T) {
	d := &Docker{
		Next:      test.ErrorHandler(),
		ttl:       DefaultTTL,
		connected: true,
		zones:     []string{"docker."},
		records: map[string][]net.IP{
			"other.docker.": {net.ParseIP("172.17.0.3")},
		},
		srvs: map[string][]srvRecord{},
		ptrs: map[string][]string{},
		cnames: map[string]string{
			"web.docker.":     "external.example.com.",
			"alias.docker.":   "external.example.com.",
			"*.wild.docker.":  "external.example.com.",
		},
	}

	var cases = []test.Case{
		{
			// CNAME type query returns the CNAME record.
			Qname: "web.docker.",
			Qtype: dns.TypeCNAME,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.CNAME("web.docker.	30	IN	CNAME	external.example.com."),
			},
		},
		{
			// A query on a CNAME name returns the CNAME; resolver chases it.
			Qname: "web.docker.",
			Qtype: dns.TypeA,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.CNAME("web.docker.	30	IN	CNAME	external.example.com."),
			},
		},
		{
			// AAAA query on a CNAME name also returns the CNAME.
			Qname: "web.docker.",
			Qtype: dns.TypeAAAA,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.CNAME("web.docker.	30	IN	CNAME	external.example.com."),
			},
		},
		{
			// Alias name also resolves to the same CNAME target.
			Qname: "alias.docker.",
			Qtype: dns.TypeA,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.CNAME("alias.docker.	30	IN	CNAME	external.example.com."),
			},
		},
		{
			// Wildcard CNAME match.
			Qname: "anything.wild.docker.",
			Qtype: dns.TypeA,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.CNAME("anything.wild.docker.	30	IN	CNAME	external.example.com."),
			},
		},
		{
			// Non-cname name is unaffected.
			Qname: "other.docker.",
			Qtype: dns.TypeA,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.A("other.docker.	30	IN	A	172.17.0.3"),
			},
		},
	}

	ctx := context.Background()

	for i, tc := range cases {
		r := tc.Msg()
		w := dnstest.NewRecorder(&test.ResponseWriter{})

		_, err := d.ServeDNS(ctx, w, r)
		if err != tc.Error {
			t.Errorf("Test %d (%s): expected no error, got %v", i, tc.Qname, err)
			continue
		}

		if w.Msg == nil {
			if tc.Rcode != dns.RcodeSuccess || len(tc.Answer) != 0 {
				t.Errorf("Test %d (%s): nil message", i, tc.Qname)
			}
			continue
		}

		if err := test.SortAndCheck(w.Msg, tc); err != nil {
			t.Errorf("Test %d (%s): %v", i, tc.Qname, err)
		}
	}
}

func TestDockerCNAMEStaleTTL(t *testing.T) {
	d := &Docker{
		Next:      test.ErrorHandler(),
		ttl:       DefaultTTL,
		connected: false,
		zones:     []string{"docker."},
		records:   map[string][]net.IP{},
		srvs:      map[string][]srvRecord{},
		ptrs:      map[string][]string{},
		cnames: map[string]string{
			"web.docker.": "external.example.com.",
		},
	}

	tc := test.Case{
		Qname: "web.docker.",
		Qtype: dns.TypeCNAME,
		Rcode: dns.RcodeSuccess,
		Answer: []dns.RR{
			test.CNAME("web.docker.	5	IN	CNAME	external.example.com."),
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
		ptrs: map[string][]string{},
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
		ptrs: map[string][]string{},
	}

	// Nonexistent name in docker. zone does NOT match fallthrough zone "other."
	// so it should return NXDOMAIN, not fall through
	tc := test.Case{
		Qname:  "nonexistent.docker.",
		Qtype:  dns.TypeA,
		Rcode:  dns.RcodeNameError,
		Answer: []dns.RR{},
		Ns: []dns.RR{
			test.SOA("docker. 30 IN SOA ns.dns.docker. hostmaster.docker. 0 7200 1800 86400 30"),
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
		ptrs: map[string][]string{},
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

func TestDockerSOAMultiZone(t *testing.T) {
	d := &Docker{
		Next:      test.ErrorHandler(),
		ttl:       DefaultTTL,
		connected: true,
		zones:     []string{"docker.", "internal."},
		records: map[string][]net.IP{
			"web.docker.":   {net.ParseIP("172.17.0.2")},
			"web.internal.": {net.ParseIP("172.17.0.2")},
		},
		srvs: map[string][]srvRecord{},
		ptrs: map[string][]string{},
	}

	var cases = []test.Case{
		{
			// SOA query for first zone
			Qname: "docker.",
			Qtype: dns.TypeSOA,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.SOA("docker. 30 IN SOA ns.dns.docker. hostmaster.docker. 0 7200 1800 86400 30"),
			},
		},
		{
			// SOA query for second zone
			Qname: "internal.",
			Qtype: dns.TypeSOA,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.SOA("internal. 30 IN SOA ns.dns.internal. hostmaster.internal. 0 7200 1800 86400 30"),
			},
		},
		{
			// NS query for first zone
			Qname: "docker.",
			Qtype: dns.TypeNS,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.NS("docker. 30 IN NS ns.dns.docker."),
			},
		},
		{
			// NS query for second zone
			Qname: "internal.",
			Qtype: dns.TypeNS,
			Rcode: dns.RcodeSuccess,
			Answer: []dns.RR{
				test.NS("internal. 30 IN NS ns.dns.internal."),
			},
		},
		{
			// NXDOMAIN in docker. zone has SOA for docker.
			Qname:  "nonexistent.docker.",
			Qtype:  dns.TypeA,
			Rcode:  dns.RcodeNameError,
			Answer: []dns.RR{},
			Ns: []dns.RR{
				test.SOA("docker. 30 IN SOA ns.dns.docker. hostmaster.docker. 0 7200 1800 86400 30"),
			},
		},
		{
			// NXDOMAIN in internal. zone has SOA for internal.
			Qname:  "nonexistent.internal.",
			Qtype:  dns.TypeA,
			Rcode:  dns.RcodeNameError,
			Answer: []dns.RR{},
			Ns: []dns.RR{
				test.SOA("internal. 30 IN SOA ns.dns.internal. hostmaster.internal. 0 7200 1800 86400 30"),
			},
		},
		{
			// NODATA in docker. zone has SOA for docker.
			Qname:  "web.docker.",
			Qtype:  dns.TypeAAAA,
			Rcode:  dns.RcodeSuccess,
			Answer: []dns.RR{},
			Ns: []dns.RR{
				test.SOA("docker. 30 IN SOA ns.dns.docker. hostmaster.docker. 0 7200 1800 86400 30"),
			},
		},
		{
			// NODATA in internal. zone has SOA for internal.
			Qname:  "web.internal.",
			Qtype:  dns.TypeAAAA,
			Rcode:  dns.RcodeSuccess,
			Answer: []dns.RR{},
			Ns: []dns.RR{
				test.SOA("internal. 30 IN SOA ns.dns.internal. hostmaster.internal. 0 7200 1800 86400 30"),
			},
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
		ptrs: map[string][]string{},
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
		ptrs: map[string][]string{},
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
		ptrs: map[string][]string{},
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
		ptrs: map[string][]string{},
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
		ptrs: map[string][]string{},
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

func TestServeDNSNXDOMAINSuccessMetric(t *testing.T) {
	d := &Docker{
		Next:      test.ErrorHandler(),
		ttl:       DefaultTTL,
		connected: true,
		zones:     []string{"docker."},
		records:   map[string][]net.IP{},
		srvs:      map[string][]srvRecord{},
		ptrs:      map[string][]string{},
	}

	beforeSuccess := testutil.ToFloat64(requestSuccessCount.WithLabelValues(""))
	beforeFailed := testutil.ToFloat64(requestFailedCount.WithLabelValues(""))

	m := new(dns.Msg)
	m.SetQuestion("nonexistent.docker.", dns.TypeA)
	w := dnstest.NewRecorder(&test.ResponseWriter{})

	_, err := d.ServeDNS(context.Background(), w, m)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	afterSuccess := testutil.ToFloat64(requestSuccessCount.WithLabelValues(""))
	afterFailed := testutil.ToFloat64(requestFailedCount.WithLabelValues(""))
	if afterSuccess != beforeSuccess+1 {
		t.Errorf("expected success_requests_total to increment by 1, before=%f after=%f", beforeSuccess, afterSuccess)
	}
	if afterFailed != beforeFailed {
		t.Errorf("expected failed_requests_total to not change, before=%f after=%f", beforeFailed, afterFailed)
	}
}

func TestServeDNSNODATATypeMismatchSuccessMetric(t *testing.T) {
	d := &Docker{
		Next:      test.ErrorHandler(),
		ttl:       DefaultTTL,
		connected: true,
		zones:     []string{"docker."},
		records: map[string][]net.IP{
			"web.docker.": {net.ParseIP("172.17.0.2")},
		},
		srvs: map[string][]srvRecord{},
		ptrs: map[string][]string{},
	}

	beforeSuccess := testutil.ToFloat64(requestSuccessCount.WithLabelValues(""))
	beforeFailed := testutil.ToFloat64(requestFailedCount.WithLabelValues(""))

	m := new(dns.Msg)
	m.SetQuestion("web.docker.", dns.TypeAAAA)
	w := dnstest.NewRecorder(&test.ResponseWriter{})

	_, err := d.ServeDNS(context.Background(), w, m)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	afterSuccess := testutil.ToFloat64(requestSuccessCount.WithLabelValues(""))
	afterFailed := testutil.ToFloat64(requestFailedCount.WithLabelValues(""))
	if afterSuccess != beforeSuccess+1 {
		t.Errorf("expected success_requests_total to increment by 1, before=%f after=%f", beforeSuccess, afterSuccess)
	}
	if afterFailed != beforeFailed {
		t.Errorf("expected failed_requests_total to not change, before=%f after=%f", beforeFailed, afterFailed)
	}
}

func TestServeDNSNODATAUnsupportedTypeSuccessMetric(t *testing.T) {
	d := &Docker{
		Next:      test.ErrorHandler(),
		ttl:       DefaultTTL,
		connected: true,
		zones:     []string{"docker."},
		records: map[string][]net.IP{
			"web.docker.": {net.ParseIP("172.17.0.2")},
		},
		srvs: map[string][]srvRecord{},
		ptrs: map[string][]string{},
	}

	beforeSuccess := testutil.ToFloat64(requestSuccessCount.WithLabelValues(""))
	beforeFailed := testutil.ToFloat64(requestFailedCount.WithLabelValues(""))

	m := new(dns.Msg)
	m.SetQuestion("web.docker.", dns.TypeMX)
	w := dnstest.NewRecorder(&test.ResponseWriter{})

	_, err := d.ServeDNS(context.Background(), w, m)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	afterSuccess := testutil.ToFloat64(requestSuccessCount.WithLabelValues(""))
	afterFailed := testutil.ToFloat64(requestFailedCount.WithLabelValues(""))
	if afterSuccess != beforeSuccess+1 {
		t.Errorf("expected success_requests_total to increment by 1, before=%f after=%f", beforeSuccess, afterSuccess)
	}
	if afterFailed != beforeFailed {
		t.Errorf("expected failed_requests_total to not change, before=%f after=%f", beforeFailed, afterFailed)
	}
}

func TestServeDNSFallthroughNXDOMAINMetric(t *testing.T) {
	d := &Docker{
		Next:      test.ErrorHandler(),
		ttl:       DefaultTTL,
		connected: true,
		zones:     []string{"docker."},
		Fall:      fall.Root,
		records:   map[string][]net.IP{},
		srvs:      map[string][]srvRecord{},
		ptrs:      map[string][]string{},
	}

	beforeFallthrough := testutil.ToFloat64(requestFallthroughCount.WithLabelValues(""))
	beforeSuccess := testutil.ToFloat64(requestSuccessCount.WithLabelValues(""))

	m := new(dns.Msg)
	m.SetQuestion("nonexistent.docker.", dns.TypeA)
	w := dnstest.NewRecorder(&test.ResponseWriter{})

	d.ServeDNS(context.Background(), w, m)

	afterFallthrough := testutil.ToFloat64(requestFallthroughCount.WithLabelValues(""))
	afterSuccess := testutil.ToFloat64(requestSuccessCount.WithLabelValues(""))
	if afterFallthrough != beforeFallthrough+1 {
		t.Errorf("expected fallthrough_requests_total to increment by 1, before=%f after=%f", beforeFallthrough, afterFallthrough)
	}
	if afterSuccess != beforeSuccess {
		t.Errorf("expected success_requests_total to not change on fallthrough, before=%f after=%f", beforeSuccess, afterSuccess)
	}
}

func TestServeDNSFallthroughUnsupportedTypeMetric(t *testing.T) {
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
		ptrs: map[string][]string{},
	}

	beforeFallthrough := testutil.ToFloat64(requestFallthroughCount.WithLabelValues(""))

	m := new(dns.Msg)
	m.SetQuestion("web.docker.", dns.TypeMX)
	w := dnstest.NewRecorder(&test.ResponseWriter{})

	d.ServeDNS(context.Background(), w, m)

	afterFallthrough := testutil.ToFloat64(requestFallthroughCount.WithLabelValues(""))
	if afterFallthrough != beforeFallthrough+1 {
		t.Errorf("expected fallthrough_requests_total to increment by 1, before=%f after=%f", beforeFallthrough, afterFallthrough)
	}
}

func TestServeDNSFallthroughZoneMissMetric(t *testing.T) {
	d := &Docker{
		Next:      test.ErrorHandler(),
		ttl:       DefaultTTL,
		connected: true,
		zones:     []string{"docker."},
		records:   map[string][]net.IP{},
		srvs:      map[string][]srvRecord{},
		ptrs:      map[string][]string{},
	}

	beforeFallthrough := testutil.ToFloat64(requestFallthroughCount.WithLabelValues(""))

	m := new(dns.Msg)
	m.SetQuestion("web.other.", dns.TypeA)
	w := dnstest.NewRecorder(&test.ResponseWriter{})

	d.ServeDNS(context.Background(), w, m)

	afterFallthrough := testutil.ToFloat64(requestFallthroughCount.WithLabelValues(""))
	if afterFallthrough != beforeFallthrough+1 {
		t.Errorf("expected fallthrough_requests_total to increment by 1, before=%f after=%f", beforeFallthrough, afterFallthrough)
	}
}

func TestServeDNSFallthroughPTRNotFoundMetric(t *testing.T) {
	d := &Docker{
		Next:      test.ErrorHandler(),
		ttl:       DefaultTTL,
		connected: true,
		zones:     []string{"docker."},
		records:   map[string][]net.IP{},
		srvs:      map[string][]srvRecord{},
		ptrs:      map[string][]string{},
	}

	beforeFallthrough := testutil.ToFloat64(requestFallthroughCount.WithLabelValues(""))

	m := new(dns.Msg)
	m.SetQuestion("9.9.9.9.in-addr.arpa.", dns.TypePTR)
	w := dnstest.NewRecorder(&test.ResponseWriter{})

	d.ServeDNS(context.Background(), w, m)

	afterFallthrough := testutil.ToFloat64(requestFallthroughCount.WithLabelValues(""))
	if afterFallthrough != beforeFallthrough+1 {
		t.Errorf("expected fallthrough_requests_total to increment by 1, before=%f after=%f", beforeFallthrough, afterFallthrough)
	}
}

func enableDebugLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	clog.D.Set()
	golog.SetOutput(&buf)
	t.Cleanup(func() {
		clog.D.Clear()
		golog.SetOutput(os.Stderr)
	})
	return &buf
}

func TestServeDNSDebugLogging(t *testing.T) {
	t.Run("query_extraction", func(t *testing.T) {
		buf := enableDebugLog(t)

		d := &Docker{
			Next:      test.ErrorHandler(),
			ttl:       DefaultTTL,
			connected: true,
			zones:     []string{"docker."},
			records: map[string][]net.IP{
				"web.docker.": {net.ParseIP("172.17.0.2")},
			},
			srvs: map[string][]srvRecord{},
			ptrs: map[string][]string{},
		}

		m := new(dns.Msg)
		m.SetQuestion("web.docker.", dns.TypeA)
		w := dnstest.NewRecorder(&test.ResponseWriter{})
		d.ServeDNS(context.Background(), w, m)

		output := buf.String()
		if !strings.Contains(output, "Query: qname=web.docker. qtype=A") {
			t.Errorf("expected query extraction debug log, got: %s", output)
		}
	})

	t.Run("zone_mismatch", func(t *testing.T) {
		buf := enableDebugLog(t)

		d := &Docker{
			Next:      test.ErrorHandler(),
			ttl:       DefaultTTL,
			connected: true,
			zones:     []string{"docker."},
			records:   map[string][]net.IP{},
			srvs:      map[string][]srvRecord{},
			ptrs:      map[string][]string{},
		}

		m := new(dns.Msg)
		m.SetQuestion("web.other.", dns.TypeA)
		w := dnstest.NewRecorder(&test.ResponseWriter{})
		d.ServeDNS(context.Background(), w, m)

		output := buf.String()
		if !strings.Contains(output, "not in zones [docker.]") {
			t.Errorf("expected zone mismatch debug log, got: %s", output)
		}
	})

	t.Run("lookup_results", func(t *testing.T) {
		buf := enableDebugLog(t)

		d := &Docker{
			Next:      test.ErrorHandler(),
			ttl:       DefaultTTL,
			connected: true,
			zones:     []string{"docker."},
			records: map[string][]net.IP{
				"web.docker.": {net.ParseIP("172.17.0.2")},
			},
			srvs: map[string][]srvRecord{},
			ptrs: map[string][]string{},
		}

		m := new(dns.Msg)
		m.SetQuestion("web.docker.", dns.TypeA)
		w := dnstest.NewRecorder(&test.ResponseWriter{})
		d.ServeDNS(context.Background(), w, m)

		output := buf.String()
		if !strings.Contains(output, "Lookup results for web.docker.") {
			t.Errorf("expected lookup results debug log, got: %s", output)
		}
	})

	t.Run("nxdomain", func(t *testing.T) {
		buf := enableDebugLog(t)

		d := &Docker{
			Next:      test.ErrorHandler(),
			ttl:       DefaultTTL,
			connected: true,
			zones:     []string{"docker."},
			records:   map[string][]net.IP{},
			srvs:      map[string][]srvRecord{},
			ptrs:      map[string][]string{},
		}

		m := new(dns.Msg)
		m.SetQuestion("nonexistent.docker.", dns.TypeA)
		w := dnstest.NewRecorder(&test.ResponseWriter{})
		d.ServeDNS(context.Background(), w, m)

		output := buf.String()
		if !strings.Contains(output, "No records found for nonexistent.docker., returning NXDOMAIN") {
			t.Errorf("expected NXDOMAIN debug log, got: %s", output)
		}
	})

	t.Run("fallthrough_no_records", func(t *testing.T) {
		buf := enableDebugLog(t)

		d := &Docker{
			Next:      test.ErrorHandler(),
			ttl:       DefaultTTL,
			connected: true,
			zones:     []string{"docker."},
			Fall:      fall.Root,
			records:   map[string][]net.IP{},
			srvs:      map[string][]srvRecord{},
			ptrs:      map[string][]string{},
		}

		m := new(dns.Msg)
		m.SetQuestion("nonexistent.docker.", dns.TypeA)
		w := dnstest.NewRecorder(&test.ResponseWriter{})
		d.ServeDNS(context.Background(), w, m)

		output := buf.String()
		if !strings.Contains(output, "No records found for nonexistent.docker., falling through to next plugin") {
			t.Errorf("expected fallthrough debug log, got: %s", output)
		}
	})

	t.Run("nodata_wrong_type", func(t *testing.T) {
		buf := enableDebugLog(t)

		d := &Docker{
			Next:      test.ErrorHandler(),
			ttl:       DefaultTTL,
			connected: true,
			zones:     []string{"docker."},
			records: map[string][]net.IP{
				"web.docker.": {net.ParseIP("172.17.0.2")},
			},
			srvs: map[string][]srvRecord{},
			ptrs: map[string][]string{},
		}

		m := new(dns.Msg)
		m.SetQuestion("web.docker.", dns.TypeAAAA)
		w := dnstest.NewRecorder(&test.ResponseWriter{})
		d.ServeDNS(context.Background(), w, m)

		output := buf.String()
		if !strings.Contains(output, "NODATA response for web.docker. type AAAA") {
			t.Errorf("expected NODATA debug log, got: %s", output)
		}
	})

	t.Run("answer_count", func(t *testing.T) {
		buf := enableDebugLog(t)

		d := &Docker{
			Next:      test.ErrorHandler(),
			ttl:       DefaultTTL,
			connected: true,
			zones:     []string{"docker."},
			records: map[string][]net.IP{
				"web.docker.": {net.ParseIP("172.17.0.2")},
			},
			srvs: map[string][]srvRecord{},
			ptrs: map[string][]string{},
		}

		m := new(dns.Msg)
		m.SetQuestion("web.docker.", dns.TypeA)
		w := dnstest.NewRecorder(&test.ResponseWriter{})
		d.ServeDNS(context.Background(), w, m)

		output := buf.String()
		if !strings.Contains(output, "Response for web.docker. A: 1 answer(s)") {
			t.Errorf("expected answer count debug log, got: %s", output)
		}
	})

	t.Run("other_type_no_handler", func(t *testing.T) {
		buf := enableDebugLog(t)

		d := &Docker{
			Next:      test.ErrorHandler(),
			ttl:       DefaultTTL,
			connected: true,
			zones:     []string{"docker."},
			records: map[string][]net.IP{
				"web.docker.": {net.ParseIP("172.17.0.2")},
			},
			srvs: map[string][]srvRecord{},
			ptrs: map[string][]string{},
		}

		m := new(dns.Msg)
		m.SetQuestion("web.docker.", dns.TypeMX)
		w := dnstest.NewRecorder(&test.ResponseWriter{})
		d.ServeDNS(context.Background(), w, m)

		output := buf.String()
		if !strings.Contains(output, "No handler for type MX on web.docker., returning NODATA") {
			t.Errorf("expected no handler debug log, got: %s", output)
		}
	})

	t.Run("wildcard_match", func(t *testing.T) {
		buf := enableDebugLog(t)

		d := &Docker{
			Next:      test.ErrorHandler(),
			ttl:       DefaultTTL,
			connected: true,
			zones:     []string{"docker."},
			records: map[string][]net.IP{
				"*.web.docker.": {net.ParseIP("172.17.0.2")},
			},
			srvs: map[string][]srvRecord{},
			ptrs: map[string][]string{},
		}

		m := new(dns.Msg)
		m.SetQuestion("foo.web.docker.", dns.TypeA)
		w := dnstest.NewRecorder(&test.ResponseWriter{})
		d.ServeDNS(context.Background(), w, m)

		output := buf.String()
		if !strings.Contains(output, "Wildcard match for foo.web.docker. via *.web.docker.") {
			t.Errorf("expected wildcard match debug log, got: %s", output)
		}
	})

	t.Run("soa_query", func(t *testing.T) {
		buf := enableDebugLog(t)

		d := &Docker{
			Next:      test.ErrorHandler(),
			ttl:       DefaultTTL,
			connected: true,
			zones:     []string{"docker."},
			records:   map[string][]net.IP{},
			srvs:      map[string][]srvRecord{},
			ptrs:      map[string][]string{},
		}

		m := new(dns.Msg)
		m.SetQuestion("docker.", dns.TypeSOA)
		w := dnstest.NewRecorder(&test.ResponseWriter{})
		d.ServeDNS(context.Background(), w, m)

		output := buf.String()
		if !strings.Contains(output, "SOA query at zone apex for docker.") {
			t.Errorf("expected SOA query debug log, got: %s", output)
		}
	})

	t.Run("ns_query", func(t *testing.T) {
		buf := enableDebugLog(t)

		d := &Docker{
			Next:      test.ErrorHandler(),
			ttl:       DefaultTTL,
			connected: true,
			zones:     []string{"docker."},
			records:   map[string][]net.IP{},
			srvs:      map[string][]srvRecord{},
			ptrs:      map[string][]string{},
		}

		m := new(dns.Msg)
		m.SetQuestion("docker.", dns.TypeNS)
		w := dnstest.NewRecorder(&test.ResponseWriter{})
		d.ServeDNS(context.Background(), w, m)

		output := buf.String()
		if !strings.Contains(output, "NS query at zone apex for docker.") {
			t.Errorf("expected NS query debug log, got: %s", output)
		}
	})

	t.Run("ptr_query", func(t *testing.T) {
		buf := enableDebugLog(t)

		d := &Docker{
			Next:      test.ErrorHandler(),
			ttl:       DefaultTTL,
			connected: true,
			zones:     []string{"docker."},
			records:   map[string][]net.IP{},
			srvs:      map[string][]srvRecord{},
			ptrs: map[string][]string{
				"2.0.17.172.in-addr.arpa.": {"web.docker."},
			},
		}

		m := new(dns.Msg)
		m.SetQuestion("2.0.17.172.in-addr.arpa.", dns.TypePTR)
		w := dnstest.NewRecorder(&test.ResponseWriter{})
		d.ServeDNS(context.Background(), w, m)

		output := buf.String()
		if !strings.Contains(output, "PTR lookup for 2.0.17.172.in-addr.arpa.") {
			t.Errorf("expected PTR lookup debug log, got: %s", output)
		}
	})

	t.Run("ptr_not_found", func(t *testing.T) {
		buf := enableDebugLog(t)

		d := &Docker{
			Next:      test.ErrorHandler(),
			ttl:       DefaultTTL,
			connected: true,
			zones:     []string{"docker."},
			records:   map[string][]net.IP{},
			srvs:      map[string][]srvRecord{},
			ptrs:      map[string][]string{},
		}

		m := new(dns.Msg)
		m.SetQuestion("9.9.9.9.in-addr.arpa.", dns.TypePTR)
		w := dnstest.NewRecorder(&test.ResponseWriter{})
		d.ServeDNS(context.Background(), w, m)

		output := buf.String()
		if !strings.Contains(output, "No PTR records for 9.9.9.9.in-addr.arpa.") {
			t.Errorf("expected no PTR records debug log, got: %s", output)
		}
	})
}

// failingResponseWriter is a dns.ResponseWriter that returns an error from
// WriteMsg. Used to exercise the error branches in ServeDNS that log and
// increment requestFailedCount when the underlying transport rejects a
// response.
type failingResponseWriter struct {
	test.ResponseWriter
}

func (w *failingResponseWriter) WriteMsg(*dns.Msg) error {
	return errWriteMsgFailed
}

var errWriteMsgFailed = errors.New("simulated WriteMsg failure")

func TestServeDNSWriteMsgErrorPaths(t *testing.T) {
	d := &Docker{
		Next:      test.ErrorHandler(),
		ttl:       DefaultTTL,
		connected: true,
		zones:     []string{"docker."},
		records: map[string][]net.IP{
			"web.docker.": {net.ParseIP("172.17.0.2")},
		},
		srvs: map[string][]srvRecord{},
		ptrs: map[string][]string{
			"2.0.17.172.in-addr.arpa.": {"web.docker."},
		},
	}

	cases := []struct {
		name  string
		qname string
		qtype uint16
	}{
		{"ptr", "2.0.17.172.in-addr.arpa.", dns.TypePTR},
		{"soa_apex", "docker.", dns.TypeSOA},
		{"ns_apex", "docker.", dns.TypeNS},
		{"nxdomain", "nonexistent.docker.", dns.TypeA},
		{"nodata_wrong_type", "web.docker.", dns.TypeAAAA},
		{"nodata_no_handler", "web.docker.", dns.TypeMX},
		{"normal_answer", "web.docker.", dns.TypeA},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			beforeFailed := testutil.ToFloat64(requestFailedCount.WithLabelValues(""))

			m := new(dns.Msg)
			m.SetQuestion(tc.qname, tc.qtype)
			w := &failingResponseWriter{}

			if _, err := d.ServeDNS(context.Background(), w, m); err != nil {
				t.Fatalf("expected ServeDNS to return nil error, got %v", err)
			}

			afterFailed := testutil.ToFloat64(requestFailedCount.WithLabelValues(""))
			if afterFailed != beforeFailed+1 {
				t.Errorf("expected failed_requests_total to increment by 1, before=%f after=%f", beforeFailed, afterFailed)
			}
		})
	}
}

func TestUnescapeTxtCharString(t *testing.T) {
	tests := []struct {
		name string
		in   string
		out  string
	}{
		{"no backslash fast path", "plain text", "plain text"},
		{"simple escape", `a\"b`, `a"b`},
		{"decimal escape", `a\032b`, "a b"},
		{"trailing lone backslash", `abc\`, `abc\`},
		{"malformed decimal escape, non-digit after first digit", `a\3xy`, `a\3xy`},
		{"malformed decimal escape, value > 255", `a\999`, `a\999`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := unescapeTxtCharString(tc.in)
			if got != tc.out {
				t.Errorf("unescapeTxtCharString(%q) = %q, want %q", tc.in, got, tc.out)
			}
		})
	}
}

func TestReadyDebugLogging(t *testing.T) {
	t.Run("ready_connected", func(t *testing.T) {
		buf := enableDebugLog(t)

		d := &Docker{
			client:    &client.Client{},
			connected: true,
		}
		d.Ready()

		output := buf.String()
		if !strings.Contains(output, "Ready check: ready (connected to Docker daemon)") {
			t.Errorf("expected ready connected debug log, got: %s", output)
		}
	})

	t.Run("ready_no_client", func(t *testing.T) {
		buf := enableDebugLog(t)

		d := &Docker{}
		d.Ready()

		output := buf.String()
		if !strings.Contains(output, "Ready check: not ready (no Docker client)") {
			t.Errorf("expected not ready no client debug log, got: %s", output)
		}
	})

	t.Run("ready_stale", func(t *testing.T) {
		buf := enableDebugLog(t)

		d := &Docker{
			client:       &client.Client{},
			connected:    false,
			lastSyncTime: time.Now(),
		}
		d.Ready()

		output := buf.String()
		if !strings.Contains(output, "Ready check: ready (serving stale records") {
			t.Errorf("expected ready stale debug log, got: %s", output)
		}
	})

	t.Run("ready_not_synced", func(t *testing.T) {
		buf := enableDebugLog(t)

		d := &Docker{
			client:    &client.Client{},
			connected: false,
		}
		d.Ready()

		output := buf.String()
		if !strings.Contains(output, "Ready check: not ready (disconnected, no previous sync)") {
			t.Errorf("expected not ready disconnected debug log, got: %s", output)
		}
	})
}
