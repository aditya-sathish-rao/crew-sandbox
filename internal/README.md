# `internal/`

Implementation packages the CLI drives. Host-side code only: this is the enforcement
point for the invariants in [SECURITY.md](../SECURITY.md).

| Package | What it owns |
| --- | --- |
| `lima/` | The VM definition and the `limactl` wrapper. The only package that knows a VM exists. |
| `vault/` | The secret provider interface. Atomic Vault is the default backend; `Static` and `FromEnv` exist for tests and for operators who export secrets. |
| `broker/` | The GitHub token broker. Wraps the ported `broker.mjs` and supplies its key from a provider. |
| `proxy/` | The egress proxy and model gateway. Wraps the ported `proxy.mjs` and supplies its allowlist and key. |
| `allowlist/` | The egress and repo allowlists, embedded. The only copy of either file. |

The `.mjs` files are ported verbatim from `agentic-dev-env/nix/crew-sandbox/`. They are
embedded in the Go binary, so a built binary cannot be pointed at a different script by
editing a file next to it. The Go layer supplies configuration and secrets; the policy
logic stays in the scripts that were already proven.
