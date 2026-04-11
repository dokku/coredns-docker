#!/usr/bin/env bats

export COREDNS_PORT=15353
export COREDNS_ZONE="docker.localhost"
export TEST_NETWORK="coredns-e2e-test"
export COREDNS_BINARY="./coredns-docker-local"
export COREDNS_PID_FILE=""
export COREFILE=""

setup_file() {
  # Safety cleanup from previous runs
  docker ps -aq --filter "name=coredns-e2e-" | xargs -r docker rm -f 2>/dev/null || true
  docker network rm "$TEST_NETWORK" 2>/dev/null || true

  # Create test network
  docker network create "$TEST_NETWORK"

  # Write test Corefile
  COREFILE="$(mktemp /tmp/Corefile.e2e.XXXXXX)"
  export COREFILE
  cat >"$COREFILE" <<EOF
${COREDNS_ZONE}:${COREDNS_PORT} in-addr.arpa:${COREDNS_PORT} ip6.arpa:${COREDNS_PORT} {
    log
    errors
    debug
    docker {
        zone ${COREDNS_ZONE}
        ttl 10
        networks bridge ${TEST_NETWORK}
    }
}
EOF

  # Start CoreDNS
  "$COREDNS_BINARY" -conf "$COREFILE" &
  COREDNS_PID_FILE="$(mktemp /tmp/coredns.e2e.pid.XXXXXX)"
  export COREDNS_PID_FILE
  echo $! >"$COREDNS_PID_FILE"

  # Wait for CoreDNS to become ready
  wait_for_coredns
}

teardown_file() {
  # Kill CoreDNS
  if [[ -n "$COREDNS_PID_FILE" ]] && [[ -f "$COREDNS_PID_FILE" ]]; then
    local pid
    pid="$(cat "$COREDNS_PID_FILE")"
    if [[ -n "$pid" ]]; then
      kill "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
    fi
    rm -f "$COREDNS_PID_FILE"
  fi

  # Clean up test containers
  docker ps -aq --filter "name=coredns-e2e-" | xargs -r docker rm -f 2>/dev/null || true

  # Remove test networks
  docker network rm "$TEST_NETWORK" 2>/dev/null || true
  docker network rm coredns-e2e-unmonitored 2>/dev/null || true

  # Remove temp Corefile
  if [[ -n "$COREFILE" ]]; then
    rm -f "$COREFILE"
  fi
}

# --- Helpers ---

wait_for_coredns() {
  local retries=20
  local i=0
  while [ "$i" -lt "$retries" ]; do
    if dig +short +time=1 +tries=1 @127.0.0.1 -p "$COREDNS_PORT" version.bind chaos txt >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.5
    i=$((i + 1))
  done
  echo "CoreDNS did not become ready" >&2
  return 1
}

wait_for_record() {
  local name="$1"
  local type="${2:-A}"
  local retries=20
  local i=0
  while [ "$i" -lt "$retries" ]; do
    local result
    result=$(dig +short +time=1 +tries=1 @127.0.0.1 -p "$COREDNS_PORT" "$name" "$type" 2>/dev/null)
    if [ -n "$result" ]; then
      echo "$result"
      return 0
    fi
    sleep 0.5
    i=$((i + 1))
  done
  echo "Record $name ($type) not found after waiting" >&2
  return 1
}

wait_for_record_on_port() {
  local name="$1"
  local type="${2:-A}"
  local port="${3:-$COREDNS_PORT}"
  local retries=20
  local i=0
  while [ "$i" -lt "$retries" ]; do
    local result
    result=$(dig +short +time=1 +tries=1 @127.0.0.1 -p "$port" "$name" "$type" 2>/dev/null)
    if [ -n "$result" ]; then
      echo "$result"
      return 0
    fi
    sleep 0.5
    i=$((i + 1))
  done
  echo "Record $name ($type) not found on port $port after waiting" >&2
  return 1
}

