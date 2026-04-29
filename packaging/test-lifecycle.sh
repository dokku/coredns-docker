#!/bin/bash
# Exercise the install -> start -> remove -> purge lifecycle of the .deb.
# Requires: a Debian-flavored host with apt, systemd, and a running docker daemon.
# Usage: packaging/test-lifecycle.sh path/to/coredns-docker_<ver>_<arch>.deb

set -euo pipefail

DEB="${1:?usage: $0 path/to/coredns-docker.deb}"

if [ ! -f "$DEB" ]; then
    echo "deb not found: $DEB" >&2
    exit 2
fi

# Convert to absolute path - apt-get insists on a leading "./" or "/" for a local file.
DEB="$(readlink -f "$DEB")"

export DEBIAN_FRONTEND=noninteractive

dump_logs() {
    echo "==== service status ===="
    systemctl --no-pager status coredns-docker.service || true
    echo "==== service journal ===="
    journalctl --no-pager -u coredns-docker.service || true
}
trap 'rc=$?; if [ $rc -ne 0 ]; then dump_logs; fi; exit $rc' EXIT

assert() {
    if ! eval "$1"; then
        echo "ASSERT FAILED: $1" >&2
        exit 1
    fi
}

refute() {
    if eval "$1"; then
        echo "REFUTE FAILED (expected to fail): $1" >&2
        exit 1
    fi
}

echo "==> Preflight: docker daemon must be active"
sudo systemctl is-active --quiet docker.service

echo "==> Install dnsutils for the DNS smoke test"
sudo apt-get update -qq
sudo apt-get install -y --no-install-recommends dnsutils iproute2

echo "==> Install $DEB"
sudo apt-get install -y "$DEB"

echo "==> Files installed"
assert "test -x /usr/bin/coredns-docker"
assert "test -f /etc/coredns/Corefile"
assert "test -f /lib/systemd/system/coredns-docker.service"

echo "==> User/group created and added to docker group"
assert "getent passwd coredns-docker >/dev/null"
assert "getent group coredns-docker >/dev/null"
assert "id -nG coredns-docker | tr ' ' '\n' | grep -qx docker"

echo "==> Unit is enabled"
assert "systemctl is-enabled --quiet coredns-docker.service"

echo "==> Wait up to 30s for service to become active"
for _ in $(seq 1 30); do
    if systemctl is-active --quiet coredns-docker.service; then break; fi
    sleep 1
done
assert "systemctl is-active --quiet coredns-docker.service"

echo "==> Service is bound to UDP/1053"
assert "ss -uln | awk '{print \$5}' | grep -qE ':1053\$'"

echo "==> Synthetic SOA query succeeds"
SOA="$(dig +short @127.0.0.1 -p 1053 docker.localhost SOA)"
echo "    SOA: $SOA"
assert "[ -n \"$SOA\" ]"

echo "==> Ordering: After=docker.service and Before=nginx.service"
systemctl show coredns-docker.service --property=After --property=Before | tee /tmp/coredns-ordering
assert "grep -q '^After=.*docker.service' /tmp/coredns-ordering"
assert "grep -q '^Before=.*nginx.service' /tmp/coredns-ordering"

echo "==> apt remove"
sudo apt-get remove -y coredns-docker

refute "test -f /usr/bin/coredns-docker"
refute "test -f /lib/systemd/system/coredns-docker.service"
refute "systemctl is-active --quiet coredns-docker.service"
# Conffile and user are preserved on remove
assert "test -f /etc/coredns/Corefile"
assert "getent passwd coredns-docker >/dev/null"

echo "==> apt purge"
sudo apt-get purge -y coredns-docker

refute "test -d /etc/coredns"
refute "getent passwd coredns-docker >/dev/null"
refute "getent group coredns-docker >/dev/null"

echo "==> Lifecycle test passed"
trap - EXIT
