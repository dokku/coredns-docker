# Installation

coredns-docker is distributed as a prebuilt CoreDNS binary with the `docker` plugin compiled in. Pick whichever format fits your platform.

## From a release

Download a prebuilt binary from the [GitHub releases page](https://github.com/dokku/coredns-docker/releases). Each release publishes flat binaries named `coredns-docker-<platform>-<arch>` for Linux (`amd64`, `arm`, `arm64`, `mips`, `mips64le`, `ppc64le`, `riscv64`, `s390x`), macOS (`amd64`, `arm64`), and Windows (`amd64`).

```bash
# Linux amd64 as an example; pick the binary that matches your platform
curl -fsSL -o coredns-docker \
    https://github.com/dokku/coredns-docker/releases/latest/download/coredns-docker-linux-amd64
chmod +x coredns-docker
sudo install -m 0755 coredns-docker /usr/local/bin/coredns-docker
```

**Why prefer a release over building from source?** You do not need a Go toolchain, and every release is signed with GitHub build provenance so you can verify it with `gh attestation verify`. Releases are the recommended path for production hosts.

## From a Debian package

Release tags also publish `.deb` packages for `amd64` and `arm64` on [PackageCloud](https://packagecloud.io/dokku/dokku). Add the repository once, then install with `apt`:

```bash
curl -s https://packagecloud.io/install/repositories/dokku/dokku/script.deb.sh | sudo bash
sudo apt install coredns-docker
```

The package installs:

- `/usr/bin/coredns-docker` -- the binary.
- `/etc/coredns/Corefile` -- a default Corefile that binds `:1053` and serves the `docker.` zone (so `web.docker`, `db.docker`, etc., resolve to the matching containers). Marked as a Debian conffile, so your edits survive upgrades.
- `/lib/systemd/system/coredns-docker.service` -- a hardened systemd unit ordered after `docker.service` and before `nginx.service` (so reverse-proxy upstreams that resolve via container DNS are reachable before nginx starts).

A dedicated `coredns-docker` system user is created and added to the `docker` group so it can read `/var/run/docker.sock`. The unit is enabled and started automatically, so by the time `apt` returns the service is already serving DNS on port `1053`.

```bash
systemctl status coredns-docker
dig @127.0.0.1 -p 1053 docker. SOA
```

`apt remove coredns-docker` stops the service and removes the binary and unit, but keeps `/etc/coredns/Corefile` (standard Debian conffile behavior). `apt purge coredns-docker` additionally removes `/etc/coredns/`, the `coredns-docker` user, and the `coredns-docker` group, leaving the host as if the package had never been installed.

**Why a `.deb` instead of a tarball?** `apt` handles upgrades, signature checking, and uninstallation cleanly. On Debian and Ubuntu hosts the deb is usually the least painful way to keep the plugin up to date.

**Want port `53` or a different Corefile?** Edit `/etc/coredns/Corefile` and run `systemctl reload coredns-docker`. If you switch to port `53`, also drop in `AmbientCapabilities=CAP_NET_BIND_SERVICE` via `systemctl edit coredns-docker`. See [linux-systemd.md](linux-systemd.md) for the full unit reference.

## From source

The `build-local` target produces a single binary in the repository root:

```bash
make build-local
```

You end up with `./coredns-docker-local`, which you can run directly or move into your `PATH`. The Makefile does the work of cloning the official CoreDNS source into `.coredns-build/`, registering the `docker` plugin in `plugin.cfg`, and compiling a fresh CoreDNS binary with the plugin baked in. You need only a Go toolchain (version matching `go.mod`) and `git`.

**Why the extra clone step?** CoreDNS plugins are compiled in, not loaded at runtime. Every CoreDNS distribution is a custom binary assembled from a list of plugins in `plugin.cfg`. `build-local` is the minimal way to produce such a binary with only the `docker` plugin added on top of the stock plugin list.

For cross-platform release builds (Linux, macOS, Windows across all supported architectures), use:

```bash
make build
```

This produces the full build matrix under `build/` and `.deb` packages under `build/deb/`. It is what CI runs on tagged releases.

## Docker image

The repository ships a `Dockerfile.hub` that wraps the Linux amd64 binary in a `scratch` image. There is no automatically published image, so you need to build it yourself after running `make build`:

```bash
make build
docker build -t coredns-docker:local -f Dockerfile.hub .
```

Then run CoreDNS in a container, bind-mounting your `Corefile` and the Docker socket:

```bash
docker run --rm -d \
    --name coredns \
    -p 1053:1053/udp -p 1053:1053/tcp \
    -v "$PWD/Corefile:/Corefile:ro" \
    -v /var/run/docker.sock:/var/run/docker.sock:ro \
    coredns-docker:local -conf /Corefile
```

**Why mount the Docker socket?** The plugin uses the Docker API to list containers and subscribe to events. Without the socket inside the container, there is nothing for it to listen to. The mount is `:ro` because the plugin only needs to read container state.

## Verifying the install

Run the binary with `-plugins` to confirm the `docker` plugin is present:

```bash
coredns-docker -plugins | grep docker
```

You should see `dns.docker` in the output alongside the other CoreDNS plugins. If you do not, the binary was built without the plugin -- re-run `make build-local` and try again.
