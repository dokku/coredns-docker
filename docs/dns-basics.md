# DNS Basics

This page is a short primer on the DNS concepts coredns-docker uses. If you have already worked with zone files and record types, you can skip to [Configuration](configuration.md).

## Zones

A **zone** is the slice of the DNS namespace that a particular server is responsible for. `docker.`, `example.com.`, and `internal.company.` are all zones. The trailing dot makes the name absolute -- DNS names are always fully qualified under the hood, even when tools let you omit the final dot.

coredns-docker has a `zone` option in its Corefile because it needs to know which names it owns. When a query arrives for a name that does not end in one of its configured zones, the plugin ignores the query and hands it off to the next plugin in the chain.

```text
docker {
    zone docker.
}
```

In this example, the plugin answers `web.docker.` but not `web.example.com.`. See [Configuration: zone](configuration.md#zone) for multi-zone setups.

## A and AAAA records

An **A record** maps a name to an IPv4 address. An **AAAA record** does the same for IPv6. These are what the world means when it says "DNS lookup" in casual conversation.

```bash
dig @127.0.0.1 -p 1053 web.docker +short
# → 172.17.0.2
```

coredns-docker creates an A record for every Docker container it discovers, using the container's IP on each Docker network it is attached to. If the container has an IPv6 address, it gets an AAAA record too.

## SRV records

An **SRV record** maps a service name to a target hostname **and** port. It is the DNS-native way to say "the HTTP service for `web` lives at `web.docker.` on port `80`". SRV names follow the convention `_service._protocol.name`, so the HTTP-over-TCP record for `web.docker.` is `_http._tcp.web.docker.`.

```bash
dig @127.0.0.1 -p 1053 _http._tcp.web.docker SRV +short
# → 10 10 80 web.docker.
```

The four numbers in an SRV answer are priority, weight, port, and target hostname. coredns-docker derives SRV records from Docker labels (see [docker-labels.md](docker-labels.md#srv-labels)) or, if no labels are set, from the container's exposed ports.

## TXT records

A **TXT record** maps a name to arbitrary text. TXT records are how the rest of the internet publishes SPF policies, DKIM keys, ACME challenge tokens, and various service metadata strings.

```bash
dig @127.0.0.1 -p 1053 web.docker TXT +short
# → "v=spf1 -all"
```

coredns-docker can attach TXT records to a container via labels, including multi-string values and keyed subnames like `_acme-challenge.web.docker.`. See [docker-labels.md](docker-labels.md#txt-labels).

## CNAME records

A **CNAME record** is a canonical-name alias. Looking up a name that has a CNAME returns the CNAME record, and resolvers automatically follow it to the target name.

```bash
dig @127.0.0.1 -p 1053 web.docker +short
# → external.example.com.
# → 203.0.113.10
```

The DNS spec (RFC 1034 §3.6.2) forbids a CNAME name from also having A/AAAA/SRV/PTR records -- a name is either an alias or a concrete destination, never both. coredns-docker respects this: if you add a `cname` label to a container, the plugin stops emitting A/AAAA/SRV/PTR for that container entirely.

## PTR records (reverse DNS)

A **PTR record** goes the other way: given an IP address, return a name. Reverse DNS lookups use two special zones (`in-addr.arpa.` for IPv4 and `ip6.arpa.` for IPv6) whose names are the IP address in reverse. For example, `172.17.0.2` becomes `2.0.17.172.in-addr.arpa.`.

```bash
dig @127.0.0.1 -p 1053 -x 172.17.0.2 +short
# → web.docker.
```

coredns-docker emits a PTR record for every A/AAAA record it creates. To serve PTR queries you must also list `in-addr.arpa` and `ip6.arpa` in your CoreDNS server block, because those reverse zones are outside your `docker.` zone. See [Configuration: reverse zones](configuration.md#reverse-zones-ptr-records).

## TTL

The **TTL** ("time to live") on a DNS answer tells downstream resolvers and clients how long they are allowed to cache it. A TTL of `30` means "it is fine to reuse this answer for 30 seconds before asking again".

Low TTLs make changes propagate faster at the cost of more DNS traffic. coredns-docker defaults to `30` seconds, which is short enough that a container restart is visible almost immediately. During Docker daemon outages the plugin automatically drops the TTL to 5 seconds so clients re-query as soon as the daemon comes back -- see [Configuration: stale mode](configuration.md#stale-mode).

## Wildcard records

A **wildcard record** is a record whose leftmost label is `*`. `*.web.docker.` matches any single label in that position, so `tenant1.web.docker.` and `tenant2.web.docker.` both resolve to the same answer. Per RFC 4592, a wildcard matches exactly **one** label -- `*.web.docker.` does not match `foo.bar.web.docker.`.

coredns-docker can generate wildcard records for a container when you set the `wildcard=true` label. This is handy in development for multi-tenant apps where each tenant is a different subdomain. See [docker-labels.md](docker-labels.md#wildcard-labels).
