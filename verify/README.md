# `verify/`

The acceptance criteria. Each script takes a phase and runs named assertions, one line
per assertion, exiting non-zero on any failure. Phases add assertions; none are removed.

- `gates/` — one script per invariant in [SECURITY.md](../SECURITY.md).
- `redteam/` — adversarial simulations that try to break those invariants.

These are wired into `nix flake check`, so a passing gate is the definition of "the
sandbox works", not a suggestion.
