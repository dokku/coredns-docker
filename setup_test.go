package docker

import (
	"testing"
	"time"

	"github.com/coredns/caddy"
)

func TestParse(t *testing.T) {
	tests := []struct {
		input           string
		shouldErr       bool
		expectedTTL     uint32
		expectedDomain  string
		expectedPrefix  string
		expectedBackoff time.Duration
	}{
		{
			`docker`,
			false,
			DefaultTTL,
			"docker.",
			"com.dokku.coredns-docker",
			60 * time.Second,
		},
		{
			`docker example.org`,
			false,
			DefaultTTL,
			"example.org.",
			"com.dokku.coredns-docker",
			60 * time.Second,
		},
		{
			`docker example.org {
				ttl 60
				label_prefix com.example
				max_backoff 30s
			}`,
			false,
			60,
			"example.org.",
			"com.example",
			30 * time.Second,
		},
		{
			`docker example.org {
				label_prefix ""
			}`,
			false,
			DefaultTTL,
			"example.org.",
			"",
			60 * time.Second,
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
		},
	}

	for i, test := range tests {
		c := caddy.NewTestController("dns", test.input)
		d := &Docker{
			ttl:         DefaultTTL,
			domain:      "docker.",
			labelPrefix: "com.dokku.coredns-docker",
			maxBackoff:  60 * time.Second,
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

		if d.domain != test.expectedDomain {
			t.Errorf("Test %d: expected domain %s, got %s", i, test.expectedDomain, d.domain)
		}

		if d.labelPrefix != test.expectedPrefix {
			t.Errorf("Test %d: expected prefix %s, got %s", i, test.expectedPrefix, d.labelPrefix)
		}

		if d.maxBackoff != test.expectedBackoff {
			t.Errorf("Test %d: expected backoff %v, got %v", i, test.expectedBackoff, d.maxBackoff)
		}
	}
}
