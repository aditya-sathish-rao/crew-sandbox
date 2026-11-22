#!/usr/bin/env bash
# The guest reaches the model only through the host gateway, and the real key
# exists only in that proxy's process. Runs in the guest as `agent`.
set -uo pipefail

fail=0

pass() { printf 'PASS  %s\n' "$1"; }
flunk() {
  printf 'FAIL  %s\n' "$1"
  fail=1
}
# skip: an assertion this run cannot make because the thing it would test is not
# configured. Never counted as a pass.
skip() { printf 'SKIP  %s (%s)\n' "$1" "$2"; }

gateway="http://host.lima.internal:3128/model"
token_file=/etc/crew-model-token
models="$HOME/.pi/agent/models.json"
settings="$HOME/.pi/agent/settings.json"

if [[ -s "$token_file" ]]; then
  token=$(cat "$token_file")
  pass "the guest holds a model gateway token"
else
  token=""
  flunk "the guest holds a model gateway token"
fi

if [[ -n "$token" && "$token" != sk-* ]]; then
  pass "the gateway token is not a model key"
else
  flunk "the gateway token is not a model key"
fi

# The host passes the fingerprint, never the key. A key copied under another
# variable name still matches, so this proves the value is absent, not just its
# usual name.
if [[ -n "${CREW_REAL_KEY_SHA256:-}" ]]; then
  leaked=0
  while IFS= read -r line; do
    value=${line#*=}
    [[ -n "$value" ]] || continue
    hash=$(printf '%s' "$value" | sha256sum | awk '{print $1}')
    if [[ "$hash" == "$CREW_REAL_KEY_SHA256" ]]; then
      leaked=1
      break
    fi
  done < <(env)
  if [[ "$leaked" -eq 0 ]]; then
    pass "no environment value is the model key"
  else
    flunk "no environment value is the model key"
  fi
else
  flunk "the gate was given the model key's fingerprint"
fi

# A missing file is the guest configuration not having been applied, which is worth
# saying: the boundary is intact and the fleet has nowhere to point.
missing() { flunk "$1 (no ${2}; apply the guest configuration)"; }

if [[ ! -f "$models" ]]; then
  missing "the model client's provider points at the gateway" "$models"
  missing "the model client reads its gateway token from the guest file" "$models"
elif grep -q 'host.lima.internal:3128/model' "$models"; then
  pass "the model client's provider points at the gateway"
else
  flunk "the model client's provider points at the gateway"
fi

if [[ -f "$models" ]]; then
  if grep -q "!cat ${token_file}" "$models"; then
    pass "the model client reads its gateway token from the guest file"
  else
    flunk "the model client reads its gateway token from the guest file"
  fi
fi

# Read the names the guest is actually configured with rather than naming a
# provider here: the gateway is provider-agnostic, so the gate must be too.
provider=$(sed -n 's/.*"defaultProvider"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$settings" 2>/dev/null | head -1)
model=$(sed -n 's/.*"defaultModel"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$settings" 2>/dev/null | head -1)
if [[ -n "$provider" && -n "$model" ]]; then
  pass "the model client defaults to a provider through the gateway (${provider}/${model})"
elif [[ ! -f "$settings" ]]; then
  missing "the model client defaults to a provider through the gateway" "$settings"
else
  flunk "the model client defaults to a provider through the gateway (got '${provider}/${model}')"
fi

code=$(curl --noproxy '*' -sS -m 10 -o /dev/null -w '%{http_code}' "$gateway/models" 2>/dev/null || echo 000)
if [[ "$code" == 401 ]]; then
  pass "the gateway refuses a request without a token"
else
  flunk "the gateway refuses a request without a token (got ${code})"
fi

code=$(curl --noproxy '*' -sS -m 10 -o /dev/null -w '%{http_code}' \
  -H 'Authorization: Bearer not-the-guest' "$gateway/models" 2>/dev/null || echo 000)
if [[ "$code" == 401 ]]; then
  pass "the gateway refuses a request with the wrong token"
else
  flunk "the gateway refuses a request with the wrong token (got ${code})"
fi

body=$(mktemp)
code=$(curl --noproxy '*' -sS -m 30 -o "$body" -w '%{http_code}' \
  -H "Authorization: Bearer $token" "$gateway/models" 2>/dev/null || echo 000)
if [[ "$code" == 200 ]] && grep -q '"data"' "$body"; then
  pass "the gateway reaches the model API with the host's key"
else
  flunk "the gateway reaches the model API with the host's key (got ${code})"
fi
rm -f "$body"

code=$(curl --noproxy '*' -sS -m 10 -o /dev/null -w '%{http_code}' \
  -H "Authorization: Bearer $token" "$gateway/not-a-model-route" 2>/dev/null || echo 000)
if [[ "$code" == 404 ]]; then
  pass "the gateway refuses paths outside the model API"
else
  flunk "the gateway refuses paths outside the model API (got ${code})"
fi

# The upstream is named by the operator, so the gate is told it rather than
# assuming a vendor. Reaching it directly is what the guest must not be able to do.
upstream=${CREW_MODEL_UPSTREAM:-}
if [[ -n "$upstream" ]]; then
  origin=$(printf '%s' "$upstream" | awk -F/ '{ print $1"//"$3 }')
  if curl -sS -m 5 -o /dev/null "$origin" 2>/dev/null; then
    flunk "the model API is unreachable without the gateway (${origin} answered)"
  else
    pass "the model API is unreachable without the gateway"
  fi
else
  skip "the model API is unreachable without the gateway" "upstream was not given to the gate"
fi

# pi is the harness this repository ships configuration for, not a requirement of
# the boundary, so a guest without it still gets every other assertion.
if command -v pi >/dev/null 2>&1; then
  if [[ -n "$model" ]] && timeout 30 pi --list-models 2>/dev/null | grep -qF "$model"; then
    pass "the harness lists the proxied model (${model})"
  else
    flunk "the harness lists the proxied model (want '${model:-unset}')"
  fi
else
  skip "the harness lists the proxied model" "no pi on PATH"
fi

exit "$fail"
