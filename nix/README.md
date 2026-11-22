# `nix/`

The flake's Nix surface. Two halves, split by where the code runs.

- `modules/` — Nix modules exported to consumers, including `darwinModules.default`.
- `packages/` — the host package set the CLI and the darwin module depend on.

Nothing in this directory is guest-side. The guest's own configuration is a separate
home-manager module, and it is not exported to consumers.
