# Metrics

coredns-docker exports Prometheus metrics under the `coredns_docker_*` namespace. Use them to monitor DNS request volume, record counts, and the plugin's connection to the Docker daemon.

## Enabling metrics

Add the standard CoreDNS `prometheus` plugin to any server block that contains `docker`. CoreDNS aggregates metrics from every plugin in that block and serves them on the address you provide.

```text
docker.:1053 in-addr.arpa:1053 ip6.arpa:1053 {
    docker {
        zone docker.
    }
    prometheus :9153
}
```

**Why the separate `prometheus` block?** CoreDNS ships metrics via a dedicated plugin that hosts the `/metrics` HTTP endpoint. Individual plugins (like `docker`) only register metrics; the `prometheus` plugin is what actually exposes them. If you leave `prometheus` out of your Corefile, the metrics still exist internally but there is no way to scrape them.

Scrape the `/metrics` endpoint with any Prometheus-compatible collector:

```bash
curl -s http://127.0.0.1:9153/metrics | grep coredns_docker
```

## Exposed metrics

Every metric is in the `coredns_docker` namespace. Counters monotonically increase, gauges can go up and down, and histograms include `_count`, `_sum`, and `_bucket` series.

| Metric | Type | Labels | Description |
|---|---|---|---|
| `coredns_docker_success_requests_total` | counter | `server` | DNS requests handled successfully |
| `coredns_docker_failed_requests_total` | counter | `server` | DNS requests that failed |
| `coredns_docker_request_duration_seconds` | histogram | `server`, `type` | Latency of DNS requests, bucketed; `type` is the query type (`A`, `AAAA`, `SRV`, `TXT`, `CNAME`, `PTR`, ...) |
| `coredns_docker_stale_requests_total` | counter | `server` | DNS requests served from stale data while the Docker daemon was disconnected |
| `coredns_docker_fallthrough_requests_total` | counter | `server` | DNS requests passed to the next plugin via `fallthrough` |
| `coredns_docker_last_sync_timestamp_seconds` | gauge | -- | Unix timestamp of the last successful record sync from Docker |
| `coredns_docker_records_total` | gauge | -- | Number of A/AAAA record names currently tracked |
| `coredns_docker_srv_records_total` | gauge | -- | Number of SRV record names currently tracked |
| `coredns_docker_ptr_records_total` | gauge | -- | Number of PTR record names currently tracked |
| `coredns_docker_cname_records_total` | gauge | -- | Number of CNAME record names currently tracked |
| `coredns_docker_txt_records_total` | gauge | -- | Number of TXT record names currently tracked |
| `coredns_docker_connected` | gauge | -- | `1` if the plugin is connected to the Docker daemon, `0` otherwise |
| `coredns_docker_containers_total` | gauge | -- | Number of Docker containers currently tracked |
| `coredns_docker_sync_duration_seconds` | histogram | -- | Duration of each record sync from Docker |
| `coredns_docker_sync_errors_total` | counter | -- | Failed record sync attempts |

The `server` label is the CoreDNS server block identifier (e.g., `dns://docker.:1053`). The `type` label on `request_duration_seconds` is the DNS query type from `miekg/dns` (`A`, `AAAA`, `SRV`, etc.).

## Suggested alerts

These are starting points -- tune thresholds to match your environment.

**Docker daemon disconnected for more than 5 minutes:** indicates the plugin is serving stale records. Usually a sign that the Docker socket is unavailable or permissions changed.

```promql
max_over_time(coredns_docker_connected[5m]) == 0
```

**Record sync failing:** surfaces repeated errors contacting Docker during sync. Can precede a disconnect or indicate an API version mismatch.

```promql
rate(coredns_docker_sync_errors_total[5m]) > 0
```

**No sync in the last 5 minutes:** useful as a belt-and-suspenders check in addition to `connected`. If `last_sync_timestamp_seconds` is more than 5 minutes behind wall-clock, the plugin is not receiving Docker events.

```promql
time() - coredns_docker_last_sync_timestamp_seconds > 300
```

**DNS error rate spike:** alerts on a sudden increase in failed responses relative to successful ones.

```promql
rate(coredns_docker_failed_requests_total[5m])
  /
(rate(coredns_docker_success_requests_total[5m]) + rate(coredns_docker_failed_requests_total[5m]))
  > 0.05
```
