# macOS with /etc/resolver

macOS has a built-in mechanism for routing DNS queries to a particular resolver based on the domain suffix: drop a file into `/etc/resolver/` and the system picks it up automatically. This page sets up `docker.` so every tool on your Mac (`curl`, `ping`, browsers, language clients) can resolve container names without passing `@127.0.0.1 -p 1053` every time.

This assumes CoreDNS is already running on your Mac (see [getting-started.md](getting-started.md)) and listening on `127.0.0.1:1053`.

## Create the resolver file

Create `/etc/resolver/docker` with a nameserver and port:

```bash
sudo mkdir -p /etc/resolver
sudo tee /etc/resolver/docker <<'EOF'
nameserver 127.0.0.1
port 1053
EOF
```

**Why a file named `docker`?** macOS reads every file in `/etc/resolver/` and treats the filename as the domain it applies to. A file named `docker` routes queries for the `docker.` TLD; a file named `internal.example` routes queries for `internal.example.`. This is documented in the `resolver(5)` manpage.

**Why `nameserver` + `port`?** The resolver config uses BSD-style directives. `nameserver` is the IP of the DNS server and `port` is its port. macOS does not accept `127.0.0.1:1053` in a single line the way `systemd-resolved` does -- it needs the two keys on their own lines.

No restart is needed. macOS reads `/etc/resolver/` on every lookup, so the change takes effect immediately.

## Verify

Three commands confirm the routing is set up correctly.

**1. Check that macOS picked up the file:**

```bash
scutil --dns | grep -A3 docker
```

You should see an entry like:

```text
resolver #9
  domain   : docker
  nameserver[0] : 127.0.0.1
  port     : 1053
```

**2. Resolve through the macOS directory service:**

```bash
dscacheutil -q host -a name web.docker
```

This bypasses `dig` and asks macOS's own resolver the way everyday applications do. A successful lookup prints the name, IP, and the alias chain.

**3. Sanity-check with `dig`:**

```bash
dig web.docker @127.0.0.1 -p 1053 +short
```

If this returns an IP but `dscacheutil` does not, the `/etc/resolver/docker` file was not picked up. Try `sudo killall -HUP mDNSResponder` to nudge the resolver.

## Multi-zone setups

macOS's `/etc/resolver/` mechanism routes **one TLD per file**. If your Corefile serves several zones, create one resolver file per zone:

```bash
sudo tee /etc/resolver/docker <<'EOF'
nameserver 127.0.0.1
port 1053
EOF

sudo tee /etc/resolver/internal <<'EOF'
nameserver 127.0.0.1
port 1053
EOF
```

**Why one file per zone?** macOS only looks at the filename, so a single file cannot own multiple unrelated suffixes. This is different from `systemd-resolved`, which can list several `Domains=` entries in one drop-in. It is not a limitation of coredns-docker.

## Common gotchas

- **CoreDNS not running.** macOS routes queries based on the file, not the liveness of the server. If CoreDNS is down, resolver lookups hang until they time out. Use `lsof -iUDP:1053` or `ss -ulnp | grep 1053` on Linux to confirm the server is listening.
- **VPN resolvers shadowing your config.** Corporate VPNs often install their own resolver files with higher priority. `scutil --dns` will show you every resolver macOS considers; if a VPN resolver claims the `docker.` suffix, remove or edit its file.
- **Browsers with their own resolvers.** Chrome and Firefox support DNS-over-HTTPS with their own resolver, bypassing macOS entirely. If browser lookups for `web.docker` fail while `dscacheutil` succeeds, disable Secure DNS in the browser's settings for testing.
