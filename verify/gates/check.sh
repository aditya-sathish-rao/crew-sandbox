# shellcheck shell=bash
# The phase gates. Each phase is one command that proves the phase's boundary
# still holds on the guest as it is right now, not as it was built.
#
# This file is the driver. The scripts beside it are the assertions: a phase that
# needs a particular user has its script staged somewhere only that user can reach,
# and everything that can only be observed from the host — the loopback, the
# lifetime of a service holding a key — is asserted here rather than in the guest.
set -euo pipefail

# The operator binary names itself, because this file starts the proxy and the
# broker and those are the two processes that hold credentials.
self=${CREW_SELF:-crew-sandbox}
name=${CREW_NAME:-crew-sandbox}
self_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

# Policy and pins the gate has to be told. None of it is a property of this repo:
# the fleet is the consumer's, the allowlists are the operator's, and the model
# provider is a setting.
egress_allow=${CREW_EGRESS_ALLOW:-}
repos_allow=${CREW_REPOS_ALLOW:-}
model_upstream=${CREW_MODEL_UPSTREAM:-}
model_key_env=${CREW_MODEL_KEY_ENV:-CREW_MODEL_KEY}
# The workspace is the one mount the guest is allowed to have, and it is applied
# when the VM is created, so the gate has to be told whether one was asked for.
workspace=${CREW_WORKSPACE:-}
writable=${CREW_WRITABLE:-false}
expected_tools=${CREW_EXPECTED_TOOLS:-}
# The adversarial transcript, and the repositories its push attempt aims at.
redteam_log=${CREW_REDTEAM_LOG:-}
redteam_repos=${CREW_REDTEAM_REPOS:-}
app_id=${CREW_APP_ID:-}
other_repo=${CREW_BROKER_OTHER_REPO:-}
# Not 3129: the gate controls the key the broker runs with, so it must not share a
# listener with a broker the operator already has running.
broker_port=${CREW_BROKER_PORT:-3139}
# The model gateway listens on its own port. The gate controls the key, so it must
# not share the egress proxy's socket or a gateway the operator already has running.
model_port=${CREW_MODEL_PORT:-3130}

# The gate directory. Owned by root, group-writable by the Lima admin account so
# `limactl copy` can stage into it, and not writable by `agent`. Copying into
# world-writable /tmp let the guest pre-own the path root later executes; this
# makes that impossible rather than merely awkward. `agent` is not in `crew`, so
# the group bit buys it nothing.
gate_dir=/run/crew-gate

usage() {
  cat >&2 <<'EOF'
usage: crew-sandbox check <phase>

  guest      the boundary itself: no host mount, no sudo, default-deny egress
  root       what only root can see: the ruleset, sudoers, the gate directory
  toolchain  the guest's tools, resolving from the same store the host pins
  proxy      the egress allowlist, through a keyless proxy
  model      the model key stays on the host, through a keyed proxy
  broker     one repo, one token, revocable, and the default branch refuses it
  redteam    the adversary: seven ways out, all of which must fail
  all        every phase above, in one run
EOF
}

phase=${1:-guest}
case "$phase" in
  guest | root | toolchain | proxy | model | broker | redteam | all) ;;
  -h | --help)
    usage
    exit 0
    ;;
  *)
    echo "crew-sandbox check: no gate for '$phase'" >&2
    usage >&2
    exit 2
    ;;
esac

status=0
# Set by the flunk calls that print their own lines, including the host-side
# assertions whose subject is the host's state rather than a script in the guest.
fail=0
# Whether this run created the gate directory, so cleanup removes only what it made.
staged=0
# The listener probe's PID file inside the guest, and whether one is running.
probe_pid_file=/home/agent/.crew-forward-probe.pid
probe_started=0
service_pid=""
# Every port this run started a service on. A phase can start more than one
# service (the model phase starts a keyed proxy and then runs the proxy gate
# through it), and a later phase overwrites service_pid, so cleanup needs the full
# set or it orphans the earlier listener and the next run fails on "port already
# in use".
service_ports=()
key_hash_file=""

