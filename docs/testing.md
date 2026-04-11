# Testing

The project has three tiers of tests, each with its own Makefile target. Pick the one that matches what you are changing.

## Unit tests

```bash
make test
```

This runs `go test -v ./...` against every Go package in the repository. Unit tests do not talk to Docker or start a real CoreDNS server -- they drive the plugin's functions directly with mock inputs and assert on the records and responses that come back.

**When to use them:** any time you change parsing (`setup.go`), record generation (`docker.go`), or response shaping. They run in a few seconds and do not need Docker, so there is no reason to skip them.

**Example:**

```bash
make test
# ok    github.com/dokku/coredns-docker    1.284s
```

## Integration tests

```bash
make test-integration
```

This runs `go test -v -tags integration -count=1 -timeout 120s ./...`. The `integration` build tag gates a second set of tests that spin up real Docker containers, call the plugin's sync function against the live daemon, and verify the records it produces.

**When to use them:** any change that touches container discovery, network filtering, label parsing in the presence of real Docker payloads, or the event loop. A failing integration test usually means a divergence between what the plugin reads from the Docker API and what the unit test mocks assume.

**Requirements:**

- A running Docker daemon the current user can talk to
- Network access to pull `nginx` and other small images the tests use

**Example:**

```bash
docker info > /dev/null   # sanity check
make test-integration
```

## End-to-end tests

```bash
make test-e2e
```

This first runs `make build-local` to produce `./coredns-docker-local`, then runs `bats e2e.bats`. The BATS suite starts the binary with a test `Corefile`, creates Docker containers labeled for each feature, and uses `dig` to assert on the actual DNS wire responses.

**When to use them:** any change to Corefile parsing, the plugin's interaction with the CoreDNS server block, answer shaping (authority section, flags, NXDOMAIN vs NODATA), or macOS/Linux-only runtime behavior. E2E tests catch regressions that unit and integration tests miss because they exercise the real binary.

**Requirements:**

- A running Docker daemon
- `dig` (from `dnsutils` on Debian/Ubuntu, `bind-utils` on Fedora, or the `bind` Homebrew formula on macOS)
- [bats-core](https://github.com/bats-core/bats-core) 1.5 or newer

**Example:**

```bash
make test-e2e
#  ✓ serves A records for running containers
#  ✓ serves SRV records for labeled ports
#  ...
```

## Release validation

```bash
make validate
```

`validate` runs `lintian` against the built `.deb` packages, inspects their contents with `dpkg-deb` and `dpkg -c`, installs them on the current host, and runs `bats test.bats`. It is the same check CI runs on release branches.

**When to use it:** before tagging a release, to catch packaging regressions (missing dependencies, wrong file permissions, broken `postinst` scripts). It assumes `make build` has already produced `build/deb/*.deb`.

**Requirements:**

- Output of `make build` (the `.deb` files under `build/deb/`)
- `lintian`, `dpkg`, `apt`, and `bats` installed on the host -- in practice this target is usually run inside the project's `Dockerfile` build image via `make validate-in-docker`
