# crew-sandbox

A Linux VM boundary for untrusted agent work. The crew's panes, the first mate and the
crewmates run inside one Lima guest; the Mac keeps the credentials, the egress policy and the
token broker. Nothing from the host is mounted into the guest.

This repository is the tool extracted from
[`agentic-dev-env`](https://github.com/aditya-sathish-rao/agentic-dev-env), where it started as
`nix/crew-sandbox/`. It is meant to be consumed there as a Nix flake input, so the sandbox can
be versioned, reviewed and tested on its own.

**Status: core ported.** The CLI, the VM definition, the token broker and the egress proxy are
here and tested. The verification gates are not ported yet, so `crew-sandbox check` reports that
plainly rather than pretending to pass.

## Why the boundary exists

A worktree isolates git state and nothing else. A crewmate running as your user can read every
file in `$HOME`, use your `gh` token, and open any connection the host can. One prompt
injection in a README or an issue body is enough to turn a task into an exfiltration or a push.

The guest kernel is the boundary. Inside it, untrusted repo content cannot see `$HOME`, cannot
reach the vault, cannot open a socket the allowlist does not name, and holds no credential that
outlives its task. See [SECURITY.md](SECURITY.md) for the threat model and the invariants each
gate is meant to prove.

## Layout

| Path             | What belongs there                                                              |
| ---------------- | ------------------------------------------------------------------------------- |
| `cmd/`           | The operator CLI: `crew-sandbox run --workspace <path> -- <command>`.           |
| `internal/`      | Implementation packages the CLI drives: VM lifecycle, proxy, broker.            |
| `nix/modules/`   | Nix modules exported to consumers, including `darwinModules.default`.           |
| `nix/packages/`  | The host package set the CLI and the darwin module depend on.                   |
| `verify/gates/`  | One script per invariant in [SECURITY.md](SECURITY.md).                         |
| `verify/redteam/`| Adversarial simulations that try to break those invariants.                     |
| `examples/`      | Minimal consumer flakes, including orchestrator wiring such as `herdr`.         |

Every directory carries a short `README.md` stating its boundary: what runs there, and
what must not.

## Using it from `agentic-dev-env`

```nix
{
  inputs.crew-sandbox.url = "github:aditya-sathish-rao/crew-sandbox";
}
```

The flake exposes `packages.default` (the CLI), `checks.unit` (the Go unit tests),
`devShells.default` and `formatter`. The `darwinModules.default` module is not written yet; it
lands when the host-side options are settled.

## Development

```console
$ nix flake show          # evaluates the flake
$ nix develop             # git, go, jq, lima, nixfmt, nodejs
$ nix build               # builds the CLI
$ nix flake check         # runs the unit tests
$ nix fmt                 # format the Nix sources
```

The CLI needs `limactl` and `node` on PATH; the packaged binary wraps both. Running it from
`nix develop` gives you the same tools without the wrapper.

## Verification

`verify/` holds the gates. Each gate is a script that takes a phase and runs named assertions,
one line per assertion, exiting non-zero on any failure. Phases add assertions; none are
removed. The gates are not ported yet, so `crew-sandbox check` exits with an error saying so.

What is tested today is the part that does not need a guest: the vault provider chain and the
mount rendering, run by `nix flake check`. The broker and proxy each carry a `--selftest` that
exercises their parsing logic without a network or a VM:

```console
$ crew-sandbox proxy --selftest    # 7 ClientHello parser cases
$ crew-sandbox broker --selftest   # JWT signing against the configured key
```

## License

Not yet chosen.
