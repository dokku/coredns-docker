package docker

import (
	"context"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
	"github.com/docker/docker/client"
)

// init registers this plugin.
func init() { plugin.Register(pluginName, setup) }

// setup is the function that gets called when the config parser see the token "docker".
func setup(c *caddy.Controller) error {
	d := &Docker{
		labelPrefix: "com.dokku.coredns-docker",
		maxBackoff:  60 * time.Second,
		records:     make(map[string][]net.IP),
		srvs:        make(map[string][]srvRecord),
		ptrs:        make(map[string][]string),
		cnames:      make(map[string]string),
		txts:        make(map[string][][]string),
		ttl:         DefaultTTL,
		zones:       []string{"docker."},
	}
	if err := parse(c, d); err != nil {
		return plugin.Error(pluginName, err)
	}
	log.Debugf("Configuration: zones=[%s], ttl=%d, label_prefix=%q, networks=[%s], max_backoff=%s, host_mode=%t, host_mode_ptr=%t",
		strings.Join(d.zones, ", "), d.ttl, d.labelPrefix, strings.Join(d.networks, ", "), d.maxBackoff, d.hostMode, d.hostModePTR)

	// Create a new Docker client.
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return plugin.Error(pluginName, err)
	}
	d.client = dockerClient

	// Do a ping check to check if the Docker daemon is reachable.
	_, err = d.client.Ping(context.Background())
	if err != nil {
		return plugin.Error(pluginName, err)
	}
	d.connected = true

	ctx, cancel := context.WithCancel(context.Background())
	c.OnStartup(func() error {
		go d.startEventLoop(ctx)
		return nil
	})
	c.OnShutdown(func() error {
		cancel()
		d.client.Close()
		return nil
	})

	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		d.Next = next
		return d
	})

	return nil
}

func parse(c *caddy.Controller, d *Docker) error {
	for c.Next() {
		for c.NextBlock() {
			selector := c.Val()

			switch selector {
			case "label_prefix":
				if !c.NextArg() {
					return c.ArgErr()
				}
				d.labelPrefix = c.Val()
			case "max_backoff":
				if !c.NextArg() {
					return c.ArgErr()
				}
				dur, err := time.ParseDuration(c.Val())
				if err != nil {
					return c.Errf("error parsing max_backoff: %v", err)
				}
				d.maxBackoff = dur
			case "networks":
				d.networks = c.RemainingArgs()
				if len(d.networks) == 0 {
					return c.ArgErr()
				}
			case "ttl":
				if !c.NextArg() {
					return c.ArgErr()
				}
				t, err := strconv.Atoi(c.Val())
				if err != nil {
					return c.Err("error parsing ttl: " + err.Error())
				}
				if t < 0 || t > 3600 {
					return c.Errf("ttl must be in range [0, 3600]: %d", t)
				}
				d.ttl = uint32(t)
			case "fallthrough":
				d.Fall.SetZonesFromArgs(c.RemainingArgs())
			case "host_mode":
				d.hostMode = true
				for _, arg := range c.RemainingArgs() {
					switch arg {
					case "ptr":
						d.hostModePTR = true
					default:
						return c.Errf("unknown host_mode option %q", arg)
					}
				}
			case "zone":
				args := c.RemainingArgs()
				if len(args) == 0 {
					return c.ArgErr()
				}
				d.zones = make([]string, 0, len(args))
				for _, arg := range args {
					z := strings.Trim(arg, ".") + "."
					if z == "." {
						return c.Err("zone cannot be empty")
					}
					d.zones = append(d.zones, z)
				}
			default:
				return c.Errf("unknown property '%s'", selector)
			}
		}
	}

	return nil
}
