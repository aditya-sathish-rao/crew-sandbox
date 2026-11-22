# `verify/`

The acceptance criteria. Each script takes a phase and runs named assertions, one line
per assertion, exiting non-zero on any failure. Phases add assertions; none are removed.

- `gates/` — one script per invariant in [SECURITY.md](../SECURITY.md).
- `redteam/` — adversarial simulations that try to break those invariants.

`crew-sandbox check <phase>` runs them. The scripts are embedded in the operator binary,
so the gates that run are the gates that were built; this directory is where they are
written and reviewed, not where they are read from at run time.

A gate is a test of the boundary, not of the code that implements it. It asks the guest
to do the forbidden thing and requires it to fail, so a change made by hand on a running
VM is caught. The phases are `guest`, `root`, `toolchain`, `proxy`, `model` and
`broker`; `all` runs them in one process, which is where failures that only appear in
combination show up.
