# `internal/proxy`

The guest's only door out. A CONNECT proxy that refuses every destination the allowlist
does not name, plus a model gateway that holds the real key so the guest never does.

`proxy.mjs` is ported verbatim from `agentic-dev-env`. The Go layer supplies the allowlist
and the model key from a vault provider. The key is optional: without it the gateway is
disabled and egress enforcement still runs.

The proxy listens on 127.0.0.1 only. The guest reaches it through the gateway address the
guest's firewall opens.

`--selftest` runs seven ClientHello parser cases without a network or a VM. That parser is
what makes the allowlist describe a hostname rather than an IP address, so a bug there is a
boundary bug, not a cosmetic one.
