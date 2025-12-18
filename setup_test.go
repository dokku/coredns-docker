package docker

import (
	"testing"

	"github.com/coredns/caddy"
)

func TestParse(t *testing.T) {
	tests := []struct {
		input          string
		shouldErr      bool
		expectedTTL    uint32
		expectedDomain string
	}{
		{
			`docker`,
			false,
			DefaultTTL,
			"docker.",
		},
		{
			`docker example.org`,
			false,
			DefaultTTL,
			"example.org.",
		},
		{
			`docker example.org {
				ttl 60
			}`,
			false,
			60,
			"example.org.",
		},
		{
			`docker {
				ttl -1
			}`,
			true,
			0,
			"",
		},
		{
			`docker {
				unknown property
			}`,
			true,
			0,
			"",
		},
	}

	for i, test := range tests {
		c := caddy.NewTestController("dns", test.input)
		d := &Docker{
			ttl:    DefaultTTL,
			domain: "docker.",
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
	}
}
