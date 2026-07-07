#!/usr/bin/env bats

export IMAGE="coredns-docker:bats-test"

# Map `uname -m` to the GOARCH used for the build/linux/coredns-docker-* binaries.
host_goarch() {
  case "$(uname -m)" in
  x86_64 | amd64) echo "amd64" ;;
  aarch64 | arm64) echo "arm64" ;;
  armv7l | armv6l | arm) echo "arm" ;;
  ppc64le) echo "ppc64le" ;;
  s390x) echo "s390x" ;;
  riscv64) echo "riscv64" ;;
  mips64 | mips64le) echo "mips64le" ;;
  mips) echo "mips" ;;
  *) echo "" ;;
  esac
}

setup_file() {
  local goarch binary
  goarch="$(host_goarch)"
  binary="build/linux/coredns-docker-${goarch}"

  if ! command -v docker >/dev/null 2>&1; then
    export SKIP_REASON="docker is not installed"
    return 0
  fi
  if ! docker buildx version >/dev/null 2>&1; then
    export SKIP_REASON="docker buildx is not available"
    return 0
  fi
  if [[ -z "$goarch" ]]; then
    export SKIP_REASON="unsupported host architecture: $(uname -m)"
    return 0
  fi
  if [[ ! -f "$binary" ]]; then
    export SKIP_REASON="missing $binary (run 'make build' first)"
    return 0
  fi

  # Build the image for the host architecture. --load produces a single-arch
  # image in the local docker regardless of whether the active buildx builder
  # is the default `docker` driver or a `docker-container` one.
  docker buildx build --load -t "$IMAGE" -f Dockerfile.hub .
}

teardown_file() {
  docker image rm -f "$IMAGE" >/dev/null 2>&1 || true
}

@test "hub image reports the docker plugin" {
  [[ -z "$SKIP_REASON" ]] || skip "$SKIP_REASON"

  run docker run --rm "$IMAGE" -plugins
  echo "status: $status"
  echo "output: $output"
  [ "$status" -eq 0 ]
  # `-plugins` lists the compiled-in plugin as a bare `docker` line (older
  # CoreDNS printed `dns.docker`); accept either form.
  echo "$output" | grep -Eq '(^|\.)docker$'
}