# require_host_tools <tool...>: the gate runs on the host, so a missing tool is a
# broken environment, not a failed boundary. Say which one rather than letting the
# assertion fail for the wrong reason.
require_host_tools() {
  local tool
  for tool in "$@"; do
    if ! command -v "$tool" >/dev/null 2>&1; then
      echo "crew-sandbox check: $tool is not on PATH; the gate cannot run without it" >&2
      return 1
    fi
  done
}

# stop_service: kill the service this gate started and wait for it to be gone.
# Signalling is not enough. A child that inherited the caller's stdout keeps that
# pipe open after we exit, so `crew-sandbox check | tail` hangs forever even
# though the gate finished. We redirect the service's stdio below and reap it here.
#
# The CLI is a wrapper: it forks the real process, so the PID we track is not the
# one holding the port. Killing only that leaves an orphaned listener that makes
# the next phase fail with "port 3128 is already in use". The port is the identity
# that matters, so the listener is stopped by port as well.
# shellcheck disable=SC2329  # reached via cleanup, which the EXIT trap invokes
stop_service() {
  local port=${1:-}
  if [[ -n "$service_pid" ]]; then
    kill "$service_pid" 2>/dev/null || true
    local _i
    for _i in {1..25}; do
      kill -0 "$service_pid" 2>/dev/null || break
      sleep 0.1
    done
    kill -9 "$service_pid" 2>/dev/null || true
    wait "$service_pid" 2>/dev/null || true
    service_pid=""
  fi
  [[ -n "$port" ]] || return 0
  local _pid
  for _pid in $(lsof -nP -iTCP:"$port" -sTCP:LISTEN -t 2>/dev/null); do
    kill "$_pid" 2>/dev/null || true
  done
  local _i
  for _i in {1..25}; do
    lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1 || return 0
    sleep 0.1
  done
  for _pid in $(lsof -nP -iTCP:"$port" -sTCP:LISTEN -t 2>/dev/null); do
    kill -9 "$_pid" 2>/dev/null || true
  done
}

