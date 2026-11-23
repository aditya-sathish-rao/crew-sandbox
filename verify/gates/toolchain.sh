#!/usr/bin/env bash
# The guest can actually run the fleet's tools. Runs as `agent` in a login shell,
# because that is the shell an operator and an ssh session get.
set -uo pipefail
cd /tmp || exit 1

fail=0
pass() { printf 'PASS  %s\n' "$1"; }
flunk() {
  printf 'FAIL  %s\n' "$1"
  fail=1
}

# The fleet is the consumer's, so the gate is told what to expect. A toolchain gate
# with nothing to look for would pass without proving anything, which is worse than
# failing: it reports a boundary nobody checked.
expected=${CREW_EXPECTED_TOOLS:-}

if [[ -n "${https_proxy:-}" ]]; then
  pass "a login shell knows the proxy (${https_proxy})"
else
  flunk "a login shell knows the proxy"
fi

# command -v reports the profile symlink; what matters is where it lands. A tool
# from the distribution or /usr/local would resolve outside the store and fail here.
from_store() {
  local path
  path=$(command -v "$1" 2>/dev/null) || return 1
  [[ -n "$path" ]] && [[ "$(readlink -f "$path")" == /nix/store/* ]]
}

if from_store nix; then
  pass "nix resolves under /nix/store"
else
  flunk "nix resolves under /nix/store (got '$(readlink -f "$(command -v nix 2>/dev/null)" 2>/dev/null || echo nothing)')"
fi

# nix-store --verify checks registration and metadata, not file contents, and the
# same `agent` that could tamper with the store is the account running it. It
# catches drift and accidental corruption; it is not a provenance or integrity
# guarantee. That limitation is stated in SECURITY.md §6 rather than papered over.
if [[ "$(nix-store --verify 2>&1 | grep -c 'error')" -eq 0 ]]; then
  pass "the Nix store has no registration errors (not a content-integrity check)"
else
  flunk "the Nix store has no registration errors (not a content-integrity check)"
fi

if [[ -z "$expected" ]]; then
  flunk "the gate was told which tools to expect (pass --expected-tools \"pi gh\")"
else
  tools=()
  read -ra tools <<<"$expected"
  for tool in "${tools[@]}"; do
    if from_store "$tool"; then
      pass "$tool resolves under /nix/store"
    else
      flunk "$tool resolves under /nix/store"
    fi
  done
fi

exit "$fail"
