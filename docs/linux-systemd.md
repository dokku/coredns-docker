# Linux with systemd

This page covers two separate systemd topics:

1. Routing `docker.` lookups through coredns-docker system-wide using `systemd-resolved`, so every command on the box (`curl`, `ping`, `psql`, language clients) can resolve container names without `@127.0.0.1 -p 1053`.
2. Running CoreDNS itself as a managed systemd service.

You can do either independently or both together.

## 1. Route `docker.` queries via systemd-resolved

Modern Ubuntu and Debian releases use `systemd-resolved` as the default DNS resolver. It supports **per-domain routing**, which lets you say "send queries for `docker.` to CoreDNS on port `1053`, and leave everything else alone".

Create a drop-in file at `/etc/systemd/resolved.conf.d/docker.conf`:

```ini
[Resolve]
DNS=127.0.0.1:1053
Domains=~docker.
```

Then reload `systemd-resolved`:

```bash
sudo systemctl restart systemd-resolved
```

**Why the tilde prefix on `Domains=`?** A tilde-prefixed domain is a **routing domain** -- it tells `systemd-resolved` to send queries that match that suffix to the listed DNS servers, without using those servers for anything else. Without the tilde, `resolved` would treat `docker.` as a search domain (appended to bare hostnames during lookup) instead of routing by suffix, which is not what you want.

**Why port `1053`?** CoreDNS is running as an unprivileged process on port `1053` in the quickstart. `systemd-resolved` can talk to any port, not just `53`. If you run CoreDNS on port `53` (see Section 2 below), drop the `:1053` from the `DNS=` line.

Verify the new routing:

```bash
# Inspect the routing configured for this link/global
resolvectl status | grep -A2 "DNS Servers"

# Resolve a container name directly through the system resolver
host web.docker
# or
getent hosts web.docker
```

Both should return the container's IP without specifying a resolver explicitly.

**If it does not work:**

- Check that CoreDNS is actually listening: `ss -ulnp | grep 1053`.
- Check that `systemd-resolved` reloaded the drop-in: `resolvectl status` should list `127.0.0.1:1053` under "DNS Servers" with a `(docker.)` suffix.
- Confirm no other resolver is shadowing `docker.` queries (e.g., `dnsmasq`, `nscd`, or a per-link DNS server from a VPN).

## 2. Run CoreDNS as a systemd service

Deployed hosts usually want CoreDNS running as a managed service that starts on boot, restarts on failure, and logs to the journal.

### If you installed via the Debian package

`apt install coredns-docker` already installs and starts the service for you. The package ships:

- `/lib/systemd/system/coredns-docker.service` -- the unit (same shape as the manual unit below, minus `CAP_NET_BIND_SERVICE` since it binds `:1053`).
- `/etc/coredns/Corefile` -- a default Corefile, marked as a conffile so your edits survive upgrades.
- A `coredns-docker` system user/group, with the user added to the `docker` group.

Check status, follow logs, and reload after editing the Corefile:

```bash
sudo systemctl status coredns-docker
journalctl -u coredns-docker -f
sudo systemctl reload coredns-docker   # SIGUSR1, no dropped queries
```

To customize the unit (for example to add `AmbientCapabilities=CAP_NET_BIND_SERVICE` so you can bind `:53`), use a dropin so your changes are not overwritten on upgrade:

```bash
sudo systemctl edit coredns-docker
```

Skip ahead to [Section 1](#1-route-docker-queries-via-systemd-resolved) if you also want `docker.` lookups routed through the service system-wide.

### If you installed from a release binary or built from source

You need to wire the service up yourself. Install the binary to `/usr/bin/coredns-docker` and your `Corefile` to `/etc/coredns/Corefile`. Create a dedicated system user:

```bash
sudo useradd --system --no-create-home --shell /usr/sbin/nologin coredns
sudo mkdir -p /etc/coredns
sudo chown root:coredns /etc/coredns
sudo chmod 0750 /etc/coredns
# place your Corefile at /etc/coredns/Corefile
```

Add the CoreDNS user to the `docker` group so it can read the Docker socket:

```bash
sudo usermod -a -G docker coredns
```

**Why a dedicated user?** Running long-lived daemons as their own unprivileged user limits the blast radius of a bug or compromise. The `coredns` user only needs to read the Corefile, bind the DNS port, and talk to the Docker socket -- nothing else.

**Why the docker group?** The plugin uses the Docker API socket at `/var/run/docker.sock`, which is owned by `root:docker` on most distributions. Membership in the `docker` group is the standard way to grant non-root access to that socket.

Create `/etc/systemd/system/coredns.service`:

```ini
[Unit]
Description=CoreDNS with docker plugin
Documentation=https://github.com/dokku/coredns-docker
After=network-online.target docker.service
Before=nginx.service
Wants=network-online.target
Requires=docker.service

[Service]
Type=simple
User=coredns
Group=coredns
ExecStart=/usr/bin/coredns-docker -conf /etc/coredns/Corefile
ExecReload=/bin/kill -SIGUSR1 $MAINPID
Restart=on-failure
RestartSec=5s
LimitNOFILE=1048576
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
```

**Why `CAP_NET_BIND_SERVICE`?** This capability lets an unprivileged process bind to ports below 1024. If your Corefile uses port `53` you need it; if you stick with port `1053` you can remove both the `AmbientCapabilities=` and `CapabilityBoundingSet=` lines.

**Why `Requires=docker.service`?** The plugin fails to start if it cannot ping the Docker daemon, so ordering CoreDNS after `docker.service` avoids a spurious startup failure on boot. `Requires` (as opposed to `After`) ensures systemd starts Docker alongside CoreDNS if it is not already running.

**Why `Before=nginx.service`?** If nginx is configured to upstream by container hostname (e.g., `proxy_pass http://web.docker:8080;`), nginx needs `127.0.0.1:1053` answering before it tries to resolve those upstreams during startup, or it will refuse to start. `Before=` is purely ordering: it pulls nothing in, so the line is a no-op on hosts that do not have nginx installed.

Enable and start the service:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now coredns.service
```

Check status and logs:

```bash
sudo systemctl status coredns.service
journalctl -u coredns.service -f
```

A successful start looks like:

```text
coredns.service - CoreDNS with docker plugin
   Loaded: loaded (/etc/systemd/system/coredns.service; enabled; vendor preset: enabled)
   Active: active (running) since ...
 Main PID: 12345 (coredns-docker)
   CGroup: /system.slice/coredns.service
           └─12345 /usr/bin/coredns-docker -conf /etc/coredns/Corefile
```

**Reloading the Corefile without a restart:** `sudo systemctl reload coredns` sends `SIGUSR1`, which CoreDNS handles as a graceful config reload. No dropped queries, no reconnect to the Docker daemon.
