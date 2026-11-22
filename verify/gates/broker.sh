#!/usr/bin/env bash
# The broker mints a token for one repository, GitHub refuses it elsewhere, and a
# revoke makes it unusable. Runs on the host, not in the guest.
#
# The write probes go to an agent branch, never to the default branch. That is
# deliberate: a `contents: write` token that could write the default branch could
# also merge its own pull request, so the human-merge backstop would not exist. The
# gate therefore asserts two opposite things, and both must hold:
#   1. the token CAN write its own repository on an agent branch, and
#   2. the default branch REFUSES the same token, because a ruleset requires review.
set -uo pipefail

fail=0

pass() { printf 'PASS  %s\n' "$1"; }
flunk() {
  printf 'FAIL  %s\n' "$1"
  fail=1
}

ruleset=${CREW_BROKER_RULESET:-}
base="http://127.0.0.1:${CREW_BROKER_PORT:-3139}"
task="gate-$$"
token=""
full=""
agent_branch=""

# shellcheck disable=SC2329  # invoked by the EXIT trap below
cleanup() {
  # The branch goes first: deleting it needs the token, and the revoke below kills it.
  if [[ -n "$token" && -n "$full" && -n "$agent_branch" ]]; then
    curl -sS -m 20 -o /dev/null -X DELETE -H "Authorization: token $token" \
      "https://api.github.com/repos/${full}/git/refs/heads/${agent_branch}" 2>/dev/null || true
  fi
  if [[ -n "$token" ]]; then
    curl -sS -m 10 -o /dev/null -X DELETE "$base/token/$task" 2>/dev/null || true
  fi
}
trap cleanup EXIT

# gh_api <method> <path> <outfile> [extra curl args...] -> prints the status code
gh_api() {
  local method=$1 path=$2 out=$3
  shift 3
  curl -sS -m 30 -o "$out" -w '%{http_code}' -X "$method" \
    -H "Authorization: token $token" \
    -H 'accept: application/vnd.github+json' \
    -H 'x-github-api-version: 2022-11-28' "$@" \
    "https://api.github.com${path}" 2>/dev/null || echo 000
}

if curl -fsS -m 5 "$base/health" >/dev/null 2>&1; then
  pass "the broker answers on its health route"
else
  flunk "the broker answers on its health route"
  exit 1
fi

repos=$(curl -fsS -m 5 "$base/health" | jq -r '.repos[]?' | head -1)
if [[ -n "$repos" ]]; then
  pass "the broker names an allowlisted repository ($repos)"
else
  flunk "the broker names an allowlisted repository"
  exit 1
fi

code=$(curl -sS -m 10 -o /dev/null -w '%{http_code}' -X POST -H 'content-type: application/json' \
  -d "{\"task\":\"$task\",\"repo\":\"not-in-allowlist\"}" "$base/token" 2>/dev/null || echo 000)
if [[ "$code" == 403 ]]; then
  pass "a repository outside the allowlist is refused before minting"
else
  flunk "a repository outside the allowlist is refused before minting (got ${code})"
fi

mint=$(curl -sS -m 20 -X POST -H 'content-type: application/json' \
  -d "{\"task\":\"$task\",\"repo\":\"$repos\"}" "$base/token" 2>/dev/null)
token=$(printf '%s' "$mint" | jq -r '.token // empty')
if [[ -n "$token" ]]; then
  pass "the broker mints a token for $repos"
else
  flunk "the broker mints a token for $repos"
  exit 1
fi

