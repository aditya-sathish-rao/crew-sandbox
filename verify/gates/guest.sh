#!/usr/bin/env bash
# What runs inside the boundary must not be able to do. Runs in the guest as
# `agent`, the account the fleet uses. Every check is a capability, not a
# configuration: it would still hold if someone re-enabled sudo or reopened egress.
set -uo pipefail

fail=0

pass() { printf 'PASS  %s\n' "$1"; }
flunk() {
  printf 'FAIL  %s\n' "$1"
  fail=1
}

# expect_fail <label> <command...>: the command must not succeed. Asking for the
# forbidden thing tests the machine as it is now, after every change anyone made
# by hand, which is the only thing worth gating on.
expect_fail() {
  local label=$1
  shift
  if "$@" >/dev/null 2>&1; then
    flunk "$label"
  else
    pass "$label"
  fi
}

if [[ "$(id -un)" == agent ]]; then
  pass "running as agent"
else
  flunk "running as agent (got $(id -un))"
fi

if [[ -e /Users ]]; then
  flunk "the host filesystem is absent"
else
  pass "the host filesystem is absent"
fi

# A virtiofs or 9p mount is a host directory with the host's permissions on it.
# A workspace is the one opt-in exception, so the gate is told whether one was
# asked for: the assertion becomes "that one, and nothing else". It is applied when
# the VM is created, which is why the operator has to say so here rather than
# leaving the gate to guess.
workspace=${CREW_WORKSPACE:-}
writable=${CREW_WRITABLE:-false}
host_mounts=$(mount | grep -E '\b(9p|virtiofs|fuse\.)' || true)
if [[ -z "$host_mounts" ]]; then
  pass "no host directory is mounted"
elif [[ -n "$workspace" ]]; then
  expected=$(printf '%s\n' "$host_mounts" | grep -cF " on ${workspace} " || true)
  others=$(printf '%s\n' "$host_mounts" | grep -vcF " on ${workspace} " || true)
  if [[ "$expected" -eq 1 && "$others" -ne 0 ]]; then
    flunk "the workspace is the only host directory mounted (also: $(printf '%s\n' "$host_mounts" | grep -vF " on ${workspace} " | tr '\n' ' '))"
  elif [[ "$expected" -ne 1 ]]; then
    flunk "the workspace is the only host directory mounted (nothing at ${workspace})"
  elif [[ "$writable" != true && "$host_mounts" != *"(ro,"* ]]; then
    flunk "the workspace mount is read-only (${host_mounts//$'\n'/ })"
  elif [[ "$writable" == true ]]; then
    pass "the workspace is the only host directory mounted, and it was asked for writable"
  else
    pass "the workspace is the only host directory mounted, read-only"
  fi
else
  flunk "no host directory is mounted (${host_mounts//$'\n'/ })"
fi

expect_fail "egress to an arbitrary host is blocked" curl -sS -m 5 -o /dev/null https://example.com
expect_fail "a query to an arbitrary resolver is blocked" dig +time=2 +tries=1 @1.1.1.1 example.com
expect_fail "the system resolver cannot resolve" getent hosts example.com
expect_fail "the host is unreachable on any port but the proxy" curl -sS -m 3 -o /dev/null \
  "http://$(ip -4 route show default | awk '{print $3; exit}'):80"
expect_fail "sudo is unavailable" sudo -n true
expect_fail "the firewall cannot be flushed" nft flush ruleset
expect_fail "the firewall ruleset cannot be read" nft list ruleset
expect_fail "the firewall configuration is not writable" test -w /etc/nftables.conf

exit "$fail"
