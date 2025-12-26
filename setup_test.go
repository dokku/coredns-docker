package docker

import (
	"testing"
	"time"

	"github.com/coredns/caddy"
)

func TestParse(t *testing.T) {
	tests := []struct {
		input            string
		shouldErr        bool
		expectedTTL      uint32
		expectedZone     string
		expectedPrefix   string
		expectedBackoff  time.Duration
		expectedNetworks []string
	}{
		{
			`docker`,
			false,
			DefaultTTL,
			"docker.",
			"com.dokku.coredns-docker",
			60 * time.Second,
			nil,
		},
		{
			`docker {
				zone example.org
			}`,
			false,
			DefaultTTL,
			"example.org.",
			"com.dokku.coredns-docker",
			60 * time.Second,
			nil,
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
			"example.org.",
			"com.example",
			30 * time.Second,
			[]string{"bridge", "my-custom-network"},
		},
		{
			`docker {
				label_prefix ""
				zone example.org
			}`,
			false,
			DefaultTTL,
			"example.org.",
			"",
			60 * time.Second,
			nil,
		},
		{
			`docker {
				ttl -1
			}`,
			true,
			0,
			"",
			"",
			0,
			nil,
		},
		{
			`docker {
				unknown property
			}`,
			true,
			0,
			"",
			"",
			0,
			nil,
		},
	}

	for i, test := range tests {
		c := caddy.NewTestController("dns", test.input)
		d := &Docker{
			labelPrefix: "com.dokku.coredns-docker",
			maxBackoff:  60 * time.Second,
			ttl:         DefaultTTL,
			zone:        "docker.",
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

		if d.zone != test.expectedZone {
			t.Errorf("Test %d: expected zone %s, got %s", i, test.expectedZone, d.zone)
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
	}
}
