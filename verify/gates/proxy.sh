#!/usr/bin/env bash
# What the guest may and may not reach once a proxy session exists. Runs in the
# guest as `agent`, so it proves the boundary from the side that could abuse it.
set -uo pipefail

fail=0

pass() { printf 'PASS  %s\n' "$1"; }
flunk() {
  printf 'FAIL  %s\n' "$1"
  fail=1
}

host=$(ip -4 route show default | awk '{print $3; exit}')
proxy="http://${host}:3128"

if curl -fsS -m 20 -x "$proxy" https://cache.nixos.org/nix-cache-info | grep -q '^StoreDir:'; then
  pass "an allowlisted host answers through the proxy"
else
  flunk "an allowlisted host answers through the proxy"
fi

# github.com alone only covers git. Tooling that drives the REST API needs
# api.github.com, a different host, so a missing entry leaves the fleet broken
# while clone and push still work.
code=$(curl -sS -m 15 -o /dev/null -w '%{http_code}' -x "$proxy" \
  https://api.github.com/ 2>/dev/null || echo 000)
if [[ "$code" != 000 && "$code" != 403 ]]; then
  pass "the GitHub API answers through the proxy (${code})"
else
  flunk "the GitHub API answers through the proxy (got ${code}); add api.github.com to the egress allowlist"
fi

if curl -fsS -m 10 -o /dev/null -x "$proxy" https://example.com 2>/dev/null; then
  flunk "a host off the allowlist is refused over https"
else
  pass "a host off the allowlist is refused over https"
fi

code=$(curl -sS -m 10 -o /dev/null -w '%{http_code}' -x "$proxy" http://example.com 2>/dev/null || echo 000)
if [[ "$code" == 403 ]]; then
  pass "a host off the allowlist is refused over http"
else
  flunk "a host off the allowlist is refused over http (got ${code})"
fi

if curl -fsS -m 10 -o /dev/null -x "$proxy" https://github.com:22 2>/dev/null; then
  flunk "a non-web port is refused even on an allowlisted host"
else
  pass "a non-web port is refused even on an allowlisted host"
fi

# The allowlist names hosts, not addresses. Two ways a guest could otherwise use
# an allowlisted address to reach a host that is not on the list, both of which
# were live before this assertion existed:
#
#   1. plain HTTP: the proxy forwarded the guest's Host header, so a request to
#      an allowlisted CDN address could be answered by any site on that CDN.
#   2. CONNECT: the tunnel only names its destination, so the TLS ClientHello
#      inside it could ask for any other name the same address serves.
#
# Both are checked against a host that is off the allowlist and shares a CDN
# address with one that is on it, so a pass means the name was enforced rather
# than the address. The plain-HTTP half compares the answer with and without a
# spoofed Host rather than expecting a particular status: whatever npm does with
# its own name today, a spoofed Host must not change it.

# answer_for <curl args>: the status and redirect target the proxy returns.
answer_for() {
  curl -sS -m 15 -o /dev/null -D- -x "$proxy" "$@" 2>/dev/null |
    tr -d '\r' | awk 'NR == 1 { print $2 } tolower($1) == "location:" { print $2 }'
}

plain=$(answer_for http://registry.npmjs.org/)
spoofed=$(answer_for -H 'Host: example.com' http://registry.npmjs.org/)
if [[ -n "$plain" && "$plain" != 403* && "$spoofed" == "$plain" ]]; then
  pass "a spoofed Host header cannot retarget a plain-HTTP request"
else
  flunk "a spoofed Host header cannot retarget a plain-HTTP request (allowed: '${plain//$'\n'/ }', spoofed: '${spoofed//$'\n'/ }')"
fi

sni=$(curl -sS -m 15 -o /dev/null -w '%{http_code}' -x "$proxy" \
  --connect-to example.com:443:registry.npmjs.org:443 https://example.com/ 2>/dev/null)
if [[ -z "$sni" || "$sni" == 000 ]]; then
  pass "a tunnel whose SNI names another host is refused"
else
  flunk "a tunnel whose SNI names another host is refused (got ${sni})"
fi

# A tunnel to port 80 would carry plain HTTP, whose Host header the CONNECT route
# cannot see, so it must be refused rather than treated as a second HTTP route.
tunnel80=$(curl -sS -m 10 -o /dev/null -w '%{http_code}' -x "$proxy" -p \
  http://registry.npmjs.org/ 2>/dev/null)
if [[ -z "$tunnel80" || "$tunnel80" == 000 ]]; then
  pass "CONNECT is refused on port 80"
else
  flunk "CONNECT is refused on port 80 (got ${tunnel80})"
fi

if curl -fsS -m 5 -o /dev/null https://cache.nixos.org/nix-cache-info 2>/dev/null; then
  flunk "the guest still cannot reach the internet without the proxy"
else
  pass "the guest still cannot reach the internet without the proxy"
fi

exit "$fail"
