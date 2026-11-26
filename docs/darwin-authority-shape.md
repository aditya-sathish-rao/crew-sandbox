# The host authority: daemon, user agent, or on demand?

**Status:** decision, 2026-11-23. The shape is chosen and shipped; one claim in
[`darwin-module.md`](darwin-module.md) is stated more strongly than the measurement behind it.

Step 6 of the migration plan asked one question before writing any host-side code: can the
vault be read with nobody at the keyboard? The answer decided whether the authority should be
a system daemon, a user agent, or something the CLI starts on demand. A user agent already
ships, in [`nix/modules/darwin.nix`](../nix/modules/darwin.nix). This record is the evidence
for that call, and for where the three options should be split apart rather than ranked.

## The premise is narrower than the doc claims

The measurement that justified the user agent ran a job in `gui/$(id -u)` — the user domain —
not in `system/`. It proves that a **TTY-less user-domain job** can read the vault once someone
approves. It does not test a root `LaunchDaemon`. See
[the evidence gap](#the-evidence-gap-worth-closing).

## Lifetime, not privilege, is the deciding axis

All three shapes can hold the key. They differ in _when the process exists_ relative to login,
and therefore in who is available to answer the vault.

```text
               boot      login                                  logout
                 │          │                                      │
A · daemon       ████████████████████████████████████████████████  no approver
B · agent                   ██████████████████████████████████████  boundary closed
C · on demand                        ██████████  ends with the task; guest may outlive it
```

Guest lifetime is itself login-bound: `limactl` runs as the operator, so the VM and the user
agent share a window. On-demand is the only shape that can end _before_ the guest does — and
that gap has consequences below.

## The three options

### A · System daemon

`launchd.daemons`, root, boots before any login.

- **Unattended boot** — works, and is the only shape that survives a power cut.
- **The key** — no session, no approver. `av inject` answers "human approval required" and
  the service comes up keyless.
- **Silent failure** — a keyless proxy does not exit: it logs `model gateway disabled` and
  keeps serving egress. The boundary looks up while the gateway is off.
- **Evidence** — inferred. Never run as root.

`nix/modules/darwin.nix:7-30` · `cmd/crew-sandbox/main.go:396-402`

### B · User LaunchAgent

`launchd.user.agents`, as the operator, inside the GUI session.

- **Unattended boot** — not supported. Services start at login; nobody logs in, nothing runs.
- **The key** — the prompt is answerable, and a Temporary Access Grant can bind to the job.
- **Failure direction** — closed, not open: no proxy means the guest has no egress at all.
- **Evidence** — measured, and evaluated in CI.

`nix/modules/darwin.nix:90-100` · `nix/checks/darwin.nix` · `darwin-module.md` §The result

### C · CLI starts it on demand

The supervisor is a child of the shell that asked for work.

- **Unattended boot** — never; there is no shell at boot.
- **The key** — best approval story of the three, interactive by construction.
- **Revocation** — tokens live in the broker's memory (`issued`), so a process that exits
  leaves `down` with nothing to revoke.
- **Evidence** — measured as a gate-only pattern.

`internal/broker/broker.mjs:177,266` · `cmd/crew-sandbox/main.go:251-256` ·
`verify/gates/check.sh:379-395`

## Scorecard

Ordered by how much each criterion constrains the answer. The last row is what makes C unusable
as a _standing_ service.

| Criterion                         | A · daemon | B · user agent | C · on demand     |
| --------------------------------- | ---------- | -------------- | ----------------- |
| Can start with no human present   | no key     | no             | no                |
| Key read has an approver          | nobody     | desktop        | terminal          |
| Covers the guest's whole life     | yes        | yes            | not by design     |
| Failure is closed, not open       | **open**   | closed         | silent timeout    |
| `down` can revoke what was minted | yes        | yes            | **registry lost** |
| Already implemented and checked   | no         | yes            | gate only         |

## The two facts that decide it

### 1. The gateway fails quiet, the broker fails loud

If the vault returns nothing, the broker refuses to start — it exits with status 2 and the log
says why. The proxy does the opposite: it prints one line and runs on without a model gateway,
still serving egress. A daemon that loses the key at boot therefore produces the worst pair: a
boundary that reports healthy while the model is off.

`internal/broker/broker.mjs:22-27` · `internal/proxy/proxy.mjs:47,235` ·
`cmd/crew-sandbox/main.go:399`

### 2. Revocation is custody of a process, not of a host

A minted token is remembered only in the broker's in-memory map. Restarting the broker strands
every token it issued — a safe failure, until `crew-sandbox down` is the thing that needs to
revoke. It calls the running broker and reports "no token broker is running; no tokens to
revoke" when there is none. An on-demand broker that exits between requests therefore breaks
the one lifecycle guarantee the security model leans on.

`internal/broker/broker.mjs:177,266` · `cmd/crew-sandbox/main.go:253-254` · `SECURITY.md` §3.5

## The evidence gap worth closing

The doc's conclusion is sound, but it is one inference longer than its evidence.

**What was run.** A job bootstrapped into `gui/$(id -u)` with `RunAtLoad`, `ppid 1`, no TTY,
against several secrets including a multiline PEM. Every plain `inject` returned the value and
was recorded `approvalSource: Manual`, "Approved in prompt".

**What it supports.** "A user-domain job with no terminal can read the vault, once a human
approves." That is exactly enough for B, and it is the part the shipped module depends on.

**What it does not support.** That a root daemon in `system/` fails. That is a different
domain, a different user and a different vault. One run there closes the question either way.

**Suggested edit.** In [`darwin-module.md`](darwin-module.md), under "The result",
change "A `LaunchDaemon` cannot hold the model key" from a measured finding into the
narrower, provable statement: a daemon cannot obtain the key _without an operator-arranged
policy_. Then either run the `system/` probe or cite the policy gap as the reason the option
is out.

## Recommendation: split the question by lifetime

The three options are not three answers to one question. The proxy and the broker have
different reasons to stay up, and those reasons point the same way.

- **Proxy → user LaunchAgent (unchanged).** It must be up for the guest's entire life, and the
  guest is login-bound anyway. Keeping it as an agent means the key read always has a desktop
  to land on, and the failure direction stays closed.
- **Broker → user LaunchAgent, not on demand.** Not because of privilege but because of
  custody: its memory-only token map is what `down` revokes against. A process per task cannot
  hold that map across tasks.
- **On demand → the right shape for gates and one-off runs.** Which is what
  `verify/gates/check.sh` already does: start by port if free, reap by port on exit. That
  pattern is correct there and should stay there.
- **The real lever is policy, not domain.** What would make a boot-time read possible is
  `av bless --endorse-launcher` against the wrapper nix already generates, or a Temporary
  Access Grant bound to it. That belongs to the operator, and it is the only thing that turns
  "daemon" from unchosen into choosable.

## Open follow-ups

- **F1** — Qualify the daemon claim in [`darwin-module.md`](darwin-module.md): state it as a
  policy gap, not a measured failure.
- **F2** — Make a keyless proxy fail loudly instead of degrading, so a keyless start is
  distinguishable from a healthy one.
- **F3** — Persist the broker's issued-token registry, so revocation is not tied to one
  process's lifetime.
- **F4** — Extend [`nix/checks/darwin.nix`](../nix/checks/darwin.nix) to assert the LaunchAgent
  is the _only_ shape registered, with the reasoning recorded next to it.
- **F5** — Record the lifetime argument as an ADR, next to the measurement it rests on.

## Sources read for this record

`nix/modules/darwin.nix` · `nix/checks/darwin.nix` · `docs/darwin-module.md` ·
`docs/archive/crew-sandbox-plan.md` (§Shape B, §Phase 4) · `cmd/crew-sandbox/main.go` ·
`internal/proxy/{proxy.go,proxy.mjs}` · `internal/broker/{broker.go,broker.mjs}` ·
`internal/vault/vault.go` · `internal/lima/lima.yaml` · `verify/gates/check.sh` ·
`SECURITY.md` §3.5 · `docs/archive/crew-sandbox-review.md` (C1, H4)
