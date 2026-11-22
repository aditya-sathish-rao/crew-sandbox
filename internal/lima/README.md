# `internal/lima`

The VM definition and the `limactl` wrapper.

`lima.yaml` is embedded in the binary. It declares `mounts: []` and an ignore rule for
every port forward, so a guest created from it sees nothing of the host and publishes
nothing back. A workspace mount is applied per run as a `limactl --set` override, which
keeps the embedded file the single source of truth for everything else.

The tests assert those two properties directly: if someone adds a mount or removes the
port-forward ignore rule from the embedded definition, the unit tests fail.
