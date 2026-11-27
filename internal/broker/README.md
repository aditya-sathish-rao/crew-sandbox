# `internal/broker`

The host-side GitHub token broker. A crewmate never holds a GitHub credential: it asks
the broker for a token scoped to one repository, and the token expires on its own.

`broker.mjs` is ported from `agentic-dev-env`, with one deliberate divergence: the
request handler is wrapped so a request it cannot parse answers 500 instead of
terminating the process. The Go layer supplies the App ID,
the private key from a vault provider, and the repository allowlist from
`internal/allowlist`. The key is passed
through the child's environment, never argv, so it does not appear in `ps` output.

The broker listens on 127.0.0.1 only. The guest's firewall deliberately does not open its
port: while the broker authenticates no caller, a crewmate that could reach it could mint
itself a token.

`--selftest` proves the key parses and the JWT signs without contacting GitHub.
