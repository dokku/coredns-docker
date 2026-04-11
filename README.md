# docker

## Name

*docker* - DNS interface to Docker containers.

## Description

The docker plugin serves DNS records for containers running on the local Docker daemon. It follows the Docker event stream, picking up changes whenever something happens to a container - whether it gets created, started, deleted, or restarted.

The plugin resolves container names, network aliases, DNS names, SRV records, and PTR (reverse DNS) records to their respective container IP addresses within a specified network.

### SRV Records via Docker Labels

To create SRV records using Docker labels, add labels to your container in the format:

```text
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

### Custom Hostname via Docker Labels

To add custom DNS hostnames to a container, add a label in the format:

```text
[LABEL_PREFIX]/hostname=[HOSTNAME1],[HOSTNAME2],...
```

Where:

- `LABEL_PREFIX` is the value of the `label_prefix` option (defaults to `com.dokku.coredns-docker`)
- `HOSTNAME1`, `HOSTNAME2`, etc. are the custom hostnames to register

The custom hostnames are registered **in addition to** the container's existing names (container name, network aliases, DNS names, and Docker Compose project.service names).

Multiple hostnames can be specified as a comma-separated list. Whitespace around each hostname is trimmed. Empty values are ignored.

**Example Docker Compose configuration:**

```yaml
services:
  web:
    image: nginx
    labels:
      - "com.dokku.coredns-docker/hostname=myapp,www"
```

**Example Docker run command:**

```bash
docker run -d \
  --name web \
  --label "com.dokku.coredns-docker/hostname=myapp,www" \
  nginx
```

This will create A records for both custom hostnames in addition to the container name:

- `web.docker.` -> container IP
- `myapp.docker.` -> container IP
- `www.docker.` -> container IP

If SRV labels are also configured, SRV records are generated for all names including custom hostnames.

### Wildcard Records

To enable wildcard DNS resolution for a container, add a label in the format:

```text
[LABEL_PREFIX]/wildcard=true
```

Where `LABEL_PREFIX` is the value of the `label_prefix` option (defaults to `com.dokku.coredns-docker`).

When enabled, the plugin generates wildcard DNS entries (`*.name.zone.`) for all of the container's names (container name, network aliases, DNS names, Docker Compose project.service names, and custom hostnames). This allows any single-label subdomain to resolve to the container's IP address.

**Example Docker Compose configuration:**

```yaml
services:
  web:
    image: nginx
    labels:
      - "com.dokku.coredns-docker/wildcard=true"
```

**Example Docker run command:**

```bash
docker run -d \
  --name web \
  --label "com.dokku.coredns-docker/wildcard=true" \
  nginx
