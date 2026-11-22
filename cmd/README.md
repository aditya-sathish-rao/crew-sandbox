# `cmd/`

The operator CLI. One binary, host-side only.

Contract: `crew-sandbox run --workspace <dir> -- <command>`.

The CLI runs on the Mac. It starts and stops the guest, decides what the guest may
mount and reach, and hands the command to the guest. It never executes the command
itself, and it never runs inside the guest. Anything that needs to happen inside the
boundary belongs in `internal/` or in the guest module under `nix/`.
