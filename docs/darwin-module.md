# The darwin module: why a user agent and not a daemon

Step 6 of the migration plan asked one question before any code:

> Test whether a launchd daemon can read the vault key non-interactively (`av` refuses with
> "human approval required").

This is the answer, and the design that follows from it. Measured on 2026-11-23, macOS
27.3, Automic Vault 4.23.1. The full comparison of the three shapes — and what the
measurement does and does not support — is in
[`darwin-authority-shape.md`](darwin-authority-shape.md).

## The test

A `launchd` job was bootstrapped into `gui/$(id -u)` — the same domain a `LaunchAgent`
uses, but started with `RunAtLoad` and no terminal:

```console
$ launchctl print gui/$(id -u)/local.crew.vaultprobe | grep -E 'pid|program'
$ cat /tmp/probe.out
ppid: 1
tty:  not a tty
```

From inside that job, `av inject` was asked for several different secrets, including a
multiline PEM and a secret not touched during the session:

```sh
av inject +GITHUB_APP_PRIVATE_KEY -- \
  sh -c 'printf %s "$GITHUB_APP_PRIVATE_KEY" | wc -c'
```

## The result

The value came back. What matters is how the vault recorded the decision, from
`av history --json`:

| Request | `approvalSource` | `reason` |
| ------- | ---------------- | -------- |
| `av inject` from the TTY-less job, `DEEPSEEK_API_KEY` | `Manual` | Approved in prompt |
| the same, `OPENROUTER_API_KEY` | `Manual` | Approved in prompt |
| the same, `GITHUB_APP_PRIVATE_KEY` (multiline) | `Manual` | Approved in prompt |
| a Blessed Script with `capabilities: {}` | `Manual` | Approved in prompt |
| `gh auth status` through the GitHub Gate | `Auto` | Secret Disclosure from Default Policy |

Every plain `inject` — including from a process with `ppid 1` and no TTY — was approved by
a **human**, and the vault says so. The only `Auto` decisions belong to a tool-specific
Authorization Gate, and it approves the tool (`gh`), not an arbitrary command.

Two consequences follow, and they are the whole answer to step 6:

1. **A `LaunchDaemon` cannot hold the model key or the App key.** It starts as root before
   login. There is no Gate for "the proxy", and no human to answer a prompt, so the read
   either blocks until it times out or never happens. The service would come up with no key
   and the model gateway would be disabled with no obvious cause.
2. **A `LaunchAgent` can.** It runs as the operator inside the GUI session, so the prompt
   lands on a desktop where someone can answer it, and the vault can bind a Temporary
   Access Grant to that job. This is the narrowest shape that works rather than a
   compromise.

## What this costs, stated plainly

- The services start **at login** and stop when the last session ends. A Mac that reboots
  unattended has no egress proxy until someone logs in, so the guest's way out is closed
  until then. That is the correct failure direction — closed, not open.
- A first start after login needs a human click unless the operator has enrolled the agent
  and granted it a Temporary Access Grant. The module's `KeepAlive` waits 30 seconds
  between restarts so a slow prompt is not mistaken for a crash loop.
- Approving a root daemon would need a vault policy that authorizes an exact signed
  executable with no human in the loop. That is a decision for the operator, not something
  this repository should arrange silently.

## The module

`nix/modules/darwin.nix` exports `services.crew-sandbox`, which produces two user agents:

| Option | Default | Why |
| ------ | ------- | --- |
| `proxy.port` | `3128` | The guest firewall and `lima.yaml` name it; changing it means changing them. |
| `proxy.modelPort` | `3130` | Kept off `proxy.port` so the socket holding the key is never the egress socket. |
| `proxy.upstream` | `""` | Empty disables the gateway; there is no default vendor. |
| `proxy.keySecret` | `CREW_MODEL_KEY` | The vault Secret Name, not the key. |
| `broker.appID` | `""` | Public configuration, not a secret; empty means no broker, with a warning. |

`checks.darwin` evaluates a real `darwinSystem` and asserts what launchd would receive, so a
bad option or `ProgramArguments` type fails `nix flake check` instead of surfacing at
`darwin-rebuild`. It checks that a user agent exists, that no `crew-sandbox` daemon does,
that the provider and Secret Name arrive as flags, and that the log directory is created at
activation — launchd will not create it, and a job whose `StandardOutPath` parent is missing
fails to spawn without a useful error.
