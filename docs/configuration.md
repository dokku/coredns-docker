# Configuration

coredns-docker is configured through a CoreDNS **Corefile**. A Corefile is a plain-text file where each top-level block is a server (a zone and port it listens on) and each inner directive is a plugin. The `docker` block inside a server declaration configures this plugin.

A minimal Corefile looks like this:

```text
docker:1053 {
    docker {
        zone docker.
    }
}
```

The outer `docker:1053` says "listen on port `1053` for queries in the `docker.` zone". The inner `docker { ... }` block is this plugin. The rest of this page documents every option you can put inside it.

## Options at a glance

| Option | Argument | Default | Purpose |
|---|---|---|---|
| [`zone`](#zone) | domain names | `docker.` | Zones the plugin answers for |
| [`ttl`](#ttl) | seconds (0-3600) | `30` | TTL on all answers |
| [`label_prefix`](#label_prefix) | string | `com.dokku.coredns-docker` | Namespace for Docker labels the plugin reads |
| [`max_backoff`](#max_backoff) | duration | `60s` | Cap on Docker reconnect backoff |
| [`networks`](#networks) | network names | all | Whitelist of Docker networks to serve |
| [`fallthrough`](#fallthrough) | `[zones...]` | off | Pass unmatched queries to the next plugin |
| [`host_mode`](#host_mode) | `[ptr]` | off | Use host-port bindings instead of container IPs |

Full syntax:

```text
docker {
    zone ZONE [ZONE...]
    ttl SECONDS
    label_prefix PREFIX
    max_backoff DURATION
    networks NETWORK [NETWORK...]
    fallthrough [ZONE...]
    host_mode [ptr]
}
```

## `zone`

Set the DNS zones the plugin is authoritative for. Multiple zones can be listed on one line. Trailing dots are optional; the plugin adds them internally. An empty zone is rejected.

**Why this exists:** CoreDNS delivers queries to a plugin based on the server block's zone declaration, but the plugin itself still needs to know which suffix it owns so it can build the right FQDNs when turning Docker container names into DNS records. If the Corefile server block lists `docker. test.`, but the plugin block only declares `zone docker.`, records for `.test.` are not generated.

```text
docker.:1053 test.:1053 {
    docker {
        zone docker. test.
    }
}
```

With both zones configured, a container named `web` is reachable as `web.docker.` **and** `web.test.`.

See [examples/09-multiple-zones](examples/09-multiple-zones) for a runnable setup.

## `ttl`

Set the time-to-live (in seconds) returned on every answer. Valid range is `0` to `3600` (one hour). The default is `30`.

**Why this exists:** TTL is the knob that balances change propagation against query volume. A low TTL (like the default 30s) means downstream resolvers re-query often, so container restarts are visible almost immediately. A TTL of `0` disables caching entirely, which is useful for tests that create and destroy containers rapidly. Setting TTL higher than `300` is rarely a good idea in a local Docker environment, where container IPs churn.

```text
docker {
    zone docker.
    ttl 5
}
```

Note that during Docker daemon outages the plugin overrides this setting and serves records with a reduced TTL of `5` seconds (or the configured TTL if it is already lower). See [Stale mode](#stale-mode).

## `label_prefix`

Change the namespace the plugin uses when reading Docker labels. The default is `com.dokku.coredns-docker`.

**Why this exists:** Docker labels live in a flat key-value namespace shared with every other tool your containers interact with. The prefix gives the plugin a unique slot so it does not collide with labels used by orchestrators, monitoring, or other CoreDNS setups running in the same environment. Only change this if you have a reason -- most users should leave it alone so documentation examples and community setups keep working.

```text
docker {
    zone docker.
    label_prefix com.example.dns
}
```

With that configuration the plugin reads `com.example.dns/hostname=...` instead of `com.dokku.coredns-docker/hostname=...`.

## `max_backoff`

Cap the exponential backoff the plugin uses when it loses its connection to the Docker daemon and is trying to reconnect. Accepts any Go duration string (`500ms`, `10s`, `2m`). The default is `60s`.

**Why this exists:** When Docker goes away (daemon restart, socket permissions change, `snap` upgrade on Ubuntu), the plugin does not give up -- it reconnects with exponential backoff starting at `1s`. Without a cap, backoff would grow unbounded and short outages could leave you waiting minutes for the plugin to notice Docker is back. `max_backoff` is that cap.

```text
docker {
    zone docker.
    max_backoff 10s
}
```

Lower values recover faster from outages at the cost of slightly more reconnect traffic on a persistently unreachable daemon.

## `networks`

Limit the plugin to containers attached to a whitelist of Docker networks. If unset, all networks are considered.

**Why this exists:** Production hosts frequently run many unrelated Docker networks (`bridge`, an app network, a monitoring network, the swarm overlay, etc.). Without a whitelist, every container on every network would get DNS records, which is usually not what you want -- particularly if containers on different networks reuse the same name. `networks` restricts the plugin to the networks you care about.

```text
docker {
    zone docker.
    networks bridge my-custom-network
}
```

Containers attached only to networks **not** in the list are ignored, even if they have matching labels. Containers attached to at least one whitelisted network are included and resolved via that network's IP.

See [examples/08-network-filtering](examples/08-network-filtering) for a runnable setup.

## `fallthrough`

Instead of returning NXDOMAIN for unmatched names, pass the query to the next plugin in the chain. Without arguments, fallthrough applies to every unmatched name. With arguments, it applies only to the listed zones.

**Why this exists:** The plugin is often stacked with other plugins in the same server block -- `file` for static records, `forward` for upstream DNS, or `hosts` for overrides. If you want those other plugins to get a turn when a name does not match any container, you need `fallthrough`. Without it, the `docker` plugin returns NXDOMAIN and the next plugin never runs for that query.

```text
docker.:1053 {
    docker {
        zone docker.
        fallthrough
    }
    forward . 1.1.1.1
}
```

With that Corefile, `web.docker.` is answered by the `docker` plugin, and anything else (`google.com.`, `example.org.`) is forwarded upstream. See [examples/10-fallthrough](examples/10-fallthrough) for a runnable setup.

## `host_mode`

Resolve container names to the host IP/port bound to the container instead of the container's internal network IP. Optionally also emit PTR records for host IPs when you pass the `ptr` flag.

**Why this exists:** By default, the plugin returns the container's internal Docker network IP (e.g., `172.17.0.2`). Those addresses are only reachable from inside Docker's networks, so they are useless if CoreDNS is running **outside** Docker (a common setup on macOS, or when CoreDNS runs directly on the host). `host_mode` switches to using each container's published host port bindings so names resolve to something reachable from the host.

```text
docker.:1053 {
    docker {
        zone docker.
        host_mode
    }
}
```

In host mode:

- **A/AAAA records** use the host IP from each port binding.
- **SRV records** report the **host** port (e.g., `18080`), not the container port (e.g., `80`).
- Containers with no published ports produce no records at all -- they are not reachable from the host.
- Wildcard bind addresses are normalized: `0.0.0.0` becomes `127.0.0.1`, `::` becomes `::1`. Bindings to specific IPs are used as-is.

PTR records are **off by default** in host mode because many containers usually share a single host IP (especially when they all bind to `127.0.0.1`), which would make reverse lookups return a noisy list. Opt back in with the `ptr` flag:

```text
docker.:1053 in-addr.arpa:1053 ip6.arpa:1053 {
    docker {
        zone docker.
        host_mode ptr
    }
}
```

See [examples/07-host-mode](examples/07-host-mode) for a runnable setup.

## Stale mode

When the Docker daemon becomes unreachable, the plugin does not drop records -- it keeps serving the last known set until the daemon comes back. This is automatic and has no configuration.

**Why this exists:** Docker daemon restarts are routine (package upgrades, `snap refresh`, manual systemctl bounces). Dropping every container name during those few seconds would break services that depend on DNS resolution, especially nginx resolvers that cache NXDOMAIN responses and never retry until their TTL expires. Stale mode keeps the plugin useful during short outages.

While in stale mode:

- Previously synced A, AAAA, SRV, and PTR records continue to be served.
- TTL is forced down to `5` seconds (or the configured `ttl` if it is already lower) so clients re-query aggressively as soon as Docker is back.
- The plugin remains "ready" per the `ready` plugin, as long as it has synced at least once before.
- The `coredns_docker_last_sync_timestamp_seconds` metric stops advancing, which is a good alert signal -- see [metrics.md](metrics.md).

Once the daemon reconnects, the plugin syncs fresh records and normal TTLs resume.

## Ready plugin integration

The plugin implements CoreDNS's `Ready` interface. Combine it with the `ready` plugin to expose a readiness endpoint:

```text
docker.:1053 {
    ready {
        monitor continuously
    }
    docker {
        zone docker.
    }
}
```

The plugin reports ready once it has either successfully connected to the Docker daemon or synced at least one set of records (stale mode still counts as ready). Use this endpoint for Kubernetes readiness probes, load balancer health checks, or supervision scripts.

## Reverse zones (PTR records)

To answer reverse DNS queries (`dig -x 172.17.0.2`), you must declare the `in-addr.arpa` and `ip6.arpa` zones in the **server block**, not the plugin block. The plugin automatically generates PTR records for every A/AAAA record it creates, but CoreDNS will never deliver reverse queries to the plugin unless the server block is listening on those reverse zones.

```text
docker.:1053 in-addr.arpa:1053 ip6.arpa:1053 {
    docker {
        zone docker.
    }
}
```

PTR queries that do not match a known container IP are passed to the next plugin in the chain. A runnable setup is in [examples/01-basic](examples/01-basic).

## Synthetic SOA and NS records

For every configured zone, the plugin generates a synthetic SOA and NS record for the zone apex. You do not need to configure these -- they exist so queries like `dig docker SOA` and `dig docker NS` return sensible answers, and so NXDOMAIN/NODATA responses carry an SOA in the authority section (which DNS caches need in order to cache negative responses per RFC 2308).

The SOA and NS records have these values:

| Field | Value |
|---|---|
| NS target | `ns.dns.<zone>` |
| SOA MNAME | `ns.dns.<zone>` |
| SOA RNAME | `hostmaster.<zone>` |
| SOA Serial | Unix timestamp at startup |
| SOA Refresh | 7200 |
| SOA Retry | 1800 |
| SOA Expire | 86400 |
| SOA Minimum TTL | configured `ttl` |

There is nothing to enable or disable -- the records are always present.
