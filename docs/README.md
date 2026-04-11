# Documentation

Complete documentation for coredns-docker, a CoreDNS plugin that serves DNS records for Docker containers running on the local Docker daemon.

## Getting Started

- [Getting Started](getting-started.md) -- install, first Corefile, first query, and your first custom label
- [Installation](installation.md) -- release binaries, Debian packages, source builds, and Docker image
- [DNS Basics](dns-basics.md) -- a short primer on zones, A/AAAA, SRV, TXT, CNAME, PTR records, and TTLs

## Reference

- [Configuration](configuration.md) -- every Corefile option, stale mode, reverse zones, and the synthetic SOA/NS
- [Docker Labels](docker-labels.md) -- hostname, cname, txt, srv, and wildcard labels that customize records
- [Metrics](metrics.md) -- every Prometheus metric the plugin exposes

## Guides

- [Testing](testing.md) -- unit, integration, and end-to-end test targets
- [Linux with systemd](linux-systemd.md) -- route `docker.` queries via `systemd-resolved` and run CoreDNS as a service
- [macOS with /etc/resolver](macos-resolver.md) -- route `docker.` queries via the macOS resolver
- [Examples](examples/README.md) -- runnable `docker compose` + `Corefile` setups for every feature, plus an nginx reverse-proxy example
