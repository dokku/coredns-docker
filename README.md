# docker

## Name

*docker* - DNS interface to Docker containers.

## Description

The docker plugin serves DNS records for containers running on the local Docker daemon. It follows the Docker event stream, picking up changes whenever something happens to a container - whether it gets created, started, deleted, or restarted.

The plugin resolves container names, network aliases, and DNS names to their respective container IP addresses within a specified network.

## Compilation

It will require you to use `go get` or as a dependency on [plugin.cfg](https://github.com/coredns/coredns/blob/master/plugin.cfg).

A simple way to consume this plugin, is by adding the following on [plugin.cfg](https://github.com/coredns/coredns/blob/master/plugin.cfg), and recompile it as [detailed on coredns.io](https://coredns.io/2017/07/25/compile-time-enabling-or-disabling-plugins/#build-with-compile-time-configuration-file).

```text
docker:github.com/dokku/coredns-docker
```

After this you can compile coredns by running:

```bash
make
```

## Syntax

```text
docker [DOMAIN] {
    ttl DURATION
}
```

* `DOMAIN` is the domain for which the plugin will respond. Defaults to `docker.`.

* `ttl` allows you to set a custom TTL for responses. **DURATION** defaults to `30 seconds`. The minimum TTL allowed is `0` seconds, and the maximum is capped at `3600` seconds. Setting TTL to 0 will prevent records from being cached. The unit for the value is seconds.

## Metrics

If monitoring is enabled (via the *prometheus* directive) the following metric is exported:

* `coredns_docker_success_requests_total{server}` - Counter of DNS requests handled successfully.
* `coredns_docker_failed_requests_total{server}` - Counter of DNS requests failed.

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
