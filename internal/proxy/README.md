# `internal/proxy`

The guest's only door out. A CONNECT proxy that refuses every destination the allowlist
does not name, plus a model gateway that holds the real key so the guest never does.

`proxy.mjs` is ported from `agentic-dev-env`. The Go layer supplies the allowlist from
`internal/allowlist` and the model key from a vault provider. The key is optional: without
it the gateway is disabled and egress enforcement still runs.

The model gateway has no hardcoded vendor: it forwards to whatever `--model-upstream`
names and authenticates with the secret `--model-key-env` names. It speaks one surface,
though - `GET /models` and `POST /chat/completions` with `Authorization: Bearer` over
https, the OpenAI-compatible shape - so a provider that authenticates differently needs
a change in the script. With no upstream the gateway is disabled and the vault is not
consulted at all. The script reads the key from `CREW_MODEL_KEY`, a provider-neutral
variable the Go layer fills from the resolved secret.

The gateway is bounded, because the guest's token is world-readable inside the VM by design and
therefore cannot be what limits abuse: a request body is capped at 32 MiB (declared and
streamed, so chunked bodies are counted too) and the token gets 120 requests a minute.

The proxy listens on 127.0.0.1 only. The guest reaches it through the gateway address the
guest's firewall opens.

`--selftest` runs eight ClientHello parser cases, plus the address guard's cases, without a
network or a VM. The parser is what makes the allowlist describe a hostname rather than an IP
address, so a bug there is a boundary bug, not a cosmetic one. The address cases cover
loopback v4 and v6, link-local (including the cloud metadata address), private ranges, and a
name that resolves to loopback.

The egress routes resolve a name once, refuse it if any answer is a blocked address, and then
connect to that address rather than the name - so a name that was safe when it was checked
cannot be re-pointed at the host before the connection is made. The model gateway's upstream
is deliberately *not* subject to this: it is operator configuration, and a model server on the
operator's own loopback is a legitimate upstream.
