# The guest side of a consumer: what the agent can run inside the VM.
#
# Imported by `flake.nix` in this directory and evaluated by
# `nix/checks/examples.nix`, so a change to the module's contract breaks a check
# rather than a VM boot.
#
# `extraPackages` is the whole fleet. The module installs nothing of its own, so
# this list is exactly what the agent can run — replace it with your own tools.
{ pkgs, ... }:
{
  # A real consumer passes herdr, firstmate and the rest here.
  home.packages = with pkgs; [
    git
    jq
  ];
}
