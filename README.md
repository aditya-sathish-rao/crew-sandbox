# crew-sandbox

A Linux VM boundary for untrusted agent work. The crew's panes, the first mate and the
crewmates run inside one Lima guest; the Mac keeps the credentials, the egress policy and the
token broker. Nothing from the host is mounted into the guest.

This repository is the tool extracted from
[`agentic-dev-env`](https://github.com/aditya-sathish-rao/agentic-dev-env), where it started as
`nix/crew-sandbox/`. It is meant to be consumed there as a Nix flake input, so the sandbox can
be versioned, reviewed and tested on its own.

**Status: scaffold.** The flake evaluates and the directory skeleton exists. No sandbox logic
has moved here yet; the working implementation still lives in `agentic-dev-env`.

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

The intended consumer surface is a Nix module plus the host package set. Neither is exported
yet; the flake currently exposes only `devShells` and `formatter`.

## Development

```console
$ nix flake show          # evaluates the flake
$ nix develop             # git, jq, nixfmt
$ nix fmt                 # format the Nix sources
```

## Verification

`verify/` holds the gates. Each gate is a script that takes a phase and runs named assertions,
one line per assertion, exiting non-zero on any failure. Phases add assertions; none are
removed. The gates are not implemented yet.

## License

Not yet chosen.
