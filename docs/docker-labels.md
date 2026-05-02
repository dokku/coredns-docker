# Docker Labels

coredns-docker reads Docker labels to customize the records it serves for a container. All labels use the prefix configured via [`label_prefix`](configuration.md#label_prefix), which defaults to `com.dokku.coredns-docker`. Every example below uses the default prefix; substitute your own if you changed it.

## What you get for free

Before you touch any labels, the plugin already creates A/AAAA records for the names Docker knows about each container:

- The container **name** (e.g., `web` from `docker run --name web nginx`).
- Docker **network aliases** (`--network-alias`).
- The **DNS names** Docker itself exposes via the Docker engine.
- The Docker Compose `project.service` pair (e.g., `myproject.web` when Compose stack `myproject` has a service named `web`).
- Any names produced by [`name_from_labels`](configuration.md#name_from_labels) templates configured in the Corefile. The shipped `packaging/Corefile` includes templates for Dokku (`<app>.<process>` and `<app>`) and Compose (`<project>.<service>`), so a `docker run` carrying the matching labels gets those names without any plugin labels of your own.

All of those names become A/AAAA records under your configured zone. PTR records for the reverse lookup are created as well (see [Configuration: reverse zones](configuration.md#reverse-zones-ptr-records)). You only need labels when you want records the default name set does not cover, or when you want SRV/TXT/CNAME/wildcard records.

When the same name comes from more than one container -- for example, three containers all carrying the same Compose `service`, the same `--network-alias`, or the same Dokku `app-name` + `process-type` pair -- the plugin returns all of their IPs in a single multi-A response. Clients see this as standard DNS round-robin.

## `hostname` labels

Attach extra A/AAAA names to a container on top of its default names.

Use this when you want the container to be reachable under a name Docker does not already know about -- for example when the container name is an implementation detail and you want a stable alias that matches your application.

**Label format:**

```text
com.dokku.coredns-docker/hostname=NAME1,NAME2,...
```

Multiple names are comma-separated. Whitespace around each name is trimmed; empty values are ignored.

**Example:**

```yaml
services:
  web:
    image: nginx
    labels:
      - "com.dokku.coredns-docker/hostname=myapp,www"
```

With that label, all three of these resolve to the same container IP:

```bash
dig @127.0.0.1 -p 1053 web.docker +short   # the default container name
dig @127.0.0.1 -p 1053 myapp.docker +short # from the label
dig @127.0.0.1 -p 1053 www.docker +short   # from the label
```

Runnable example: [examples/02-custom-hostname](examples/02-custom-hostname).

## `cname` labels

Alias a container to an external name. Every name the container would otherwise receive (container name, aliases, DNS names, Compose `project.service`, and `hostname` labels) becomes a CNAME pointing at your target instead of an A/AAAA.

Use this when you want applications inside your Docker network to resolve a container name, but actually connect to an external service -- a managed database, a SaaS API, or a legacy box outside Docker.

**Label format:**

```text
com.dokku.coredns-docker/cname=TARGET
```

The target should be a fully qualified name. If you forget the trailing dot, the plugin adds it. The value is lowercased at sync time. An empty `cname` label is ignored, and the container falls back to normal A/AAAA records.

**Example:**

```yaml
services:
  web:
    image: nginx
    labels:
      - "com.dokku.coredns-docker/cname=external.example.com"
```

```bash
dig @127.0.0.1 -p 1053 web.docker CNAME +short
# → external.example.com.
```

**Important:** RFC 1034 forbids a CNAME name from also having A/AAAA/SRV/PTR records. If you set a `cname` label, the plugin suppresses **all** A/AAAA/SRV/PTR/TXT records for that container. `dig A web.docker` still returns the CNAME -- clients follow it to its target automatically. If the `wildcard` label is also set, a wildcard CNAME (`*.web.docker.` → target) is generated too.

Runnable example: [examples/03-cname](examples/03-cname).

## `txt` labels

Attach TXT records to a container. TXT records hold arbitrary text and are the workhorse behind SPF, DKIM, ACME challenges, and ad-hoc metadata.

Use this for local fixtures of SPF/DKIM during mail testing, DNS-01 ACME challenge tokens, or exposing small strings of metadata (build version, git SHA, config URLs) that clients can look up via DNS.

**Label formats:**

```text
com.dokku.coredns-docker/txt=VALUE           # attaches to the container FQDN
com.dokku.coredns-docker/txt.KEY=VALUE       # attaches to KEY.<container>.<zone>.
```

Multiple TXT labels can coexist on the same container; each contributes one TXT resource record. Keys are lowercased when building the FQDN, so `txt.Info` and `txt.info` produce the same record.

**Example:**

```yaml
services:
  web:
    image: nginx
    labels:
      - "com.dokku.coredns-docker/txt=v=spf1 -all"
      - "com.dokku.coredns-docker/txt.info=version=1.0.0"
      - "com.dokku.coredns-docker/txt._acme-challenge=deadbeef"
```

Produces three TXT records:

```bash
dig @127.0.0.1 -p 1053 web.docker TXT +short
# → "v=spf1 -all"

dig @127.0.0.1 -p 1053 info.web.docker TXT +short
# → "version=1.0.0"

dig @127.0.0.1 -p 1053 _acme-challenge.web.docker TXT +short
# → "deadbeef"
```

**Value semantics:**

- Empty values are valid (RFC 1035 allows empty TXT strings).
- Values longer than 255 bytes are automatically split into multiple 255-byte character-strings on the wire. No manual chunking required.
- If the value **starts** with a double quote, the plugin parses it as an RFC 1035 master-file string. This lets a single label produce multi-string TXT records and supports standard escapes (`\"`, `\\`, `\DDD`).
- If the value does **not** start with a double quote, it is stored verbatim -- useful for values containing `=`, `;`, spaces, or other special characters.

**Interaction with other labels:**

- TXT records are generated for every name a container gets A/AAAA records for, including `hostname` label names.
- If `wildcard` is also set, wildcard TXT records (`*.name.zone.` and `KEY.*.name.zone.`) are generated too.
- If `cname` is set, TXT labels are ignored entirely -- the CNAME takes over.

Runnable example: [examples/04-txt-records](examples/04-txt-records).

## `srv` labels

Advertise services a container exposes as SRV records. The plugin looks for labels matching `srv._PROTO._SERVICE=PORT` and generates the corresponding SRV records under each of the container's names.

Use this when clients discover services via SRV (Kubernetes-style service discovery, many message brokers, and any code that does `dig SRV _service._proto.host`). You can advertise multiple services per container by setting multiple labels.

**Label format:**

```text
com.dokku.coredns-docker/srv._PROTO._SERVICE=PORT
```

`PROTO` is typically `tcp` or `udp`. `SERVICE` is the service name (`http`, `https`, `mysql`, `grpc`). `PORT` is the numeric port.

**Example:**

```yaml
services:
  web:
    image: nginx
    labels:
      - "com.dokku.coredns-docker/srv._tcp._http=80"
      - "com.dokku.coredns-docker/srv._tcp._https=443"
```

```bash
dig @127.0.0.1 -p 1053 _http._tcp.web.docker SRV +short
# → 10 10 80 web.docker.

dig @127.0.0.1 -p 1053 _https._tcp.web.docker SRV +short
# → 10 10 443 web.docker.
```

**Fallback when no SRV labels are present:** the plugin derives SRV records from the container's exposed ports (`NetworkSettings.Ports`). Each `PORT/PROTO` binding produces an SRV record like `_PROTO._PROTO.name.zone. → name.zone.:PORT`. This gives every container a usable set of SRV records even without explicit labels.

Runnable example: [examples/05-srv-records](examples/05-srv-records).

## `wildcard` labels

Generate wildcard A/AAAA records (`*.name.zone.`) for every name the container gets. Any single-label subdomain under that name resolves to the same container.

Use this for development environments with multi-tenant apps where each tenant lives under its own subdomain (`tenant1.web.docker`, `tenant2.web.docker`), or for wildcard-routed services where you want one container to answer for any subdomain.

**Label format:**

```text
com.dokku.coredns-docker/wildcard=true
```

Any value other than `true` (or an absent label) leaves wildcards off.

**Example:**

```yaml
services:
  web:
    image: nginx
    labels:
      - "com.dokku.coredns-docker/wildcard=true"
```

With that label, both exact and wildcard records exist:

```bash
dig @127.0.0.1 -p 1053 web.docker +short           # exact match
dig @127.0.0.1 -p 1053 tenant1.web.docker +short   # wildcard match → same IP
dig @127.0.0.1 -p 1053 anything.web.docker +short  # wildcard match → same IP
```

**Matching rules (RFC 4592):**

- A wildcard matches exactly **one** label. `*.web.docker.` matches `foo.web.docker.` but **not** `foo.bar.web.docker.`.
- Exact matches always win over wildcards. If both `web.docker.` and `*.web.docker.` exist, querying `web.docker.` returns the exact answer.
- If `srv` labels are set, wildcard SRV records (`_proto._service.*.name.zone.`) are generated alongside the exact ones.
- If `cname` is set, a wildcard CNAME (`*.name.zone.` → target) is generated alongside the exact CNAME.

Runnable example: [examples/06-wildcard](examples/06-wildcard).
