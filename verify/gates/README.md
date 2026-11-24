# `verify/gates/`

One script per invariant in [SECURITY.md](../../SECURITY.md). A gate proves a property
of the running sandbox and fails loudly when the property does not hold.

`check.sh` is the driver: it stages a script where the user that must run it can read
it, starts the host service a phase needs, and owns everything only the host can
observe. The rest are the assertions.

| Phase | Script | Runs as | Invariant |
| --- | --- | --- | --- |
| `guest` | `guest.sh` | `agent` in the guest | no host mount, default-deny egress, no sudo, no readable firewall |
| `guest` | `check.sh` (host side) | the host | a guest listener never reaches the host's loopback; `crew-sandbox run` executes what it is given as `agent`, not as the Lima user |
| `root` | `root.sh` | root in the guest | the ruleset, sudoers, the gate directory, the closed broker port |
| `toolchain` | `toolchain.sh` | `agent`, login shell | the tools named by `--expected-tools` resolve under `/nix/store` (location only); the store has no registration errors |
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
| a repository the App is installed on (not the one being minted) | `--other-repo OWNER/NAME` | `broker` |
| the allowlists | `--egress-allow FILE`, `--repos-allow FILE` | `proxy`, `model`, `broker` |

A phase whose input is missing fails with the reason rather than passing over an
assertion it could not make. `broker-ruleset.json` is the ruleset the broker gate
expects on the default branch; it is applied by hand, once per repository.

The adversarial run is `crew-sandbox check redteam`, which stages
[`../redteam/redteam.sh`](../redteam/redteam.sh) instead of a gate. It is a scenario
rather than an assertion, so it lives beside these rather than among them.

A gate is a test of the boundary, not of the code that implements it. It should fail
when the boundary is broken even if every unit test passes. Gates assert capabilities —
what the guest *can* do — so a configuration change that widens access fails them.
