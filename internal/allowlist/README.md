# `internal/allowlist`

The two policy files that decide what the sandbox may reach, and the only copy of
either.

| File | Enforced by | What it decides |
| --- | --- | --- |
| `egress-allow` | the proxy | which hostnames the guest may reach |
| `repos-allow` | the broker | which repositories a token may be minted for |

Both are embedded in the binary with `go:embed`, so a built binary cannot be pointed at
a different policy by editing a file next to it. The proxy and the broker read them from
this package rather than keeping their own copies, so there is one place to edit and no
way for the two to drift apart.

The format is shared: one entry per line, `#` starts a comment, a leading dot matches a
domain and its subdomains. `Parse`, `HostAllowed` and `RepoAllowed` implement it, and the
tests assert that the embedded files are non-empty and that `api.github.com` is present —
it was once missing, which broke every `gh` call while the proxy looked healthy.

An empty file is an error, not "deny everything": a proxy or broker that starts and then
refuses every request reads as a broken component rather than a missing policy.
