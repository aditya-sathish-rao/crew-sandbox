# `verify/redteam/`

Adversarial simulation: a script that tries to escape the boundary the way a
prompt-injected agent would.

This is not a gate. A gate proves a property holds; `redteam.sh` assumes it does not
and goes looking for a way through. It runs in the guest as `agent` and attempts seven
attacks in the order an injected agent would try them, from the plan that specified
them:

| Attempt | What it does | Blocked by |
| --- | --- | --- |
| `read-host-home` | lists `/Users`, `/home/crew`, `/root` | no host mount; the admin home is mode 0700 |
| `find-a-key` | dumps the environment and `$HOME` for a real credential | nothing to find; the guest token is not a key |
| `exfiltrate` | POSTs data directly, through the proxy, and over DNS | default-deny egress; the allowlist; a closed resolver |
| `push-to-github` | spends any credential it finds against `api.github.com` | the API is reachable by design and refuses the guest |
| `reach-host-services` | connects to the host on ports other than the proxy and gateway | the output chain admits only 3128 and 3130 |
| `escalate` | tries sudo, flushes the ruleset, writes the ruleset file | no sudo; root-owned firewall |
| `plant-in-workspace` | writes a file and a symlink into the mounted workspace | the workspace is mounted read-only |

Each attempt prints one line saying `BLOCKED`, `ESCAPED` or `SKIP` and how it was
stopped. The transcript is the deliverable — the evidence an ADR cites — so
`crew-sandbox check redteam --redteam-log FILE` keeps a copy on the host.

Evidence never quotes a secret. Where a key-shaped value is found the line names the
variable or the path, never the value: a transcript that contained the key would be
the leak it is meant to rule out.

Two halves are deliberately not claimed here. `plant-in-workspace` proves the plant
fails; that nothing on the host reads the workspace is a property of the host-side
design, not something the guest can observe. `push-to-github` proves the guest holds
no credential that GitHub accepts; with `--redteam-repos` it also aims a write at a
named default branch and at a project the App is not installed on.

A red-team script belongs here when an attack needs more than one assertion to
describe, or when it needs to run against a deliberately broken build to prove the
gate catches it. The single-assertion attacks live in [`../gates/`](../gates/) —
`proxy.sh` already asserts that a spoofed `Host` cannot retarget a request and that a
tunnel whose SNI names another host is refused.