```

This will create both exact and wildcard A records:

- `web.docker.` -> container IP (exact match)
- `*.web.docker.` -> container IP (wildcard match)

Any single-label subdomain will resolve:

- `tenant1.web.docker.` -> container IP
- `tenant2.web.docker.` -> container IP
- `anything.web.docker.` -> container IP

**Matching behavior:**

- Wildcards match exactly one label per [RFC 4592](https://www.rfc-editor.org/rfc/rfc4592). `*.web.docker.` matches `foo.web.docker.` but does **not** match `foo.bar.web.docker.`.
- Exact matches always take precedence over wildcard matches. If both `web.docker.` and `*.web.docker.` exist, a query for `web.docker.` returns the exact match.
- If SRV labels are configured, wildcard SRV records are also generated (e.g., `_http._tcp.*.web.docker.`).

**Use case:** Development environments where applications serve multiple subdomains, such as multi-tenant apps (`tenant1.myapp.docker`, `tenant2.myapp.docker`) or wildcard routing configurations.

### SOA Records

The plugin generates a synthetic SOA record for each configured zone. SOA records serve two purposes:

1. **Zone apex queries**: Querying for the SOA record of a configured zone (e.g., `dig docker SOA`) returns the synthetic SOA as the answer.
2. **Negative responses**: NXDOMAIN (name does not exist) and NODATA (name exists but not for the requested type) responses include the SOA record in the authority section. This is required by [RFC 2308](https://www.rfc-editor.org/rfc/rfc2308) and allows DNS caches to properly cache negative responses.

The SOA record uses the following values:

| Field | Value |
|---|---|
| MNAME (primary nameserver) | `ns.dns.<zone>` |
| RNAME (hostmaster) | `hostmaster.<zone>` |
| Serial | Current Unix timestamp |
| Refresh | 7200 |
| Retry | 1800 |
| Expire | 86400 |
| Minimum TTL | Same as configured TTL |

### NS Records

The plugin generates a synthetic NS record for each configured zone apex. When queried for NS records at the zone apex (e.g., `dig docker NS`), the plugin returns a single NS record pointing to `ns.dns.<zone>`, consistent with the MNAME field in the SOA record.

NS queries for names other than the zone apex (e.g., `dig web.docker NS`) are not handled by the NS record generator and will return a NODATA response if the name exists.

### Reverse DNS (PTR Records)

The plugin automatically generates PTR (reverse DNS) records for all container IP addresses. This allows reverse lookups from IP addresses back to container FQDNs, which is useful for log analysis, connection identification, and authentication schemes.

**How it works:**

- For every A/AAAA record generated, a corresponding PTR record is created mapping the reverse ARPA name to the container's FQDN.
- IPv4 addresses use the `in-addr.arpa.` zone (e.g., `172.17.0.2` becomes `2.0.17.172.in-addr.arpa.`).
- IPv6 addresses use the `ip6.arpa.` zone in nibble format (e.g., `2001:db8::1` becomes `1.0.0.0...8.b.d.0.1.0.0.2.ip6.arpa.`).
- If multiple containers share the same IP, or a container has multiple names, all FQDNs are returned in the PTR response.
- Wildcard names (e.g., `*.web.docker.`) are excluded from PTR records.

**Server block configuration:**

Since PTR queries use `in-addr.arpa.` and `ip6.arpa.` zones (which are outside your configured zones), you must add these reverse zones to your CoreDNS server block declaration:

```text
docker.localhost:1053 in-addr.arpa:1053 ip6.arpa:1053 {
    docker {
        zone docker.localhost
    }
}
```

When a PTR query does not match any known container IP, it is passed to the next plugin in the chain.

**Example:**

```bash
dig -x 172.17.0.2 @127.0.0.1 -p 1053
```

### Host Mode

By default, the plugin resolves container names to the container's internal network IP address (e.g. `172.17.0.2`). These IPs are only reachable from inside the Docker network, which is a problem when CoreDNS is running **outside** Docker (a common setup on macOS or when CoreDNS runs directly on the host in a development environment).

Enabling `host_mode` switches the plugin to use each container's **host port bindings** (`-p` / `--publish`) as the source of A/AAAA and SRV records. The container's internal IP is no longer used.

**Enable it in your Corefile:**

```text
docker.localhost:1053 {
    docker {
        zone docker.localhost
        host_mode
    }
}
```

**What changes when `host_mode` is enabled:**

- **A/AAAA records** use the host IP from each port binding instead of the container's internal network IP.
- **SRV records** report the **host port** (e.g. `8080`), not the container port (e.g. `80`).
- If an SRV label specifies a container port that has no matching host binding, that label is skipped (other labels still apply; there is no silent fallback to a different service name).
- If a container has **no** SRV labels, the plugin falls back to generating SRV records from the container's bound ports — one SRV per binding's host port.
- Containers with **no** host port bindings produce **no records** in host mode, because they are not reachable from the host.
- Names (container name, network aliases, DNS names, Docker Compose `project.service`, and `hostname` labels) are unioned across all selected networks, because in host mode all records point at the same host.

**Wildcard host IP normalization:**

Docker often binds to wildcard addresses (`0.0.0.0` or `::`) to accept connections on every interface. These are not usable as record values, so the plugin normalizes them to the corresponding loopback address:

- `0.0.0.0` (or empty) → `127.0.0.1`
- `::` → `::1`

Bindings to a specific host IP (e.g. `192.168.1.10`) are used as-is.

**PTR records in host mode:**

PTR (reverse DNS) records are **off by default** in host mode. Multiple containers often share a host IP (especially when bound to loopback), which would make reverse lookups return a noisy list of every container bound to that address.

To opt back in, add the `ptr` argument:

```text
docker.localhost:1053 in-addr.arpa:1053 ip6.arpa:1053 {
    docker {
        zone docker.localhost
        host_mode ptr
    }
}
```

With `host_mode ptr` enabled, a reverse lookup for a host IP will return the FQDNs of every container bound to it.

**Example:**

Start a container with a published port:

```bash
docker run -d --name web -p 127.0.0.1:18080:80 nginx
```

With `host_mode` enabled, resolution becomes:

```bash
dig web.docker.localhost @127.0.0.1 -p 1053
# → 127.0.0.1

