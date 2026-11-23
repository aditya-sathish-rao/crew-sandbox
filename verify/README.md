# `verify/`

The acceptance criteria. Each script takes a phase and runs named assertions, one line
per assertion, exiting non-zero on any failure. Phases add assertions; none are removed.

- `gates/` — one script per invariant in [SECURITY.md](../SECURITY.md).
- `redteam/` — adversarial simulations that try to break those invariants.

`crew-sandbox check <phase>` runs them. The scripts are embedded in the operator binary,
so the gates that run are the gates that were built; this directory is where they are
written and reviewed, not where they are read from at run time.

`nix flake check` does not run them. A Nix build has no VM and no network, which is most
of what a gate needs; it covers the unit tests (including `node --check` on both `.mjs`),
a prettier check, and the two `--selftest` runs instead.

The gates run on the operator's own machine rather than in CI; the flake checks and the
package build are in CI ([`../.github/workflows/ci.yml`](../.github/workflows/ci.yml)).
No hosted runner can nest the
guest — GitHub's arm64 macOS runners cannot nest a Virtualization.framework VM, and the
guest is arm64 — and the boundary they measure is that machine's. The README's
"Where the gates run" has the full argument.

A gate is a test of the boundary, not of the code that implements it. It asks the guest
to do the forbidden thing and requires it to fail, so a change made by hand on a running
VM is caught. The phases are `guest`, `root`, `toolchain`, `proxy`, `model`, `broker`
and `redteam`; `all` runs them in one process, which is where failures that only appear
in combination show up.

`redteam` is the odd one out: an attacker's script rather than a checklist. It reports
`BLOCKED`, `ESCAPED` or `SKIP` per attempt and keeps a transcript, which is the part an
ADR can cite.
