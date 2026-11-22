# `verify/gates/`

One script per invariant in [SECURITY.md](../../SECURITY.md). A gate proves a property
of the running sandbox and fails loudly when the property does not hold.

The gates are not ported yet. When they are, each one is a phase backed by a script,
matching the phases in `agentic-dev-env/nix/crew-sandbox/check.sh`:

| Phase | Script | Invariant |
| --- | --- | --- |
| `box` | `agent.sh`, `root.sh` | no host mount, default-deny egress, broker port closed, reduced privilege, no host forwarding |
| `proxy` | `proxy.sh` | the allowlist names hosts; SNI and `Host` cannot be retargeted |
| `model` | `model.sh` | no credential in the guest; the gateway holds the key |
| `broker` | `broker.sh` | a token is minted per task and sees only its own repository |
| `toolchain` | `toolchain.sh` | the guest toolchain resolves under `/nix/store` |

A gate is a test of the boundary, not of the code that implements it. It should fail
when the boundary is broken even if every unit test passes. Gates assert capabilities —
what the guest *can* do — so a configuration change that widens access fails them.
