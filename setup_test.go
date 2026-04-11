package docker

import (
	"testing"
	"time"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/plugin/pkg/fall"
)

func TestParse(t *testing.T) {
	tests := []struct {
		input               string
		shouldErr           bool
		expectedTTL         uint32
		expectedZones       []string
		expectedPrefix      string
		expectedBackoff     time.Duration
		expectedNetworks    []string
		expectedFall        fall.F
		expectedHostMode    bool
		expectedHostModePTR bool
	}{
		{
			`docker`,
			false,
			DefaultTTL,
			[]string{"docker."},
			"com.dokku.coredns-docker",
			60 * time.Second,
			nil,
			fall.F{},
			false,
			false,
		},
		{
			`docker {
				zone example.org
			}`,
			false,
			DefaultTTL,
			[]string{"example.org."},
			"com.dokku.coredns-docker",
			60 * time.Second,
			nil,
			fall.F{},
			false,
			false,
		},
		{
			`docker {
				zone example.org internal.local
			}`,
			false,
			DefaultTTL,
			[]string{"example.org.", "internal.local."},
			"com.dokku.coredns-docker",
			60 * time.Second,
			nil,
			fall.F{},
			false,
			false,
		},
		{
			`docker {
				label_prefix com.example
				max_backoff 30s
				networks bridge my-custom-network
				ttl 60
				zone example.org
			}`,
			false,
			60,
			[]string{"example.org."},
			"com.example",
			30 * time.Second,
			[]string{"bridge", "my-custom-network"},
			fall.F{},
			false,
			false,
		},
		{
			`docker {
				label_prefix ""
				zone example.org
			}`,
			false,
			DefaultTTL,
			[]string{"example.org."},
			"",
			60 * time.Second,
			nil,
			fall.F{},
			false,
			false,
		},
		{
			`docker {
				fallthrough
			}`,
			false,
			DefaultTTL,
			[]string{"docker."},
			"com.dokku.coredns-docker",
			60 * time.Second,
			nil,
			fall.Root,
			false,
			false,
		},
		{
			`docker {
				fallthrough example.org. test.org.
			}`,
			false,
			DefaultTTL,
			[]string{"docker."},
			"com.dokku.coredns-docker",
			60 * time.Second,
			nil,
			fall.F{Zones: []string{"example.org.", "test.org."}},
			false,
			false,
		},
		{
			`docker {
				host_mode
			}`,
			false,
			DefaultTTL,
			[]string{"docker."},
			"com.dokku.coredns-docker",
			60 * time.Second,
			nil,
			fall.F{},
			true,
			false,
		},
		{
			`docker {
				host_mode ptr
			}`,
			false,
			DefaultTTL,
			[]string{"docker."},
			"com.dokku.coredns-docker",
			60 * time.Second,
			nil,
			fall.F{},
			true,
			true,
		},
		{
			`docker {
				ttl -1
			}`,
			true,
			0,
			nil,
			"",
			0,
			nil,
			fall.F{},
			false,
			false,
		},
		{
			`docker {
				unknown property
			}`,
			true,
			0,
			nil,
			"",
			0,
			nil,
			fall.F{},
			false,
			false,
		},
		{
			`docker {
				zone
			}`,
			true,
			0,
			nil,
			"",
			0,
			nil,
			fall.F{},
			false,
			false,
		},
		{
			`docker {
				host_mode bogus
			}`,
			true,
			0,
			nil,
			"",
			0,
			nil,
			fall.F{},
			false,
			false,
		},
		{
			`docker {
				label_prefix
			}`,
			true,
			0,
			nil,
			"",
			0,
			nil,
			fall.F{},
			false,
			false,
		},
		{
			`docker {
				max_backoff
			}`,
			true,
			0,
			nil,
			"",
			0,
			nil,
			fall.F{},
			false,
			false,
		},
		{
			`docker {
				max_backoff bogus
			}`,
			true,
			0,
			nil,
			"",
			0,
			nil,
			fall.F{},
			false,
			false,
		},
		{
			`docker {
				networks
			}`,
			true,
			0,
			nil,
			"",
			0,
			nil,
			fall.F{},
			false,
			false,
		},
		{
			`docker {
				ttl
			}`,
			true,
			0,
			nil,
			"",
			0,
			nil,
			fall.F{},
			false,
			false,
		},
		{
			`docker {
				ttl notanumber
			}`,
			true,
			0,
			nil,
			"",
			0,
			nil,
			fall.F{},
			false,
			false,
		},
		{
			`docker {
				zone .
			}`,
			true,
			0,
			nil,
			"",
			0,
			nil,
			fall.F{},
			false,
			false,
		},
	}

	for i, test := range tests {
		c := caddy.NewTestController("dns", test.input)
		d := &Docker{
			labelPrefix: "com.dokku.coredns-docker",
			maxBackoff:  60 * time.Second,
			ttl:         DefaultTTL,
			zones:       []string{"docker."},
		}
		err := parse(c, d)

		if test.shouldErr {
			if err == nil {
				t.Errorf("Test %d: expected error but got none", i)
			}
			continue
		}

		if err != nil {
			t.Errorf("Test %d: expected no error but got %v", i, err)
			continue
		}

		if d.ttl != test.expectedTTL {
			t.Errorf("Test %d: expected TTL %d, got %d", i, test.expectedTTL, d.ttl)
		}

		if len(d.zones) != len(test.expectedZones) {
			t.Errorf("Test %d: expected %d zones, got %d", i, len(test.expectedZones), len(d.zones))
		} else {
			for j := range d.zones {
				if d.zones[j] != test.expectedZones[j] {
					t.Errorf("Test %d: expected zone %s at index %d, got %s", i, test.expectedZones[j], j, d.zones[j])
				}
			}
		}

		if d.labelPrefix != test.expectedPrefix {
			t.Errorf("Test %d: expected prefix %s, got %s", i, test.expectedPrefix, d.labelPrefix)
		}

		if d.maxBackoff != test.expectedBackoff {
			t.Errorf("Test %d: expected backoff %v, got %v", i, test.expectedBackoff, d.maxBackoff)
		}

		if len(d.networks) != len(test.expectedNetworks) {
			t.Errorf("Test %d: expected %d networks, got %d", i, len(test.expectedNetworks), len(d.networks))
		} else {
			for j := range d.networks {
				if d.networks[j] != test.expectedNetworks[j] {
					t.Errorf("Test %d: expected network %s at index %d, got %s", i, test.expectedNetworks[j], j, d.networks[j])
				}
			}
		}

		if !d.Fall.Equal(test.expectedFall) {
			t.Errorf("Test %d: expected fall %v, got %v", i, test.expectedFall, d.Fall)
		}

		if d.hostMode != test.expectedHostMode {
			t.Errorf("Test %d: expected hostMode %t, got %t", i, test.expectedHostMode, d.hostMode)
		}

		if d.hostModePTR != test.expectedHostModePTR {
			t.Errorf("Test %d: expected hostModePTR %t, got %t", i, test.expectedHostModePTR, d.hostModePTR)
		}
	}
}
