# Examples

Each directory in this folder is a runnable demonstration of a specific feature. Every example contains a `docker-compose.yml` for the demo containers and a `Corefile` for CoreDNS. The CoreDNS binary runs on the host and serves DNS on `127.0.0.1:1053`.

## Running an example

1. Install the plugin once (see [../installation.md](../installation.md)). The commands below assume `coredns-docker` is on your `PATH`. If you built with `make build-local`, substitute `../../../coredns-docker-local` or add the project root to your `PATH`.
2. `cd` into the example directory.
3. In one terminal, start CoreDNS with the example's `Corefile`:

   ```bash
   coredns-docker -conf Corefile
   ```

4. In another terminal, start the demo containers:

   ```bash
   docker compose up -d
   ```

5. Run the `dig` command listed at the top of the example's `docker-compose.yml` to verify.
6. When you are done, stop the containers and CoreDNS:

   ```bash
   docker compose down
   # then Ctrl-C the CoreDNS terminal
   ```

**Why run CoreDNS on the host and services in Compose?** The plugin needs access to `/var/run/docker.sock` to watch containers. Running CoreDNS on the host avoids any container-in-container DNS bootstrapping issues and keeps the examples minimal. If you would rather run CoreDNS in Compose too, build a local image from `Dockerfile.hub` and add a service that mounts the socket (see [../installation.md](../installation.md#docker-image)).

## Example index

| Directory | Demonstrates |
| --- | --- |
| [01-basic](01-basic) | Default `zone docker.`, no labels, resolving a container by its name |
| [02-custom-hostname](02-custom-hostname) | `hostname` label with multiple comma-separated names |
| [03-cname](03-cname) | `cname` label aliasing a container to an external name |
| [04-txt-records](04-txt-records) | Plain `txt` and keyed `txt.KEY` labels with multi-string values |
| [05-srv-records](05-srv-records) | `srv._tcp._http` labels and SRV queries |
| [06-wildcard](06-wildcard) | `wildcard=true` label and `*.web.docker.` matching |
| [07-host-mode](07-host-mode) | `host_mode` directive for CoreDNS running outside Docker |
| [08-network-filtering](08-network-filtering) | `networks` whitelist ignoring containers on other networks |
| [09-multiple-zones](09-multiple-zones) | Serving multiple zones from one Corefile |
| [10-fallthrough](10-fallthrough) | `fallthrough` to the `forward` plugin for upstream resolution |
| [11-name-from-labels](11-name-from-labels) | `name_from_labels` collapsing multiple containers onto a single multi-A name |
| [nginx-integration](nginx-integration) | nginx reverse proxy resolving backends via CoreDNS |