wait_for_ptr_record() {
  local ip="$1"
  local retries=20
  local i=0
  while [ "$i" -lt "$retries" ]; do
    local result
    result=$(dig +short +time=1 +tries=1 @127.0.0.1 -p "$COREDNS_PORT" -x "$ip" 2>/dev/null)
    if [ -n "$result" ]; then
      echo "$result"
      return 0
    fi
    sleep 0.5
    i=$((i + 1))
  done
  echo "PTR record for $ip not found after waiting" >&2
  return 1
}

wait_for_record_gone() {
  local name="$1"
  local type="${2:-A}"
  local retries=20
  local i=0
  while [ "$i" -lt "$retries" ]; do
    local result
    result=$(dig +short +time=1 +tries=1 @127.0.0.1 -p "$COREDNS_PORT" "$name" "$type" 2>/dev/null)
    if [ -z "$result" ]; then
      return 0
    fi
    sleep 0.5
    i=$((i + 1))
  done
  echo "Record $name ($type) still present after waiting" >&2
  return 1
}

flunk() {
  {
    if [[ "$#" -eq 0 ]]; then
      cat -
    else
      echo "$*"
    fi
  }
  return 1
}

assert_success() {
  if [[ "$status" -ne 0 ]]; then
    flunk "command failed with exit status $status"
  elif [[ "$#" -gt 0 ]]; then
    assert_output "$1"
  fi
}