full=$(curl -sS -m 20 -H "Authorization: token $token" \
  https://api.github.com/installation/repositories 2>/dev/null | jq -r '.repositories[0].full_name // empty')
if [[ -n "$full" ]]; then
  pass "the token sees its own repository ($full)"
else
  flunk "the token sees its own repository"
  exit 1
fi

body=$(mktemp)

code=$(gh_api GET "/repos/${full}" "$body")
if [[ "$code" == 200 ]]; then
  pass "the token reads its own repository"
else
  flunk "the token reads its own repository (got ${code})"
fi
default_branch=$(jq -r '.default_branch // empty' "$body")
if [[ -n "$default_branch" ]]; then
  pass "the gate knows the default branch (${default_branch})"
else
  flunk "the gate knows the default branch"
fi

# The ruleset is the human-merge backstop: without it a `contents: write` token can
# push the default branch and merge its own pull request. Assert the rule is in
# place before testing the push, so a missing ruleset reads as its own failure
# instead of looking like a token that happened to be refused.
code=$(gh_api GET "/repos/${full}/rules/branches/${default_branch}" "$body")
have_rules=$(jq -r 'if type == "array" then ([.[] | .type] | join(",")) else "" end' "$body")
if [[ "$code" == 200 ]] && printf '%s' "$have_rules" | grep -q 'pull_request'; then
  pass "the default branch requires a pull request (${have_rules})"
else
  flunk "the default branch requires a pull request (got '${have_rules}')"
  printf '      apply the bundled ruleset from %s to %s\n' "${ruleset:-verify/gates/broker-ruleset.json}" "$full" >&2
fi

code=$(gh_api GET "/repos/${full}/git/ref/heads/${default_branch}" "$body")
main_sha=$(jq -r '.object.sha // empty' "$body")

# A real file is the honest write probe: GitHub validates a request body before it
# authorizes, so an invalid body says nothing about permission.
probe_path="crew-gate-probe-$$-$(date +%s).txt"
probe_content=$(printf 'crew gate %s' "$task" | base64 | tr -d '\n')

# with_branch <branch> -> the contents payload, so both probes share one shape.
with_branch() {
  printf '{"message":"crew gate probe","content":"%s","branch":"%s"}' "$probe_content" "$1"
}

# 1. The token may write an agent branch. This is the work it is minted for.
agent_branch="agent/gate-${task}"
created=0
if [[ -n "$main_sha" ]]; then
  code=$(gh_api POST "/repos/${full}/git/refs" "$body" \
    -H 'content-type: application/json' \
    -d "{\"ref\":\"refs/heads/${agent_branch}\",\"sha\":\"${main_sha}\"}")
  if [[ "$code" == 201 ]]; then
    created=1
    pass "the token creates an agent branch (${agent_branch})"
  else
    flunk "the token creates an agent branch (got ${code}: $(jq -r '.message // ""' "$body"))"
  fi
else
  flunk "the token creates an agent branch (no ${default_branch} head to branch from)"
fi

if [[ "$created" -eq 1 ]]; then
  code=$(gh_api PUT "/repos/${full}/contents/${probe_path}" "$body" \
    -H 'content-type: application/json' -d "$(with_branch "$agent_branch")")
  if [[ "$code" == 200 || "$code" == 201 ]]; then
    pass "the token may write an agent branch"
  else
    flunk "the token may write an agent branch (got ${code}: $(jq -r '.message // ""' "$body"))"
  fi
fi

# 2. The same token is refused the default branch. This is the backstop.
code=$(gh_api PUT "/repos/${full}/contents/${probe_path}" "$body" \
  -H 'content-type: application/json' -d "$(with_branch "$default_branch")")
case "$code" in
  403 | 409 | 422)
    pass "the default branch refuses the token (${code})"
    ;;
  200 | 201)
    flunk "the default branch refuses the token (it wrote ${default_branch}; the App is on the ruleset bypass list)"
    # The write landed, so remove it before anything else looks at the branch.
    sha=$(jq -r '.content.sha // empty' "$body")
    if [[ -n "$sha" ]]; then
      gh_api DELETE "/repos/${full}/contents/${probe_path}" "$body" \
        -H 'content-type: application/json' \
        -d "$(printf '{"message":"crew gate probe cleanup","sha":"%s","branch":"%s"}' "$sha" "$default_branch")" \
        >/dev/null
    fi
    ;;
  *)
    flunk "the default branch refuses the token (got ${code}: $(jq -r '.message // ""' "$body"))"
    ;;
esac

# 3. The token is refused a different repository entirely.
other=${CREW_BROKER_OTHER_REPO:-}
if [[ "$other" == "$full" ]]; then
  flunk "the gate has a different repository to test"
else
  code=$(gh_api PUT "/repos/${other}/contents/${probe_path}" "$body" \
    -H 'content-type: application/json' \
    -d "$(printf '{"message":"crew gate probe","content":"%s"}' "$probe_content")")
  if [[ "$code" == 403 || "$code" == 404 || "$code" == 401 ]]; then
    pass "GitHub refuses the token a write on another repository (${code})"
  elif [[ "$code" == 200 || "$code" == 201 ]]; then
    flunk "GitHub refuses the token a write on another repository (it created a file)"
    sha=$(jq -r '.content.sha // empty' "$body")
    if [[ -n "$sha" ]]; then
      gh_api DELETE "/repos/${other}/contents/${probe_path}" "$body" \
        -H 'content-type: application/json' \
        -d "$(printf '{"message":"crew gate probe cleanup","sha":"%s"}' "$sha")" >/dev/null
    fi
  else
    flunk "GitHub refuses the token a write on another repository (got ${code})"
  fi
fi

# 4. The gate cleans up after itself, before the token is revoked.
if [[ -n "$agent_branch" ]]; then
  code=$(gh_api DELETE "/repos/${full}/git/refs/heads/${agent_branch}" "$body")
  if [[ "$code" == 204 ]]; then
    pass "the gate deletes its agent branch"
    agent_branch=""
  else
    flunk "the gate deletes its agent branch (got ${code}); remove ${agent_branch} by hand"
  fi
fi

# 5. Revoking makes the token unusable.
code=$(curl -sS -m 20 -o /dev/null -w '%{http_code}' -X DELETE "$base/token/$task" 2>/dev/null || echo 000)
if [[ "$code" == 204 ]]; then
  pass "the broker revokes the token"
else
  flunk "the broker revokes the token (got ${code})"
fi

code=$(curl -sS -m 20 -o /dev/null -w '%{http_code}' -H "Authorization: token $token" \
  "https://api.github.com/repos/${full}" 2>/dev/null || echo 000)
if [[ "$code" == 401 ]]; then
  pass "GitHub refuses the revoked token"
else
  flunk "GitHub refuses the revoked token (got ${code})"
fi
token=""

rm -f "$body"
exit "$fail"
