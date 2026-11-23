# `nix/modules/`

Nix modules exported to consumers. `darwinModules.default` is the entry point for
`agentic-dev-env`.

A module here configures the host: the egress proxy, the token broker and the policy
they enforce. It must not assume a username, a home directory, or a dotfiles layout —
every path is a module option with a documented default.

The guest's configuration is the other kind of module and lives in [`../guest/`](../guest/):
it *is* the agent user's home, so it fixes that layout rather than accepting it.

## `darwin.nix`

Two launchd **user agents**: `crew-sandbox-proxy` (which holds the model key) and
`crew-sandbox-broker` (which holds the GitHub App key). The guest is Lima's business;
nothing here creates or starts it.

### Why a user agent and not a system daemon

This was the open question in the migration plan, and it was answered by measurement
rather than argument. `av inject` was run from a `launchd` job with `ppid 1` and no TTY,
against several different secrets:

| Request | Approval recorded |
| ------- | ----------------- |
| `av inject` from a TTY-less job | value returned, `approvalSource: Manual` |
| the same via a Blessed Script with `capabilities: {}` | value returned, `approvalSource: Manual` |
| `gh` through its tool-specific Gate | `approvalSource: Auto` |

Every read that went through plain `inject` required a human, and Authorization History
recorded it as "Approved in prompt" — including from a job with no terminal at all. The
only unattended approvals came from a tool-specific Authorization Gate, and a per-tool
gate cannot serve a service that starts before any tool runs.

So a **user agent** is the narrowest thing that works: it runs as the operator inside the
GUI session, so a vault prompt is answerable and the job can hold a Temporary Access
Grant. A `LaunchDaemon` would start as root before login and simply fail.

The cost is stated rather than hidden: the services start at login, so a Mac that reboots
unattended has no egress proxy until someone logs in. That is cheaper than a daemon
holding a key nobody approved.

### What the check proves

`checks.darwin` evaluates a real `darwinSystem` with the module enabled and asserts on
what launchd would actually be given, because a misspelled option or a bad
`ProgramArguments` type stays invisible until someone runs `darwin-rebuild`:

- a user agent is registered, and no `crew-sandbox` launch daemon is
- `RunAtLoad`, and `KeepAlive.SuccessfulExit = false` (restart on failure only)
- the provider and the key's Secret Name reach the proxy as flags, not baked in
- the App ID and port reach the broker, on its own listener
- the log directory is created at activation, since launchd will not create it
- a disabled module registers no agent, and an enabled broker with no App ID warns
  instead of starting a broker that can mint nothing
