# `internal/`

Implementation packages the CLI drives: VM lifecycle, egress proxy, secret broker.

Host-side code only. These packages run as the operator on the Mac and are the
enforcement point for the invariants in [SECURITY.md](../SECURITY.md) — the proxy
decides what leaves the guest, the broker decides what credential the guest ever sees.
Nothing here is mounted into or executed by the guest.
