# `examples/`

Minimal consumer flakes showing how to import this repo.

Orchestrator wiring lives here and nowhere else. Core logic stays tool-agnostic; an
example is how a specific tool adapts to the contract, not how the contract adapts to a
tool.

## `consumer/`

A standalone flake that imports both halves of this repository:

| File | What it is |
| --- | --- |
| [`consumer/flake.nix`](consumer/flake.nix) | The inputs and the two outputs: `homeConfigurations."agent@aarch64-linux"` for the guest, `darwinConfigurations."mini"` for the Mac. |
| [`consumer/home.nix`](consumer/home.nix) | The fleet, passed to the guest module as `extraPackages`. |
| [`consumer/darwin.nix`](consumer/darwin.nix) | The host services: the provider upstream, the vault secret name and the App ID. |

It is the template for the cutover: `agentic-dev-env` replaces the fleet in `home.nix`
and the provider and App ID in `darwin.nix`, and changes nothing else.

`nix/checks/examples.nix` evaluates both halves against this flake's own modules and
asserts that the example's values reach the settled configuration, so a renamed option
fails `nix flake check` instead of failing in someone's dotfiles. The example is
evaluated, not built: it names no VM and no secrets.
