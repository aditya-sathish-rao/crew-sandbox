# `nix/packages/`

The host package set: the CLI binary and the host-side helpers it needs.

Packages here are built for the host system and run as the operator. They are not guest
packages — the guest's toolchain is provisioned inside the VM, not from this flake.
