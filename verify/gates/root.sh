#!/usr/bin/env bash
# The other half of the boundary gate: properties only root can inspect. Runs
# in the guest as root, through the administration account.
set -uo pipefail

fail=0

pass() { printf 'PASS  %s\n' "$1"; }
flunk() {
  printf 'FAIL  %s\n' "$1"
  fail=1
}

if [[ "$(id -un)" == root ]]; then
  pass "inspecting as root"
else
  flunk "inspecting as root (got $(id -un))"
fi

drops=$(nft list ruleset | grep -c 'policy drop' || true)
if [[ "$drops" -ge 2 ]]; then
  pass "input and output both default to drop"
else
  flunk "input and output both default to drop (found $drops)"
fi

# 3128 is the only way out. The broker's port is an operator tool: while it
# authenticates no caller, a reachable broker lets anything in the guest mint
# itself a GitHub token, so it must stay closed.
ports=$(nft list chain inet crew output | grep -oE 'dport [0-9]+ accept' | awk '{print $2}' | sort -u | tr '\n' ' ')
if [[ "$ports" == "3128 " ]]; then
  pass "the only reachable host port is the egress proxy (${ports% })"
else
  flunk "the only reachable host port is the egress proxy (got '${ports}')"
fi

if [[ "$ports" == *"3129"* ]]; then
  flunk "the token broker is not reachable from the guest (3129 is open)"
else
  pass "the token broker is not reachable from the guest"
fi

# The gate stages its scripts here and then runs them as root. If `agent` can write
# this directory it can pre-own the path root executes, so the ownership is part of
# the boundary, not a detail of the driver.
if [[ -d /run/crew-gate ]]; then
  gate_owner=$(stat -c '%U:%a' /run/crew-gate)
  if [[ "$gate_owner" == root:* ]]; then
    pass "the gate directory is owned by root ($gate_owner)"
  else
    flunk "the gate directory is owned by root (got $gate_owner)"
  fi
else
  flunk "the gate directory exists"
fi

owner=$(stat -c '%U:%a' /etc/nftables.conf)
if [[ "$owner" == root:644 ]]; then
  pass "the ruleset is owned by root ($owner)"
else
  flunk "the ruleset is owned by root (got $owner)"
fi

if id -nG agent | tr ' ' '\n' | grep -qxE 'sudo|admin|wheel'; then
  flunk "agent is in no privileged group"
else
  pass "agent is in no privileged group"
fi

if grep -rqs '^[^#]*agent' /etc/sudoers /etc/sudoers.d/; then
  flunk "agent appears in no sudoers rule"
else
  pass "agent appears in no sudoers rule"
fi

if [[ -e /etc/crew-provisioned ]]; then
  pass "bootstrap is marked complete, so a reboot will not reopen egress"
else
  flunk "bootstrap is marked complete, so a reboot will not reopen egress"
fi

if ss -Hlnt 'sport = :22' | grep -q .; then
  pass "the guest accepts ssh from the host"
else
  flunk "the guest accepts ssh from the host"
fi

exit "$fail"
