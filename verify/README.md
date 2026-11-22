# `verify/`

The acceptance criteria. Each script takes a phase and runs named assertions, one line
per assertion, exiting non-zero on any failure. Phases add assertions; none are removed.

- `gates/` — one script per invariant in [SECURITY.md](../SECURITY.md).
- `redteam/` — adversarial simulations that try to break those invariants.

The intent is to wire these into `nix flake check`, so a passing gate is the definition
of "the sandbox works" rather than a suggestion. They are not ported yet: `nix flake
check` currently runs only the Go unit tests, and `crew-sandbox check` exits with an
error saying the gates are missing.
