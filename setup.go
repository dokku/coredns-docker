package docker

import (
	"context"
	"net"
	"strconv"
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
		ttl:         DefaultTTL,
		records:     make(map[string][]net.IP),
		srvs:        make(map[string][]srvRecord),
		domain:      "docker.",
		labelPrefix: "com.dokku.coredns-docker",
		maxBackoff:  60 * time.Second,
	}
	if err := parse(c, d); err != nil {
		return plugin.Error(pluginName, err)
	}

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
		// The first token is "docker", and the rest are domains
		args := c.RemainingArgs()
		if len(args) > 0 {
			d.domain = args[0]
			if d.domain[len(d.domain)-1] != '.' {
				d.domain += "."
			}
		}

		for c.NextBlock() {
			selector := c.Val()

			switch selector {
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
			default:
				return c.Errf("unknown property '%s'", selector)
			}
		}
	}

	return nil
}
