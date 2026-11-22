# `verify/gates/`

One script per invariant in [SECURITY.md](../../SECURITY.md). A gate proves a property
of the running sandbox and fails loudly when the property does not hold.

`check.sh` is the driver: it stages a script where the user that must run it can read
it, starts the host service a phase needs, and owns everything only the host can
observe. The rest are the assertions.

| Phase | Script | Runs as | Invariant |
| --- | --- | --- | --- |
| `guest` | `guest.sh` | `agent` in the guest | no host mount, default-deny egress, no sudo, no readable firewall |
| `root` | `root.sh` | root in the guest | the ruleset, sudoers, the gate directory, the closed broker port |
| `toolchain` | `toolchain.sh` | `agent`, login shell | the fleet resolves under `/nix/store` and the store verifies |
| `proxy` | `proxy.sh` | `agent` in the guest | the allowlist names hosts; SNI and `Host` cannot be retargeted |
| `model` | `model.sh` | `agent` in the guest | no credential in the guest; the gateway holds the key |
| `broker` | `broker.sh` | the host | a token is minted per task, sees one repository, and is refused the default branch |

The gate is told what it cannot know. None of this is a property of this repository, so
`crew-sandbox check` takes it as flags:

| Input | Flag | Needed by |
| --- | --- | --- |
| the fleet's tools | `--expected-tools "pi gh"` | `toolchain` |
| the model provider's base URL | `--model-upstream URL` | `model` |
| the vault secret holding the model key | `--model-key-env NAME` | `model` |
| the GitHub App ID | `--app-id ID` | `broker` |
| a repository the App is not installed on | `--other-repo OWNER/NAME` | `broker` |
| the allowlists | `--egress-allow FILE`, `--repos-allow FILE` | `proxy`, `model`, `broker` |

A phase whose input is missing fails with the reason rather than passing over an
assertion it could not make. `broker-ruleset.json` is the ruleset the broker gate
expects on the default branch; it is applied by hand, once per repository.

A gate is a test of the boundary, not of the code that implements it. It should fail
when the boundary is broken even if every unit test passes. Gates assert capabilities —
what the guest *can* do — so a configuration change that widens access fails them.