dig _tcp._tcp.web.docker.localhost @127.0.0.1 -p 1053 SRV
# → 10 10 18080 web.docker.localhost.
```

## Compilation

To build coredns with this plugin enabled, run the following command in this repository:

```bash
make build
```

A binary will be created at `bin/coredns`.

## Syntax

```text
docker {
    fallthrough [ZONES...]
    host_mode [ptr]
    label_prefix PREFIX
    max_backoff DURATION
    networks NETWORK...
    ttl DURATION
    zone ZONE [ZONE...]
}
```

- `fallthrough` **[ZONES...]** - If a query matches the plugin's zone but no record is found, pass the query to the next plugin instead of returning NXDOMAIN. If **ZONES** are specified, only queries for names within those zones will fall through. If no zones are given, all unmatched queries fall through. By default, the plugin returns NXDOMAIN for unknown names. Use this when composing with other plugins that serve the same zone (e.g., `file` as a fallback for static records).

- `host_mode` **[ptr]** - Resolve container names to their host-bound IP addresses and host ports instead of the container's internal network IP. See [Host Mode](#host-mode) for details. Pass the optional `ptr` argument to also emit PTR records for host IPs (off by default).

- `zone` is the domain (or domains) for which the plugin will respond. Multiple zones can be specified separated by spaces. Defaults to `docker.` and cannot be empty.

- `ttl` allows you to set a custom TTL for responses. **DURATION** defaults to `30 seconds`. The minimum TTL allowed is `0` seconds, and the maximum is capped at `3600` seconds. Setting TTL to 0 will prevent records from being cached. The unit for the value is seconds.

- `label_prefix` allows you to set a custom prefix for SRV record labels. **PREFIX** defaults to `com.dokku.coredns-docker`.

- `max_backoff` allows you to set a maximum backoff duration for the Docker event loop reconnection logic. **DURATION** defaults to `60s`.

- `networks` allows you to specify a list of Docker networks to monitor. If specified, containers not on one of these networks will be ignored.

## Metrics

If monitoring is enabled (via the *prometheus* directive) the following metrics are exported:

- `coredns_docker_success_requests_total{server}` - Counter of DNS requests handled successfully.
- `coredns_docker_failed_requests_total{server}` - Counter of DNS requests failed.
- `coredns_docker_request_duration_seconds{server, type}` - Histogram of DNS request durations in seconds. The `type` label indicates the query type (e.g., `A`, `AAAA`, `SRV`).
- `coredns_docker_stale_requests_total{server}` - Counter of DNS requests served from stale data during Docker daemon disconnect.
- `coredns_docker_fallthrough_requests_total{server}` - Counter of DNS requests passed to the next plugin via fallthrough.
- `coredns_docker_last_sync_timestamp_seconds` - Unix timestamp of the last successful record sync from Docker. This can be used to monitor how fresh the plugin's data is.
- `coredns_docker_records_total` - Number of A/AAAA DNS record names currently tracked.
- `coredns_docker_srv_records_total` - Number of SRV DNS record names currently tracked.
- `coredns_docker_ptr_records_total` - Number of PTR DNS record names currently tracked.
- `coredns_docker_connected` - Whether the plugin is connected to the Docker daemon (1 = connected, 0 = disconnected).
- `coredns_docker_containers_total` - Number of Docker containers currently tracked.
- `coredns_docker_sync_duration_seconds` - Histogram of record sync durations in seconds.
- `coredns_docker_sync_errors_total` - Counter of failed record sync attempts.

The `server` label indicates which server handled the request. The `type` label indicates the DNS query type.

## Ready

This plugin reports readiness to the ready plugin. It will be ready when it has successfully connected to the Docker daemon, or when it has previously synced records (stale mode). During Docker daemon downtime, the plugin continues serving the last known records with a reduced TTL of 5 seconds to encourage clients to re-query frequently.

## Stale Records

When the Docker daemon becomes unreachable, the plugin continues serving the last known DNS records rather than failing all queries. This ensures that existing container names remain resolvable during brief Docker daemon restarts or outages.

During stale mode:

- All previously synced A, AAAA, SRV, and PTR records continue to be served.
- The TTL on stale responses is reduced to 5 seconds (or the configured TTL if it is already 5 seconds or lower). This encourages DNS clients to re-query frequently, so they pick up fresh records as soon as the daemon reconnects.
- The `coredns_docker_last_sync_timestamp_seconds` metric can be used to monitor how long the plugin has been operating on stale data.
- The plugin remains "ready" as long as it has previously synced at least once, even if the daemon is currently disconnected.

Once the Docker daemon becomes reachable again, the plugin automatically reconnects (using exponential backoff), re-syncs all container records, and resumes normal TTL values.

## Debug Logging

To enable debug logging for the docker plugin, add the `debug` directive to your Corefile:

```text
docker:1053 in-addr.arpa:1053 ip6.arpa:1053 {
    debug
    docker {
        zone docker.localhost
    }
}
```

When debug logging is enabled, the plugin logs messages at key decision points to help trace DNS query resolution and plugin lifecycle events.

### Query Resolution

| Log Message | Meaning |
|---|---|
| `Query: qname=<name> qtype=<type>` | A DNS query was received with the given name and type. |
| `Query <name> not in zones [<zones>], passing to next plugin` | The query name does not match any configured zone, so the query is forwarded to the next plugin in the chain. |
| `Lookup results for <name>: A/AAAA records=<n>, SRV records=<n>, connected=<bool>` | Shows the number of matching records found in the internal cache and the Docker connection status. |
| `Wildcard match for <name> via <wildcard>` | No exact match was found, but a wildcard record matched. The query name's leftmost label was replaced with `*` to find the match. |
| `PTR lookup for <name>: <n> record(s), connected=<bool>` | A PTR (reverse DNS) query matched a known container IP. Shows the number of FQDNs and connection status. |
| `No PTR records for <name>, passing to next plugin` | A PTR query did not match any known container IP, so the query is forwarded to the next plugin. |
| `No records found for <name>, falling through to next plugin` | No records exist for the name and `fallthrough` is configured, so the query is forwarded to the next plugin. |
| `SOA query at zone apex for <zone>` | A SOA query was received for the zone apex, and the synthetic SOA record is returned as the answer. |
| `NS query at zone apex for <zone>` | An NS query was received for the zone apex, and the synthetic NS record is returned as the answer. |
| `No records found for <name>, returning NXDOMAIN` | No records exist for the name and `fallthrough` is not configured, so an NXDOMAIN response is returned. |
| `Response for <name> <type>: <n> answer(s)` | The number of DNS answer records included in the response. |
| `NODATA response for <name> type <type>: name exists but no matching records` | The name exists in the record cache but has no records matching the requested type (e.g., AAAA query for an IPv4-only container). |
| `No handler for type <type> on <name>, falling through to next plugin` | The query type is not A, AAAA, or SRV, and `fallthrough` is configured. |
| `No handler for type <type> on <name>, returning NODATA` | The query type is not A, AAAA, or SRV, and `fallthrough` is not configured. |

### Plugin Lifecycle

| Log Message | Meaning |
|---|---|
| `Configuration: zones=[<zones>], ttl=<n>, label_prefix=<prefix>, networks=[<networks>], max_backoff=<duration>` | Logs the parsed plugin configuration at startup. |
| `Connected to Docker daemon` | The plugin successfully connected (or reconnected) to the Docker daemon. |
| `Found <n> running containers` | The number of containers discovered during a record sync. |
| `Synced <n> records, <n> SRV records, and <n> PTR records` | The number of DNS records generated after a sync. |

### Readiness Checks

| Log Message | Meaning |
|---|---|
| `Ready check: ready (connected to Docker daemon)` | The plugin is ready because it has an active Docker connection. |
| `Ready check: ready (serving stale records, last sync: <timestamp>)` | The plugin is ready because it has previously synced records, even though the Docker daemon is currently disconnected. |
| `Ready check: not ready (no Docker client)` | The plugin is not ready because no Docker client has been initialized. |
| `Ready check: not ready (disconnected, no previous sync)` | The plugin is not ready because it is disconnected and has never successfully synced records. |

Debug logging has minimal performance overhead when disabled. When the `debug` directive is not present, all `Debugf` calls return immediately without formatting the log message.

## Examples

Enable docker with and resolve all containers with `.docker.localhost` as the suffix. The `in-addr.arpa` and `ip6.arpa` zones enable reverse DNS (PTR) lookups.

```text
docker:1053 in-addr.arpa:1053 ip6.arpa:1053 {
    docker {
        zone docker.localhost
    }
    cache 30
}
```

Enable docker with multiple zones. Containers will be resolvable under both `.docker.localhost` and `.internal.localhost`. Note that all zones must also be listed in the server block declaration.

```text
docker.localhost:1053 internal.localhost:1053 in-addr.arpa:1053 ip6.arpa:1053 {
    docker {
        zone docker.localhost internal.localhost
    }
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

### SOA record

```shell
dig docker @127.0.0.1 -p 1053 SOA

; <<>> DiG 9.18.1-1ubuntu1.2-Ubuntu <<>> docker @127.0.0.1 -p 1053 SOA
;; global options: +cmd
;; Got answer:
;; ->>HEADER<<- opcode: QUERY, status: NOERROR, id: 12345
;; flags: qr aa rd; QUERY: 1, ANSWER: 1, AUTHORITY: 0, ADDITIONAL: 0

;; QUESTION SECTION:
;docker.  IN SOA

;; ANSWER SECTION:
docker. 30 IN SOA ns.dns.docker. hostmaster.docker. 1234567890 7200 1800 86400 30

;; Query time: 0 msec
;; SERVER: 127.0.0.1#1053(127.0.0.1) (UDP)
```

### NS record

```shell
dig docker @127.0.0.1 -p 1053 NS

; <<>> DiG 9.18.1-1ubuntu1.2-Ubuntu <<>> docker @127.0.0.1 -p 1053 NS
;; global options: +cmd
;; Got answer:
;; ->>HEADER<<- opcode: QUERY, status: NOERROR, id: 12345
;; flags: qr aa rd; QUERY: 1, ANSWER: 1, AUTHORITY: 0, ADDITIONAL: 0

;; QUESTION SECTION:
;docker.  IN NS

;; ANSWER SECTION:
docker. 30 IN NS ns.dns.docker.

;; Query time: 0 msec
;; SERVER: 127.0.0.1#1053(127.0.0.1) (UDP)
```

### PTR record (reverse DNS)

```shell
dig -x 172.17.0.2 @127.0.0.1 -p 1053

; <<>> DiG 9.18.1-1ubuntu1.2-Ubuntu <<>> -x 172.17.0.2 @127.0.0.1 -p 1053
;; global options: +cmd
;; Got answer:
;; ->>HEADER<<- opcode: QUERY, status: NOERROR, id: 54321
;; flags: qr aa rd; QUERY: 1, ANSWER: 1, AUTHORITY: 0, ADDITIONAL: 0

;; QUESTION SECTION:
;2.0.17.172.in-addr.arpa.  IN PTR

;; ANSWER SECTION:
2.0.17.172.in-addr.arpa. 30 IN PTR web.docker.

;; Query time: 0 msec
;; SERVER: 127.0.0.1#1053(127.0.0.1) (UDP)
```

## Testing

### Unit Tests

Run the Go unit tests:

```bash
make test
```

### Integration Tests

The integration tests exercise the plugin against a real Docker daemon. They create containers, call `syncRecords`, and verify DNS resolution through `ServeDNS`.

**Requirements:**

- Docker daemon running

```bash
make test-integration
```

### End-to-End Tests

The e2e tests build a CoreDNS binary with the docker plugin, start it on port 15353, create Docker containers, and verify DNS resolution with `dig`.

**Requirements:**

- Docker daemon running
- `dig` (from `dnsutils` / `bind-utils`)
- `bats` ([bats-core](https://github.com/bats-core/bats-core))

```bash
make test-e2e
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
