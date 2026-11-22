# `nix/`

The flake's Nix surface. Three parts, split by where the code runs.

- `modules/` — Nix modules exported to consumers, including `darwinModules.default`.
- `packages/` — the host package set the CLI and the darwin module depend on.
- `guest/` — the guest's home-manager configuration, exported as `homeManagerModules.default`.

`guest/` is the only guest-side code here. It runs inside the VM as `agent`, so nothing in
it may need root, and it reaches the host only through the egress proxy and the model
gateway that `lima.yaml` provisions.