assert_output() {
  local expected
  if [[ $# -eq 0 ]]; then
    expected="$(cat -)"
  else
    expected="$1"
  fi
  assert_equal "$expected" "$output"
}

assert_equal() {
  if [[ "$1" != "$2" ]]; then
    {
      echo "expected: $1"
      echo "actual:   $2"
    } | flunk
  fi
}

assert_output_contains() {
  local input="$output"
  local expected="$1"
  local count="${2:-1}"
  local found=0
  until [ "${input/$expected/}" = "$input" ]; do
    input="${input/$expected/}"
    found=$((found + 1))
  done
  assert_equal "$count" "$found"
}

# --- Tests ---

@test "[e2e] A record: basic container resolves" {
  docker run -d --name coredns-e2e-web --network bridge alpine sleep 3600

  run wait_for_record "coredns-e2e-web.${COREDNS_ZONE}"
  assert_success
  [[ "$output" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]

  # Verify the IP matches the container's actual IP
  expected_ip=$(docker inspect -f '{{.NetworkSettings.Networks.bridge.IPAddress}}' coredns-e2e-web)
  assert_equal "$expected_ip" "$output"

  docker rm -f coredns-e2e-web
}

@test "[e2e] SRV record: container with SRV label resolves" {
  docker run -d --name coredns-e2e-srvweb --network bridge \
    --label "com.dokku.coredns-docker/srv._tcp._http=80" \
    alpine sleep 3600

  run wait_for_record "_http._tcp.coredns-e2e-srvweb.${COREDNS_ZONE}" "SRV"
  assert_success
  assert_output_contains "80 coredns-e2e-srvweb.${COREDNS_ZONE}."

  docker rm -f coredns-e2e-srvweb
}

@test "[e2e] NODATA: AAAA query for IPv4-only container returns empty answer" {
  docker run -d --name coredns-e2e-v4only --network bridge alpine sleep 3600

  run wait_for_record "coredns-e2e-v4only.${COREDNS_ZONE}" "A"
  assert_success

  run dig +time=2 +tries=1 @127.0.0.1 -p "$COREDNS_PORT" "coredns-e2e-v4only.${COREDNS_ZONE}" AAAA
  assert_success
  assert_output_contains "status: NOERROR"
  assert_output_contains "ANSWER: 0"

  docker rm -f coredns-e2e-v4only
}

@test "[e2e] nonexistent container: query returns NXDOMAIN" {
  run dig +time=2 +tries=1 @127.0.0.1 -p "$COREDNS_PORT" "coredns-e2e-nonexistent.${COREDNS_ZONE}" A
  assert_success
  assert_output_contains "status: NXDOMAIN"
}

@test "[e2e] SOA record: zone apex returns SOA" {
  run dig +time=2 +tries=1 @127.0.0.1 -p "$COREDNS_PORT" "${COREDNS_ZONE}" SOA
  assert_success
  assert_output_contains "status: NOERROR"
  assert_output_contains "ANSWER: 1"
  assert_output_contains "ns.dns.${COREDNS_ZONE}."
  assert_output_contains "hostmaster.${COREDNS_ZONE}."
}

@test "[e2e] NS record: zone apex returns NS" {
  run dig +time=2 +tries=1 @127.0.0.1 -p "$COREDNS_PORT" "${COREDNS_ZONE}" NS
  assert_success
  assert_output_contains "status: NOERROR"
  assert_output_contains "ANSWER: 1"
  assert_output_contains "ns.dns.${COREDNS_ZONE}."
}

@test "[e2e] NXDOMAIN: response includes SOA in authority section" {
  run dig +time=2 +tries=1 @127.0.0.1 -p "$COREDNS_PORT" "coredns-e2e-soa-nonexistent.${COREDNS_ZONE}" A
  assert_success
  assert_output_contains "status: NXDOMAIN"
  assert_output_contains "AUTHORITY: 1"
  assert_output_contains "SOA"
}

@test "[e2e] NODATA: response includes SOA in authority section" {
  docker run -d --name coredns-e2e-nodata-soa --network bridge alpine sleep 3600

  run wait_for_record "coredns-e2e-nodata-soa.${COREDNS_ZONE}" "A"
  assert_success

  run dig +time=2 +tries=1 @127.0.0.1 -p "$COREDNS_PORT" "coredns-e2e-nodata-soa.${COREDNS_ZONE}" AAAA
  assert_success
  assert_output_contains "status: NOERROR"
  assert_output_contains "ANSWER: 0"
  assert_output_contains "AUTHORITY: 1"
  assert_output_contains "SOA"

  docker rm -f coredns-e2e-nodata-soa
}

@test "[e2e] container removal: record is cleared after container is removed" {
  docker run -d --name coredns-e2e-ephemeral --network bridge alpine sleep 3600

  run wait_for_record "coredns-e2e-ephemeral.${COREDNS_ZONE}"
  assert_success
  [[ -n "$output" ]]

  # Remove container and wait for record to disappear
  docker rm -f coredns-e2e-ephemeral

  run wait_for_record_gone "coredns-e2e-ephemeral.${COREDNS_ZONE}"
  assert_success

  # Verify record is gone
  run dig +time=2 +tries=1 @127.0.0.1 -p "$COREDNS_PORT" "coredns-e2e-ephemeral.${COREDNS_ZONE}" A
  assert_success
  assert_output_contains "status: NXDOMAIN"
}

@test "[e2e] network alias: container resolves via network alias" {
  docker run -d --name coredns-e2e-aliased \
    --network "$TEST_NETWORK" \
    --network-alias myalias \
    alpine sleep 3600

  # Wait for alias record to appear
  run wait_for_record "myalias.${COREDNS_ZONE}"
  assert_success
  [[ "$output" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]

  # Also verify the container name itself resolves
  run wait_for_record "coredns-e2e-aliased.${COREDNS_ZONE}"
  assert_success
  [[ "$output" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]

  docker rm -f coredns-e2e-aliased
}

@test "[e2e] compose naming: project.service name resolves" {
  docker run -d --name coredns-e2e-compose --network bridge \
    --label "com.docker.compose.project=myproj" \
    --label "com.docker.compose.service=mysvc" \
    alpine sleep 3600

  run wait_for_record "myproj.mysvc.${COREDNS_ZONE}"
  assert_success
  [[ "$output" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]

  docker rm -f coredns-e2e-compose
}

@test "[e2e] hostname label: container resolves via hostname label" {
  docker run -d --name coredns-e2e-hostname --network bridge \
    --label "com.dokku.coredns-docker/hostname=myapp,otherapp" \
    alpine sleep 3600

  # Verify container name resolves
  run wait_for_record "coredns-e2e-hostname.${COREDNS_ZONE}"
  assert_success
  [[ "$output" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]
  local container_ip="$output"

  # Verify first hostname resolves to same IP
  run wait_for_record "myapp.${COREDNS_ZONE}"
  assert_success
  assert_equal "$container_ip" "$output"

  # Verify second hostname resolves to same IP
  run wait_for_record "otherapp.${COREDNS_ZONE}"
  assert_success
  assert_equal "$container_ip" "$output"

  docker rm -f coredns-e2e-hostname
}

@test "[e2e] hostname label with SRV: SRV records created for hostname names" {
  docker run -d --name coredns-e2e-hostname-srv --network bridge \
    --label "com.dokku.coredns-docker/hostname=myapp" \
    --label "com.dokku.coredns-docker/srv._tcp._http=80" \
    alpine sleep 3600

  # Verify SRV record for container name
  run wait_for_record "_http._tcp.coredns-e2e-hostname-srv.${COREDNS_ZONE}" "SRV"
  assert_success
  assert_output_contains "80 coredns-e2e-hostname-srv.${COREDNS_ZONE}."

  # Verify SRV record for hostname label
  run wait_for_record "_http._tcp.myapp.${COREDNS_ZONE}" "SRV"
  assert_success
  assert_output_contains "80 myapp.${COREDNS_ZONE}."

  docker rm -f coredns-e2e-hostname-srv
}

@test "[e2e] network filtering: container on unmonitored network does not resolve" {
  docker network create coredns-e2e-unmonitored 2>/dev/null || true
  docker run -d --name coredns-e2e-filtered --network coredns-e2e-unmonitored alpine sleep 3600
  sleep 2

  run dig +time=2 +tries=1 @127.0.0.1 -p "$COREDNS_PORT" "coredns-e2e-filtered.${COREDNS_ZONE}" A
  assert_success
  assert_output_contains "status: NXDOMAIN"

  docker rm -f coredns-e2e-filtered
  docker network rm coredns-e2e-unmonitored 2>/dev/null || true
}

@test "[e2e] SRV fallback: exposed port generates SRV record" {
  docker run -d --name coredns-e2e-srvport --network bridge \
    --expose 5432 \
    alpine sleep 3600

  run wait_for_record "_tcp._tcp.coredns-e2e-srvport.${COREDNS_ZONE}" "SRV"
  assert_success
  assert_output_contains "5432 coredns-e2e-srvport.${COREDNS_ZONE}."

  docker rm -f coredns-e2e-srvport
}

@test "[e2e] TTL: response has correct TTL value" {
  docker run -d --name coredns-e2e-ttl --network bridge alpine sleep 3600

  run wait_for_record "coredns-e2e-ttl.${COREDNS_ZONE}"
  assert_success

  run dig +time=2 +tries=1 @127.0.0.1 -p "$COREDNS_PORT" "coredns-e2e-ttl.${COREDNS_ZONE}" A
  assert_success
  # Match TTL of 10 in the ANSWER section regardless of whitespace format
  [[ "$output" =~ [[:space:]]10[[:space:]]+IN[[:space:]]+A[[:space:]] ]]

  docker rm -f coredns-e2e-ttl
}

@test "[e2e] wildcard: arbitrary subdomain resolves" {
  docker run -d --name coredns-e2e-wildcard --network bridge \
    --label "com.dokku.coredns-docker/wildcard=true" \
    alpine sleep 3600

  # Wait for the base record first
  run wait_for_record "coredns-e2e-wildcard.${COREDNS_ZONE}"
  assert_success
  local container_ip="$output"

  # Wildcard subdomain should resolve to same IP
  run wait_for_record "anything.coredns-e2e-wildcard.${COREDNS_ZONE}"
  assert_success
  assert_equal "$container_ip" "$output"

  docker rm -f coredns-e2e-wildcard
}

@test "[e2e] wildcard: exact match takes precedence" {
  docker run -d --name coredns-e2e-wildcard-exact --network bridge \
    --label "com.dokku.coredns-docker/wildcard=true" \
    alpine sleep 3600

  run wait_for_record "coredns-e2e-wildcard-exact.${COREDNS_ZONE}"
  assert_success

  # The exact container name record should resolve (not via wildcard)
  run dig +time=2 +tries=1 @127.0.0.1 -p "$COREDNS_PORT" "coredns-e2e-wildcard-exact.${COREDNS_ZONE}" A
  assert_success
  assert_output_contains "status: NOERROR"
  assert_output_contains "ANSWER: 1"

  docker rm -f coredns-e2e-wildcard-exact
}

@test "[e2e] wildcard: deep subdomain does not match (RFC 4592)" {
  docker run -d --name coredns-e2e-wildcard-deep --network bridge \
    --label "com.dokku.coredns-docker/wildcard=true" \
    alpine sleep 3600

  run wait_for_record "coredns-e2e-wildcard-deep.${COREDNS_ZONE}"
  assert_success

  # Deep subdomain should NOT match the wildcard
  run dig +time=2 +tries=1 @127.0.0.1 -p "$COREDNS_PORT" \
    "deep.sub.coredns-e2e-wildcard-deep.${COREDNS_ZONE}" A
  assert_success
  assert_output_contains "status: NXDOMAIN"

  docker rm -f coredns-e2e-wildcard-deep
}

@test "[e2e] multi-zone: container resolves under multiple zones" {
  local MULTI_COREFILE
  MULTI_COREFILE="$(mktemp /tmp/Corefile.multizone.XXXXXX)"
  local MULTI_PORT=15354
  cat >"$MULTI_COREFILE" <<EOF
docker.localhost:${MULTI_PORT} internal.localhost:${MULTI_PORT} {
    log
    errors
    debug
    docker {
        zone docker.localhost internal.localhost
        ttl 10
        networks bridge ${TEST_NETWORK}
    }
}
EOF

  "$COREDNS_BINARY" -conf "$MULTI_COREFILE" &
  local MULTI_PID=$!

  # Wait for CoreDNS to become ready
  local retries=20 i=0
  while [ "$i" -lt "$retries" ]; do
    if dig +short +time=1 +tries=1 @127.0.0.1 -p "$MULTI_PORT" version.bind chaos txt >/dev/null 2>&1; then
      break
    fi
    sleep 0.5
    i=$((i + 1))
  done

  docker run -d --name coredns-e2e-multizone --network bridge alpine sleep 3600

  # Verify resolution in first zone
  run wait_for_record_on_port "coredns-e2e-multizone.docker.localhost" "A" "$MULTI_PORT"
  assert_success
  [[ "$output" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]
  local ip1="$output"

  # Verify resolution in second zone
  run wait_for_record_on_port "coredns-e2e-multizone.internal.localhost" "A" "$MULTI_PORT"
  assert_success
  [[ "$output" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]
  local ip2="$output"

  # Both zones should resolve to the same IP
  assert_equal "$ip1" "$ip2"

  # Cleanup
  docker rm -f coredns-e2e-multizone
  kill "$MULTI_PID" 2>/dev/null || true
  wait "$MULTI_PID" 2>/dev/null || true
  rm -f "$MULTI_COREFILE"
}

@test "[e2e] PTR record: reverse DNS for container IP resolves" {
  docker run -d --name coredns-e2e-ptr --network bridge alpine sleep 3600

  run wait_for_record "coredns-e2e-ptr.${COREDNS_ZONE}"
  assert_success
  local container_ip="$output"

  run wait_for_ptr_record "$container_ip"
  assert_success
  assert_output_contains "coredns-e2e-ptr.${COREDNS_ZONE}."

  docker rm -f coredns-e2e-ptr
}

@test "[e2e] PTR record: reverse DNS cleared after container removal" {
  docker run -d --name coredns-e2e-ptr-rm --network bridge alpine sleep 3600

  run wait_for_record "coredns-e2e-ptr-rm.${COREDNS_ZONE}"
  assert_success
  local container_ip="$output"

  run wait_for_ptr_record "$container_ip"
  assert_success

  docker rm -f coredns-e2e-ptr-rm

  run wait_for_record_gone "coredns-e2e-ptr-rm.${COREDNS_ZONE}"
  assert_success

  # PTR should also be gone
  run dig +short +time=2 +tries=1 @127.0.0.1 -p "$COREDNS_PORT" -x "$container_ip"
  [[ -z "$output" ]]
}

@test "[e2e] host_mode: published port resolves to host IP and host port" {
  local HOSTMODE_COREFILE
  HOSTMODE_COREFILE="$(mktemp /tmp/Corefile.hostmode.XXXXXX)"
  local HOSTMODE_PORT=15355
  local PUBLISHED_PORT=15800
  cat >"$HOSTMODE_COREFILE" <<EOF
docker.localhost:${HOSTMODE_PORT} {
    log
    errors
    debug
    docker {
        zone docker.localhost
        ttl 10
        networks bridge ${TEST_NETWORK}
        host_mode
    }
}
EOF

  "$COREDNS_BINARY" -conf "$HOSTMODE_COREFILE" &
  local HOSTMODE_PID=$!

  # Wait for CoreDNS to become ready
  local retries=20 i=0
  while [ "$i" -lt "$retries" ]; do
    if dig +short +time=1 +tries=1 @127.0.0.1 -p "$HOSTMODE_PORT" version.bind chaos txt >/dev/null 2>&1; then
      break
    fi
    sleep 0.5
    i=$((i + 1))
  done

  docker run -d --name coredns-e2e-hostmode --network bridge \
    -p "127.0.0.1:${PUBLISHED_PORT}:80" \
    alpine sleep 3600

  # A record should resolve to the published host IP.
  run wait_for_record_on_port "coredns-e2e-hostmode.docker.localhost" "A" "$HOSTMODE_PORT"
  assert_success
  assert_equal "127.0.0.1" "$output"

  # SRV fallback should emit a record with the published host port.
  run dig +short +time=2 +tries=1 @127.0.0.1 -p "$HOSTMODE_PORT" \
    "_tcp._tcp.coredns-e2e-hostmode.docker.localhost" SRV
  assert_success
  assert_output_contains "${PUBLISHED_PORT} coredns-e2e-hostmode.docker.localhost."

  # PTR should NOT resolve by default (host_mode without ptr).
  run dig +time=2 +tries=1 @127.0.0.1 -p "$HOSTMODE_PORT" -x 127.0.0.1
  assert_success
  assert_output_contains "status: NXDOMAIN"

  # Cleanup
  docker rm -f coredns-e2e-hostmode
  kill "$HOSTMODE_PID" 2>/dev/null || true
  wait "$HOSTMODE_PID" 2>/dev/null || true
  rm -f "$HOSTMODE_COREFILE"
}

@test "[e2e] host_mode: container without published ports produces no records" {
  local HOSTMODE_COREFILE
  HOSTMODE_COREFILE="$(mktemp /tmp/Corefile.hostmode.XXXXXX)"
  local HOSTMODE_PORT=15356
  cat >"$HOSTMODE_COREFILE" <<EOF
docker.localhost:${HOSTMODE_PORT} {
    log
    errors
    debug
    docker {
        zone docker.localhost
        ttl 10
        networks bridge ${TEST_NETWORK}
        host_mode
    }
}
EOF

  "$COREDNS_BINARY" -conf "$HOSTMODE_COREFILE" &
  local HOSTMODE_PID=$!

  local retries=20 i=0
  while [ "$i" -lt "$retries" ]; do
    if dig +short +time=1 +tries=1 @127.0.0.1 -p "$HOSTMODE_PORT" version.bind chaos txt >/dev/null 2>&1; then
      break
    fi
    sleep 0.5
    i=$((i + 1))
  done

  docker run -d --name coredns-e2e-hostmode-nobind --network bridge alpine sleep 3600
  sleep 2

  run dig +time=2 +tries=1 @127.0.0.1 -p "$HOSTMODE_PORT" \
    "coredns-e2e-hostmode-nobind.docker.localhost" A
  assert_success
  assert_output_contains "status: NXDOMAIN"

  # Cleanup
  docker rm -f coredns-e2e-hostmode-nobind
  kill "$HOSTMODE_PID" 2>/dev/null || true
  wait "$HOSTMODE_PID" 2>/dev/null || true
  rm -f "$HOSTMODE_COREFILE"
}
