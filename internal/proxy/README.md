# `internal/proxy`

The guest's only door out. A CONNECT proxy that refuses every destination the allowlist
does not name, plus a model gateway that holds the real key so the guest never does.

`proxy.mjs` is ported from `agentic-dev-env`. The Go layer supplies the allowlist from
`internal/allowlist` and the model key from a vault provider. The key is optional: without
it the gateway is disabled and egress enforcement still runs.

The model gateway is provider-agnostic. It forwards to whatever `--model-upstream` names
and authenticates with the secret `--model-key-env` names, so no vendor is hardcoded. With
no upstream the gateway is disabled and the vault is not consulted at all. The script reads
the key from `CREW_MODEL_KEY`, a provider-neutral variable the Go layer fills from the
resolved secret.

The proxy listens on 127.0.0.1 only. The guest reaches it through the gateway address the
guest's firewall opens.

`--selftest` runs seven ClientHello parser cases without a network or a VM. That parser is
what makes the allowlist describe a hostname rather than an IP address, so a bug there is a
boundary bug, not a cosmetic one.
