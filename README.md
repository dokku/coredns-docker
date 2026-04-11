# coredns-docker

A CoreDNS plugin that serves DNS records for containers running on the local Docker daemon.

## Installation

Install from a published release on the [GitHub releases page](https://github.com/dokku/coredns-docker/releases):

```bash
# Linux amd64 — pick the binary that matches your platform/arch
curl -fsSL -o coredns-docker \
    https://github.com/dokku/coredns-docker/releases/latest/download/coredns-docker-linux-amd64
chmod +x coredns-docker
sudo mv coredns-docker /usr/local/bin/
```

Or build a single binary from source (requires Go):

```bash
make build-local
```

This produces `./coredns-docker-local` in the repository root. See [docs/installation.md](docs/installation.md) for Debian packages, cross-platform builds, and the Docker image.

## Basic usage

Create a minimal `Corefile` that serves the `docker.` zone on port `1053`:

```text
docker:1053 {
    docker {
        zone docker.
    }
}
```

Start CoreDNS with that Corefile:

```bash
coredns-docker -conf Corefile
```

Start any Docker container and query it by name:

```bash
docker run -d --name web nginx
dig @127.0.0.1 -p 1053 web.docker +short
# → 172.17.0.2
```

The plugin watches Docker events, so starting, stopping, or restarting containers updates the available names with no need to restart CoreDNS.

## Documentation

- [Getting Started](docs/getting-started.md) -- install, first Corefile, first query
- [Installation](docs/installation.md) -- release binaries, Debian packages, source builds, and Docker image
- [DNS Basics](docs/dns-basics.md) -- a short primer on zones, A/AAAA, SRV, TXT, CNAME, PTR records, and TTLs
- [Configuration](docs/configuration.md) -- every Corefile option, stale mode, reverse zones, and the synthetic SOA/NS
- [Docker Labels](docs/docker-labels.md) -- hostname, cname, txt, srv, and wildcard labels
- [Metrics](docs/metrics.md) -- every Prometheus metric the plugin exposes
- [Testing](docs/testing.md) -- unit, integration, and end-to-end test targets
- [Linux with systemd](docs/linux-systemd.md) -- route `docker.` queries via `systemd-resolved` and run CoreDNS as a service
- [macOS with /etc/resolver](docs/macos-resolver.md) -- route `docker.` queries via the macOS resolver
- [Examples](docs/examples/README.md) -- runnable `docker compose` + `Corefile` setups for every feature

## License

[MIT](LICENSE)
