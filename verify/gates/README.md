# `verify/gates/`

One script per invariant in [SECURITY.md](../../SECURITY.md). A gate proves a property
of the running sandbox and fails loudly when the property does not hold.

| Gate | Invariant |
| --- | --- |
| `fs_boundary` | no host mount; no host forwarding; admin home unreadable |
| `egress` | default-deny; only 3128 reachable; broker port closed |
| `proxy` | allowlist names hosts; SNI and `Host` cannot be retargeted |
| `env_audit` | no credential in the guest's environment or `/proc` |
| `privilege` | no sudo; ruleset not readable, flushable or writable |

A gate is a test of the boundary, not of the code that implements it. It should fail
when the boundary is broken even if every unit test passes. Gates assert capabilities —
what the guest *can do* — so re-enabling sudo or reopening egress fails them.

None of these are ported yet. Until they are, `crew-sandbox check` exits with an error
rather than reporting a pass.
