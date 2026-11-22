# `nix/guest/`

The guest's home-manager configuration, exported as `homeManagerModules.default`.

It runs inside the VM as `agent`, so it may assume nothing about the host and may need no
root. `extraPackages` is the whole fleet: the module installs no tools of its own, so a
consumer that passes nothing gets a shell and nothing to run.

`username` and `homeDirectory` are fixed to `agent` and `/home/agent` because `lima.yaml`
creates exactly that account; they are not options. Anything else fails activation.

`models.json` and `settings.json` are pi's. They point the harness at the host's model
gateway, so the model key never enters the guest's configuration or environment. Their
provider and model names match what `agentic-dev-env`'s gateway serves; a consumer fronting a
different provider overrides both files.

`nix/checks/guest.nix` evaluates this module as `agent@aarch64-linux` and asserts the
properties above, so a misspelled option fails `nix flake check` instead of a VM boot.
