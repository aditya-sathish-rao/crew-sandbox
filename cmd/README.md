# `cmd/`

The operator CLI. One binary, host-side only.

Contract: `crew-sandbox run --workspace <dir> -- <command>`.

`crew-sandbox attach` is the interactive counterpart: it allocates a pty and lands in a
login shell as `agent`. `run` cannot do this, because it forces `--tty=false` so its
output stays data an orchestrator can read.

The CLI runs on the Mac. It starts and stops the guest, decides what the guest may
mount and reach, and hands the command to the guest. It never executes the command
itself, and it never runs inside the guest. Anything that needs to happen inside the
boundary belongs in `internal/` or in the guest module under `nix/`.
