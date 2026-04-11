# Nginx Integration Example

This example shows how to run an nginx reverse proxy that discovers backends via CoreDNS, so you never hard-code container IPs and restarted containers keep working without a reload.

## What it does

- `proxy` -- nginx running on port `8080`, configured via `nginx.conf` to resolve backends through CoreDNS (`172.28.0.1:1053`). Routes `/api/` to `api.docker` and everything else to `web.docker`.
- `web` -- a plain nginx container with the label `com.dokku.coredns-docker/hostname=web`, reachable inside the plugin's zone as `web.docker.`.
- `api` -- same idea with the label `com.dokku.coredns-docker/hostname=api`, reachable as `api.docker.`.
- A custom Docker bridge network with a fixed subnet (`172.28.0.0/16`), so the gateway IP is predictable from inside the nginx container.

## Running it

1. Start CoreDNS on the host with the example's `Corefile`:

   ```bash
   coredns-docker -conf Corefile
   ```

2. Start the containers:

   ```bash
   docker compose up -d
   ```

3. Hit the proxy:

   ```bash
   curl -s http://localhost:8080/      # → web.docker
   curl -s http://localhost:8080/api/  # → api.docker
   ```

4. Restart a backend to confirm dynamic resolution:

   ```bash
   docker compose restart web
   curl -s http://localhost:8080/      # still works; nginx picks up the new IP
   ```

## How the pieces fit together

### Why nginx resolves `172.28.0.1:1053`

The `docker-compose.yml` defines a custom bridge network with `subnet: 172.28.0.0/16` and `gateway: 172.28.0.1`. Docker assigns that gateway IP to a bridge interface **on the host**. From inside any container on this network, `172.28.0.1` is the host -- specifically, whichever interface on the host is handling Docker's side of the bridge.

CoreDNS on the host binds to all interfaces by default, so it is reachable at `172.28.0.1:1053` from inside nginx without any extra configuration. The fixed subnet is purely so the gateway IP is known in advance and we can put it in `nginx.conf` as a literal. (Nginx's `resolver` directive requires a literal IP, not a hostname.)

### Why `set $backend "http://...";` matters

```nginx
set $web_backend "http://web.docker";
proxy_pass $web_backend;
```

Nginx treats static `proxy_pass http://web.docker;` as "resolve this name **once** at config load time and reuse the IP forever". That is exactly what you do not want when container IPs change. Nginx only re-resolves names that come through a **variable**, so the indirection via `$web_backend` forces nginx to go back to the resolver on each request (subject to the `valid=10s` cache).

### Why `valid=10s` on `resolver`

`valid` caps how long nginx caches a DNS answer. The plugin's default TTL is 30s, but nginx enforces its own `valid` window regardless. Lower values (5-10s) make container restarts visible quickly at the cost of slightly more DNS traffic. In production, match `valid` to your tolerance for stale answers -- anything from `5s` during development to `60s` for stable backends is reasonable.

### Why `hostname` labels on the backends

The backend containers are named `nginx-integration-web` and `nginx-integration-api` by Docker Compose, so without labels they would be reachable as `nginx-integration-web.docker.` and `nginx-integration-api.docker.`. The `hostname` labels give them shorter aliases (`web.docker.` and `api.docker.`) that match what `nginx.conf` uses. You could achieve the same thing by setting `container_name: web` on each service -- the label approach is just more explicit about the intent.

## Troubleshooting

- **`curl` returns 502 Bad Gateway.** Nginx could not reach the backend. Check `docker logs nginx-integration-proxy` for the upstream error. The two common causes are CoreDNS not running (so nginx cannot resolve `web.docker`) and the backend container being stopped.
- **Nginx logs `host not found in resolver`.** CoreDNS is not reachable at `172.28.0.1:1053`. Confirm the host is actually listening on all interfaces with `ss -tulnp | grep 1053` (Linux) or `lsof -iUDP:1053` (macOS).
- **Changes to `nginx.conf` do not take effect.** The file is mounted read-only into the container. Run `docker compose restart proxy` or `docker exec nginx-integration-proxy nginx -s reload` after editing.
