# docker

## Name

*docker* - DNS interface to Docker containers.

## Description

The docker plugin serves DNS records for containers running on the local Docker daemon. It follows the Docker event stream, picking up changes whenever something happens to a container - whether it gets created, started, deleted, or restarted.

The plugin resolves container names, network aliases, DNS names, and SRV records to their respective container IP addresses within a specified network.

### SRV Records via Docker Labels

To create SRV records using Docker labels, add labels to your container in the format:

```
[LABEL_PREFIX]/srv._[PROTOCOL]._[SERVICE]=[PORT]
```

Where:

- `LABEL_PREFIX` is the value of the `label_prefix` option (defaults to `com.dokku.coredns-docker`)
- `PROTOCOL` is the transport protocol (e.g., `tcp`, `udp`)
- `SERVICE` is the service name (e.g., `http`, `https`, `mysql`)
- `PORT` is the port number

**Example Docker Compose configuration:**

```yaml
services:
  web:
    image: nginx
    labels:
      - "com.dokku.coredns-docker/srv._tcp._http=80"
      - "com.dokku.coredns-docker/srv._tcp._https=443"
```

**Example Docker run command:**

```bash
docker run -d \
  --name web \
  --label "com.dokku.coredns-docker/srv._tcp._http=80" \
  nginx
```

This will create SRV records:

- `_http._tcp.web.docker.` → `web.docker.:80`
- `_https._tcp.web.docker.` → `web.docker.:443`

If no labels with the specified prefix are found, the plugin falls back to using the container's exposed ports (`NetworkSettings.Ports`).

- For a port mapping like `80/tcp`, it generates an SRV record for `_tcp._tcp.container-name.zone`.
- For a port mapping without a protocol like `80`, it generates SRV records for both `_tcp._tcp` and `_udp._udp`.

## Compilation

To build coredns with this plugin enabled, run the following command in this repository:

```bash
make build
```

A binary will be created at `bin/coredns`.

## Syntax

```text
docker {
    label_prefix PREFIX
    max_backoff DURATION
    networks NETWORK...
    ttl DURATION
    zone ZONE
}
```

- `zone` is the domain for which the plugin will respond. Defaults to `docker.` and cannot be empty.

- `ttl` allows you to set a custom TTL for responses. **DURATION** defaults to `30 seconds`. The minimum TTL allowed is `0` seconds, and the maximum is capped at `3600` seconds. Setting TTL to 0 will prevent records from being cached. The unit for the value is seconds.

- `label_prefix` allows you to set a custom prefix for SRV record labels. **PREFIX** defaults to `com.dokku.coredns-docker`.

- `max_backoff` allows you to set a maximum backoff duration for the Docker event loop reconnection logic. **DURATION** defaults to `60s`.

- `networks` allows you to specify a list of Docker networks to monitor. If specified, containers not on one of these networks will be ignored.

## Metrics

If monitoring is enabled (via the *prometheus* directive) the following metric is exported:

- `coredns_docker_success_requests_total{server}` - Counter of DNS requests handled successfully.
- `coredns_docker_failed_requests_total{server}` - Counter of DNS requests failed.

The `server` label indicated which server handled the request.

## Ready

This plugin reports readiness to the ready plugin. It will be ready only when it has successfully connected to the Docker daemon.

## Examples

Enable docker with and resolve all containers with `.docker.` as the suffix.

```text
docker:1053 {
    docker docker.
    cache 30
}
```

You can see the [Corefile.example](./Corefile.example) for a full Corefile example.

## Usage Example

### A record

```shell
dig web.docker @127.0.0.1 -p 1053    

; <<>> DiG 9.18.1-1ubuntu1.2-Ubuntu <<>> web.docker @127.0.0.1 -p 1053
;; global options: +cmd
;; Got answer:
;; ->>HEADER<<- opcode: QUERY, status: NOERROR, id: 54986
;; flags: qr aa rd; QUERY: 1, ANSWER: 1, AUTHORITY: 0, ADDITIONAL: 0

;; QUESTION SECTION:
;web.docker.  IN A

;; ANSWER SECTION:
web.docker. 30 IN A 172.17.0.2

;; Query time: 4 msec
;; SERVER: 127.0.0.1#1053(127.0.0.1) (UDP)
```

### SRV record

```shell
dig _http._tcp.web.docker @127.0.0.1 -p 1053 SRV

; <<>> DiG 9.18.1-1ubuntu1.2-Ubuntu <<>> _http._tcp.web.docker @127.0.0.1 -p 1053 SRV
;; global options: +cmd
;; Got answer:
;; ->>HEADER<<- opcode: QUERY, status: NOERROR, id: 49945
;; flags: qr aa rd; QUERY: 1, ANSWER: 1, AUTHORITY: 0, ADDITIONAL: 0

;; QUESTION SECTION:
;_http._tcp.web.docker.  IN SRV

;; ANSWER SECTION:
_http._tcp.web.docker. 30 IN SRV 10 10 80 web.docker.

;; Query time: 0 msec
;; SERVER: 127.0.0.1#1053(127.0.0.1) (UDP)
```