# shellcheck disable=SC2329  # invoked by the EXIT trap below
cleanup() {
  local _p
  for _p in "${service_ports[@]}"; do stop_service "$_p"; done
  if [[ -n "$key_hash_file" ]]; then rm -f "$key_hash_file"; fi
  # A probe cut off before its own cleanup must not leave a listener for the next
  # run to trip over.
  if [[ "$probe_started" -eq 1 ]]; then stop_probe_listener; fi
  # The gate leaves no root-owned directory behind, and no staged script for the
  # next run to execute.
  if [[ "$staged" -eq 1 ]]; then
    limactl shell "$name" -- sudo -n rm -rf "$gate_dir" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT
# A gate cut off with Ctrl-C, or one whose output is piped into a reader that exits
# first (`check proxy | head`), must still reap the service holding the key. The
# signal traps exit through the EXIT trap above rather than duplicating cleanup.
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 141' PIPE

# ensure_gate_dir: create the root-owned staging directory. /run is tmpfs, so it
# is recreated per run rather than provisioned once. 0775 with the admin group:
# root owns it, `limactl copy` (which runs as the admin account) can stage into
# it, and `agent` is not in that group, so it cannot write there at all.
ensure_gate_dir() {
  if ! limactl shell "$name" -- sudo -n install -d -o root -g crew -m 0775 "$gate_dir" >/dev/null 2>&1; then
    printf 'FAIL  the gate could not create %s\n' "$gate_dir"
    printf '      the guest may be interfering with the gate\n' >&2
    return 1
  fi
  staged=1
}

# stage <script> <guest-name>: put a check script where the user that runs it can
# read it. A copy failure is tampering, not infrastructure: the only way it fails
# is that something in the guest owns the path, and that has to read as a named
# failure. The file is installed root-owned, so the account that staged it cannot
# rewrite what root is about to execute.
stage() {
  local src=$1 dest=$2 err
  if [[ ! -f "$src" ]]; then
    printf 'FAIL  the gate has no %s to stage\n' "$(basename "$src")"
    printf '      the binary is missing one of its gates\n' >&2
    return 1
  fi
  ensure_gate_dir || return 1
  if ! err=$(limactl copy "$src" "$name:${gate_dir}/.${dest}.new" 2>&1); then
    printf 'FAIL  the gate could not stage %s\n' "$dest"
    printf '      %s\n' "$err" >&2
    printf '      the guest may be interfering with the gate\n' >&2
    return 1
  fi
  if ! limactl shell "$name" -- sudo -n install -o root -g root -m 0755 \
    "${gate_dir}/.${dest}.new" "${gate_dir}/${dest}" >/dev/null 2>&1; then
    printf 'FAIL  the gate could not install %s as root\n' "$dest"
    return 1
  fi
  limactl shell "$name" -- sudo -n rm -f "${gate_dir}/.${dest}.new" >/dev/null 2>&1 || true
}

require_vm() {
  if [[ "$(limactl list "$name" --format '{{.Status}}')" != Running ]]; then
    echo "crew-sandbox check: the guest is not running; start it with 'crew-sandbox up'." >&2
    return 1
  fi
}

pass() { printf 'PASS  %s\n' "$1"; }
flunk() {
  printf 'FAIL  %s\n' "$1"
  fail=1
}

# free_host_port: the first port in a short range nothing on the host is using.
# The probe publishes a guest listener on the host loopback if the boundary is
# broken, so the port has to be free here or the test would prove nothing.
free_host_port() {
  local p
  for p in 18080 18081 18082 18083 18084 18085 18086 18087; do
    if ! lsof -nP -iTCP:"$p" -sTCP:LISTEN >/dev/null 2>&1; then
      printf '%s\n' "$p"
      return 0
    fi
  done
  return 1
}

# stop_probe_listener: leave no listener behind, on every path including a failure.
# shellcheck disable=SC2329  # reached via cleanup, which the EXIT trap invokes
stop_probe_listener() {
  [[ "$probe_started" -eq 1 ]] || return 0
  limactl shell "$name" -- sudo -n -u agent -H bash -c \
    "kill \$(cat ${probe_pid_file}) 2>/dev/null; rm -f ${probe_pid_file}" >/dev/null 2>&1 || true
  probe_started=0
}

# probe_no_forward <bind-address> <label>: start a listener inside the guest as
# `agent` and require the host's loopback to stay empty.
#
# Lima's host agent appends a fallback rule that publishes every guest listener on
# the host's own 127.0.0.1, on the same port, for all ports and both protocols, and
# `portForwards: []` does not remove it — the instance definition needs the `ignore`
# rule. The forward rides the guest agent rather than the network, so the guest's
# input chain never sees the connection: whatever runs in the guest could put a
# service on the operator's loopback with one command. Only the host can observe
# that, so this assertion lives here and not in guest.sh.
#
# The listener is `nc`, not a web server: the guest's resolver is closed on purpose
# and Python's http.server resolves its own bind address at startup, so it comes up
# seconds after a gate would have given up on it.
probe_no_forward() {
  local bind=$1 label=$2 port _i
  if ! port=$(free_host_port); then
    flunk "$label (no free host port to test with)"
    return
  fi
  probe_started=1
  limactl shell "$name" -- sudo -n -u agent -H bash -c \
    "nohup timeout 300 nc -l -k -s ${bind} -p ${port} >/dev/null 2>&1 &
     echo \$! > ${probe_pid_file}" >/dev/null 2>&1 || true
  # If the listener is not up, the host probe would fail for the wrong reason and
  # report a boundary that was never exercised.
  local up=0
  for _i in {1..25}; do
    if limactl shell "$name" -- sudo -n -u agent -H bash -c \
      "nc -z -w 1 127.0.0.1 ${port}" >/dev/null 2>&1; then
      up=1
      break
    fi
    sleep 0.2
  done
  if [[ "$up" -ne 1 ]]; then
    flunk "$label (could not start a listener inside the guest)"
    stop_probe_listener
    return
  fi
  # Watch for a few seconds rather than sampling once: the guest agent reports
  # listening ports on its own interval, so a single early look could miss the
  # forward and pass a boundary that does not exist.
  local forwarded=0
  for _i in {1..20}; do
    if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1 ||
      nc -z -w 1 127.0.0.1 "$port" >/dev/null 2>&1; then
      forwarded=1
      break
    fi
    sleep 0.2
  done
  if [[ "$forwarded" -eq 1 ]]; then
    flunk "$label (the guest published 127.0.0.1:${port} on the host)"
  else
    pass "$label"
  fi
  stop_probe_listener
}

# proxy_args: the proxy invocation for this gate. The allowlist is whatever the
# operator configured; with none, the proxy falls back to the embedded example.
proxy_args=()
build_proxy_args() {
  proxy_args=(proxy)
  if [[ -n "$egress_allow" ]]; then proxy_args+=(--egress-allow "$egress_allow"); fi
}

# proxy_selftest: the ClientHello parser is the only thing standing between an
# allowlisted address and any other host on the same CDN, so prove it before
# trusting a tunnel. It needs no network and no secret, so every phase that relies
# on the allowlist can afford it.
proxy_selftest() {
  build_proxy_args
  if "$self" "${proxy_args[@]}" --selftest >/dev/null 2>&1; then
    pass "the ClientHello parser reads the name a tunnel asks for"
  else
    flunk "the ClientHello parser reads the name a tunnel asks for"
    return 1
  fi
}

# wait_for_proxy <port>: block until the proxy answers a real request through
# itself, so an assertion never runs against a listener that is not ready.
wait_for_proxy() {
  local port=$1 _i
  for _i in {1..600}; do
    if curl -s -o /dev/null -m 1 -x "http://127.0.0.1:${port}" https://cache.nixos.org/nix-cache-info; then
      return 0
    fi
    sleep 0.2
  done
  return 1
}

phase_guest() {
  require_host_tools limactl curl lsof nc || return 1
  require_vm || return 1
  local rc=0
  stage "$self_dir/guest.sh" crew-guest-checks.sh || return 1
  printf '\nphase 1: the guest\n'
  limactl shell "$name" -- sudo -n -u agent -H env \
    CREW_WORKSPACE="$workspace" CREW_WRITABLE="$writable" \
    bash "${gate_dir}/crew-guest-checks.sh" || rc=1
  # The guest cannot see the host's loopback, so the ingress half of the boundary
  # is asserted from here, against a listener the gate starts as `agent`.
  probe_no_forward 0.0.0.0 "a guest listener on every address stays off the host"
  probe_no_forward 127.0.0.1 "a guest listener on loopback stays off the host"
  [[ "$fail" -eq 0 ]] || rc=1
  return "$rc"
}

phase_root() {
  require_host_tools limactl || return 1
  require_vm || return 1
  local rc=0
  stage "$self_dir/root.sh" crew-root-checks.sh || return 1
  printf '\nphase 2: root and sudo\n'
  limactl shell "$name" -- sudo -n bash "${gate_dir}/crew-root-checks.sh" || rc=1
  return "$rc"
}

phase_toolchain() {
  require_host_tools limactl || return 1
  require_vm || return 1
  local rc=0
  stage "$self_dir/toolchain.sh" crew-toolchain-checks.sh || return 1
  printf '\nphase 3: the toolchain\n'
  limactl shell "$name" -- sudo -n -u agent -H env CREW_EXPECTED_TOOLS="$expected_tools" \
    bash -l "${gate_dir}/crew-toolchain-checks.sh" || rc=1
  return "$rc"
}

# start_keyless_proxy: bring up a proxy with no model key, so a phase can test the
# allowlist through the door the guest actually has. Sets proxy_started when this run
# owns the listener; a proxy the operator already had running is left alone.
proxy_started=0
start_keyless_proxy() {
  if lsof -nP -iTCP:3128 -sTCP:LISTEN >/dev/null 2>&1; then
    echo "proxy: already listening on 127.0.0.1:3128; using it as it is"
    proxy_started=0
    return 0
  fi
  echo "proxy: starting one for the duration of this phase"
  build_proxy_args
  "$self" "${proxy_args[@]}" --port 3128 >/dev/null 2>&1 &
  service_pid=$!
  service_ports+=(3128)
  proxy_started=1
  wait_for_proxy 3128
}

phase_proxy() {
  require_host_tools limactl curl lsof || return 1
  require_vm || return 1
  proxy_selftest || return 1
  start_keyless_proxy || return 1
  local rc=0
  stage "$self_dir/proxy.sh" crew-proxy-checks.sh || return 1
  printf '\nphase 4: the egress proxy\n'
  limactl shell "$name" -- sudo -n -u agent -H bash "${gate_dir}/crew-proxy-checks.sh" || rc=1
  # Stop the keyless proxy we started, so the next phase can bind 3128 with the
  # key. A proxy the operator already had running is left alone.
  if [[ "$proxy_started" -eq 1 ]]; then stop_service 3128; fi
  return "$rc"
}

phase_model() {
  require_host_tools limactl curl lsof || return 1
  require_vm || return 1
  if [[ -z "$model_upstream" ]]; then
    echo "model: no model upstream is configured, so the gateway is disabled." >&2
    echo "model: pass --model-upstream URL; there is nothing to prove without one." >&2
    return 1
  fi
  local token
  token=$(limactl shell "$name" -- sudo -n cat /etc/crew-model-token 2>/dev/null | tr -d '[:space:]' || true)
  if [[ -z "$token" ]]; then
    echo "model: /etc/crew-model-token is missing; re-create the guest from the current lima.yaml." >&2
    return 1
  fi
  build_proxy_args
  proxy_args+=(--port 3128 --model-port "$model_port" --model-upstream "$model_upstream" --model-key-env "$model_key_env")
  if lsof -nP -iTCP:3128 -sTCP:LISTEN >/dev/null 2>&1 ||
    lsof -nP -iTCP:"$model_port" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "model: port 3128 or ${model_port} is already in use; stop that service so this gate controls the key." >&2
    return 1
  fi
  key_hash_file=$(mktemp)
  echo "model: starting the proxy with the vault key for the duration of this gate"
  echo "model: approve the vault prompt if one appears"
  "$self" "${proxy_args[@]}" --key-hash-out "$key_hash_file" >/dev/null 2>&1 &
  service_pid=$!
  service_ports+=(3128 "$model_port")
  local _i
  for _i in {1..600}; do
    if [[ -s "$key_hash_file" ]] &&
      lsof -nP -iTCP:3128 -sTCP:LISTEN >/dev/null 2>&1 &&
      lsof -nP -iTCP:"$model_port" -sTCP:LISTEN >/dev/null 2>&1; then
      break
    fi
    if ! kill -0 "$service_pid" 2>/dev/null; then
      break
    fi
    sleep 0.2
  done
  if [[ ! -s "$key_hash_file" ]]; then
    echo "model: the vault returned no $model_key_env, so there is no key to prove stays home." >&2
    return 1
  fi
  if ! lsof -nP -iTCP:3128 -sTCP:LISTEN >/dev/null 2>&1 ||
    ! lsof -nP -iTCP:"$model_port" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "model: the proxy did not start both listeners (3128, ${model_port})." >&2
    return 1
  fi
  local rc=0
  stage "$self_dir/model.sh" crew-model-checks.sh || return 1
  printf '\nphase 5: the model key\n'
  limactl shell "$name" -- sudo -n -u agent -H env \
    CREW_REAL_KEY_SHA256="$(cat "$key_hash_file")" \
    CREW_MODEL_UPSTREAM="$model_upstream" \
    bash -l "${gate_dir}/crew-model-checks.sh" || rc=1
  # The keyed proxy enforces the same allowlist as the keyless one, so the egress
  # assertions run here too. A gate that proves the key stays home while the
  # allowlist is wrong would report a boundary that does not exist.
  proxy_selftest || rc=1
  stage "$self_dir/proxy.sh" crew-proxy-checks.sh || return 1
  printf '\nphase 4: the egress proxy (through the keyed proxy)\n'
  limactl shell "$name" -- sudo -n -u agent -H bash "${gate_dir}/crew-proxy-checks.sh" || rc=1
  # Stop the keyed proxy here rather than leaving it to the EXIT trap: the broker
  # gate starts its own service and overwrites service_pid, so a later phase would
  # orphan this listener and the next run would fail on "port 3128 is already in
  # use". Each phase owns the service it started. The one process holds both
  # listeners, so stopping either port releases both.
  stop_service 3128
  stop_service "$model_port"
  return "$rc"
}

phase_broker() {
  require_host_tools curl lsof jq || return 1
  if [[ -z "$app_id" ]]; then
    echo "broker: no GitHub App ID is configured." >&2
    echo "broker: pass --app-id; it is public configuration, not a secret." >&2
    return 1
  fi
  if [[ -z "$other_repo" ]]; then
    echo "broker: no second repository is configured to test the token against." >&2
    echo "broker: pass --other-repo owner/name, one this App is installed on but the token should not reach." >&2
    return 1
  fi
  if lsof -nP -iTCP:"$broker_port" -sTCP:LISTEN >/dev/null 2>&1; then
    echo "broker: port ${broker_port} is already in use; stop that broker and rerun." >&2
    return 1
  fi
  local broker_args=(broker --port "$broker_port" --app-id "$app_id")
  if [[ -n "$repos_allow" ]]; then broker_args+=(--repos-allow "$repos_allow"); fi
  echo "broker: starting the broker with the vault key for the duration of this gate"
  echo "broker: approve the vault prompt if one appears"
  "$self" "${broker_args[@]}" >/dev/null 2>&1 &
  service_pid=$!
  service_ports+=("$broker_port")
  local _i
  for _i in {1..600}; do
    if curl -fsS -m 1 "http://127.0.0.1:${broker_port}/health" >/dev/null 2>&1; then
      break
    fi
    if ! kill -0 "$service_pid" 2>/dev/null; then
      break
    fi
    sleep 0.2
  done
  if ! curl -fsS -m 1 "http://127.0.0.1:${broker_port}/health" >/dev/null 2>&1; then
    echo "broker: the broker did not start." >&2
    return 1
  fi
  local rc=0
  printf '\nphase 6: the token broker\n'
  CREW_BROKER_PORT="$broker_port" \
    CREW_BROKER_OTHER_REPO="$other_repo" \
    CREW_BROKER_RULESET="$self_dir/broker-ruleset.json" \
    bash "$self_dir/broker.sh" || rc=1
  return "$rc"
}

# phase_redteam: hand the guest an attacker's script instead of a checklist. The
# transcript is the deliverable — the evidence an ADR cites — so --redteam-log keeps
# a copy on the host rather than leaving it to scroll past.
phase_redteam() {
  require_host_tools limactl tee || return 1
  require_vm || return 1
  local rc=0
  stage "$self_dir/../redteam/redteam.sh" crew-redteam.sh || return 1
  # The attacks are only worth anything if the guest has the door it normally has.
  # Without a proxy they would fail because nothing is listening, which says nothing
  # about the allowlist or about what the guest holds.
  start_keyless_proxy || return 1
  printf '\nphase 7: the adversary\n'
  local run=(
    limactl shell "$name" -- sudo -n -u agent -H env
    CREW_WORKSPACE="$workspace" CREW_REDTEAM_REPOS="$redteam_repos"
    bash "${gate_dir}/crew-redteam.sh"
  )
  if [[ -n "$redteam_log" ]]; then
    "${run[@]}" 2>&1 | tee "$redteam_log" || rc=1
    printf 'redteam: transcript kept at %s\n' "$redteam_log"
  else
    "${run[@]}" || rc=1
  fi
  if [[ "$proxy_started" -eq 1 ]]; then stop_service 3128; fi
  return "$rc"
}

# Each phase is named literally rather than dispatched through a variable so the
# linter can see each call, and a typo'd phase name fails here instead of in the
# guest.
#
# `all` exists because a boundary that holds in isolation can still fail in
# combination: the guest phase tears down the gate directory the later phases
# reuse, and the broker gate revokes the very token the model gate would need.
# Running them in one process is what catches that. The broker gate is a host
# service, so it runs whether or not the guest is up.
case "$phase" in
  guest) phase_guest || status=1 ;;
  root) phase_root || status=1 ;;
  toolchain) phase_toolchain || status=1 ;;
  proxy) phase_proxy || status=1 ;;
  model) phase_model || status=1 ;;
  broker) phase_broker || status=1 ;;
  redteam) phase_redteam || status=1 ;;
  all)
    phase_guest || status=1
    phase_root || status=1
    phase_toolchain || status=1
    phase_proxy || status=1
    phase_model || status=1
    phase_broker || status=1
    phase_redteam || status=1
    ;;
esac

if [[ "$status" -eq 0 ]]; then
  printf '\n%s: pass\n' "$phase"
else
  printf '\n%s: fail\n' "$phase" >&2
fi

exit "$status"
