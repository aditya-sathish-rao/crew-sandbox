# `internal/lima`

The VM definition and the `limactl` wrapper.

`lima.yaml` is embedded in the binary. It declares `mounts: []` and an ignore rule for
every port forward, so a guest created from it sees nothing of the host and publishes
nothing back. A workspace mount is applied per run as a `limactl --set` override, which
keeps the embedded file the single source of truth for everything else.

The tests assert those two properties directly: if someone adds a mount or removes the
port-forward ignore rule from the embedded definition, the unit tests fail.

## Two ways in

`Run` and `RunIO` go through `limactl shell --tty=false`, because their output is data.
`Attach` goes to `ssh` directly with `-tt`, because a full-screen program needs a pty and
`limactl shell` only allocates one when its own stdout is a terminal. It reads the ssh
config Lima writes at `~/.lima/<name>/ssh.config` rather than re-deriving the identity
file, host key policy and forwarded port, so attach cannot drift from what Lima started.

All three apply `asAgent`, which is unexported and applied unconditionally. The ssh login
is the Lima user — that is the account Lima installs the instance key for — and the
session becomes `agent` through `sudo -n -u agent -H --`. Attaching as the administration
account would put the operator one `sudo` from root for no benefit.