## Integrating with other systems

### macOS Integration

To configure macOS to use CoreDNS for the `docker` domain, create a resolver configuration file:

```bash
sudo mkdir -p /etc/resolver
sudo tee /etc/resolver/docker <<EOF
nameserver 127.0.0.1
port 1053
EOF
```

Replace `127.0.0.1` and `1053` with the IP address and port where CoreDNS is listening.

After creating the resolver file, macOS will automatically use CoreDNS for all queries to the `docker` domain without the need to restart your computer. You can test the configuration with the following commands:

```bash
# Resolve container name
scutil --dns | grep docker

# Query DNS directly
dscacheutil -q host -a name web.docker

# Or use dig
dig web.docker @127.0.0.1 -p 1053
```

### Nginx Integration

Nginx can be configured to use CoreDNS for resolving Docker container names, enabling dynamic reverse proxy configurations without hardcoding IP addresses.

#### Basic Configuration

Configure nginx to use CoreDNS as a resolver by adding a `resolver` directive in your location blocks. Replace `127.0.0.1:1053` with the IP address and port where CoreDNS is listening.

> [!Important]
> Nginx resolves domain names only once at startup when using static hostnames in `proxy_pass`. To enable dynamic resolution that updates when containers restart, you must use variables.

```nginx
http {
    server {
        listen 80;
        server_name example.com;
        
        location / {
            resolver 127.0.0.1:1053 valid=10s;
            set $backend "http://web.docker";
            proxy_pass $backend;
            proxy_set_header Host $host;
            proxy_set_header X-Real-IP $remote_addr;
        }
    }
}
```

The `valid=10s` parameter controls how long nginx caches DNS responses. A shorter value ensures faster updates when containers restart. The variable (`$backend`) is required because nginx only performs DNS lookups for variables, not static hostnames.

#### Multiple Backend Services

You can proxy to multiple Docker containers based on the request path:

```nginx
http {
    server {
        listen 80;
        server_name example.com;
        
        location /api/ {
            resolver 127.0.0.1:1053 valid=10s;
            set $api_backend "http://api.docker";
            proxy_pass $api_backend;
            proxy_set_header Host $host;
        }
        
        location /web/ {
            resolver 127.0.0.1:1053 valid=10s;
            set $web_backend "http://web.docker";
            proxy_pass $web_backend;
            proxy_set_header Host $host;
        }
    }
}
```

#### Using Docker Compose Project/Service Names

If your containers use Docker Compose, you can reference them using the `project.service` format:

```nginx
http {
    server {
        listen 80;
        server_name example.com;
        
        location / {
            resolver 127.0.0.1:1053 valid=10s;
            set $backend "http://myproject.myservice.docker";
            proxy_pass $backend;
            proxy_set_header Host $host;
        }
    }
}
```

#### Load Balancing Multiple Containers

When multiple containers share the same name or alias, CoreDNS returns all IP addresses. Nginx will automatically load balance between them:

```nginx
http {
    upstream backend {
        resolver 127.0.0.1:1053 valid=10s;
        server web.docker resolve;
    }
    
    server {
        listen 80;
        server_name example.com;
        
        location / {
            proxy_pass http://backend;
            proxy_set_header Host $host;
        }
    }
}
```

**Note:** The `resolve` parameter on the `server` directive is required to enable periodic re-resolution of the domain name. Without it, nginx will only resolve the domain once at startup.

**Note:** Ensure that CoreDNS is accessible from the nginx container. If nginx runs in a Docker container, you may need to use the host's IP address (e.g., `host.docker.internal:1053` on Docker Desktop, or the host's bridge IP on Linux).

### Systemd Integration

To configure systemd-resolved to use CoreDNS for the `docker` domain, create or edit `/etc/systemd/resolved.conf.d/docker.conf`:

```ini
[Resolve]
DNS=127.0.0.1:1053
Domains=~docker.
```

Replace `127.0.0.1:1053` with the IP address and port where CoreDNS is listening.

Then restart systemd-resolved:

```bash
sudo systemctl restart systemd-resolved
```

After configuration, you can resolve Docker container names directly:

```bash
# Resolve container name
host web.docker

# Query SRV records
host -t SRV _http._tcp.web.docker
```

**Note:** The `~` prefix in `Domains=~docker.` tells systemd-resolved to route only queries for the `docker` domain to the specified DNS server, while other queries will use the default DNS servers.
