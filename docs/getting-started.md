# Getting Started

This page walks you from nothing to "I can `dig` a running Docker container by name" in a few minutes. If DNS terminology is new to you, skim [DNS Basics](dns-basics.md) first.

## 1. Install

The fastest way to try the plugin is to build a local binary from source. This clones CoreDNS and produces a single executable you can run from the repository root:

```bash
make build-local
```

You end up with `./coredns-docker-local`. If you would rather install from a release or use the Debian package, see [installation.md](installation.md).

**Why a custom binary?** CoreDNS plugins are compiled in -- there is no runtime plugin loader. This is how every CoreDNS plugin works, not just this one. The `build-local` target clones the official CoreDNS source, registers the `docker` plugin in its `plugin.cfg`, and builds a new `coredns` binary with the plugin baked in.

## 2. Write a minimal Corefile

A **Corefile** is how CoreDNS is configured. The outer block is a server declaration (`zone:port`) and the inner blocks are plugins. Create `Corefile` in your working directory:

```text
docker:1053 {
    docker {
        zone docker.
    }
}
```

This says: "Listen on port `1053` for queries in the `docker.` zone. Inside that zone, use the `docker` plugin to answer using records derived from the local Docker daemon."

**Why port 1053?** Port `53` is privileged on most systems and requires `sudo` (or `CAP_NET_BIND_SERVICE`). Port `1053` is unprivileged, so you can run CoreDNS as an ordinary user for development. See [linux-systemd.md](linux-systemd.md) for running on port `53` in production.

## 3. Start CoreDNS

From the same directory, run:

```bash
./coredns-docker-local -conf Corefile
```

You should see a log line like `CoreDNS-1.14.2` followed by `docker.:1053 on [...]`. Leave this terminal running.

**Why does the plugin need Docker access?** coredns-docker talks to the local Docker daemon (by default via `/var/run/docker.sock`) to list containers and subscribe to container lifecycle events. If you are running CoreDNS on a host where the Docker socket is not at `/var/run/docker.sock`, set the `DOCKER_HOST` environment variable before starting CoreDNS -- the plugin uses the standard Docker client environment variables.

## 4. Run a container and query it

In a second terminal, start any container:

```bash
docker run -d --name web nginx
```

Then query the plugin:

```bash
dig @127.0.0.1 -p 1053 web.docker +short
```

You should see the container's IP address (e.g., `172.17.0.2`). The name comes from the container's name (`--name web`); the zone comes from your Corefile (`docker.`); the plugin glued them together when the container started.

**Why the `@127.0.0.1 -p 1053`?** Those flags tell `dig` to send the query directly to CoreDNS on the port you chose. Without them, `dig` would use your system's default resolver, which does not know about the `docker.` zone yet. See [linux-systemd.md](linux-systemd.md) and [macos-resolver.md](macos-resolver.md) for how to make `web.docker` resolvable system-wide without the `@` flag.

## 5. Add a custom name

Docker labels let you attach extra DNS names to a container without changing its actual name. Restart the nginx container with a `hostname` label:

```bash
docker rm -f web
docker run -d --name web --label "com.dokku.coredns-docker/hostname=myapp,www" nginx
```

Now `web.docker`, `myapp.docker`, and `www.docker` all resolve to the same IP:

```bash
dig @127.0.0.1 -p 1053 myapp.docker +short
dig @127.0.0.1 -p 1053 www.docker +short
```

The label uses the default prefix `com.dokku.coredns-docker`. The value after the `=` is a comma-separated list of extra hostnames, and whitespace around each name is trimmed.

**Why labels instead of a config file?** The plugin has no config for "which containers get which names". Everything it serves is derived live from container state, so the **only** way to customize records is via labels the container already carries. That means ephemeral development containers can bring their own DNS without you touching the DNS config.

## Where to next

- [docker-labels.md](docker-labels.md) -- the full set of labels (`hostname`, `cname`, `txt`, `srv`, `wildcard`)
- [configuration.md](configuration.md) -- every Corefile option (custom label prefix, TTL, zones, network filtering, fallthrough, host mode)
- [examples/](examples/README.md) -- runnable `docker compose` setups for every feature
- [linux-systemd.md](linux-systemd.md) or [macos-resolver.md](macos-resolver.md) -- stop typing `@127.0.0.1 -p 1053` on every query
