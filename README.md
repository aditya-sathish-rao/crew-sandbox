# crew-sandbox

A Linux VM boundary for untrusted agent work. The commands you run under it execute inside one
Lima guest; the Mac keeps the credentials, the egress policy and the token broker. Nothing from
the host is mounted into the guest.

This repository is the tool extracted from
[`agentic-dev-env`](https://github.com/aditya-sathish-rao/agentic-dev-env), where it started as
`nix/crew-sandbox/`. It is meant to be consumed there as a Nix flake input, so the sandbox can
be versioned, reviewed and tested on its own.

**Status: core, guest config and gates ported.** The CLI, the VM definition, the token broker,
the egress proxy, the guest's home-manager configuration and the phase gates are here. The
gates need a running guest and the operator's secrets; a phase whose inputs are missing fails
with the reason rather than reporting a pass.

## Why the boundary exists

A worktree isolates git state and nothing else. A crewmate running as your user can read every
file in `$HOME`, use your `gh` token, and open any connection the host can. One prompt
injection in a README or an issue body is enough to turn a task into an exfiltration or a push.

The guest kernel is the boundary. Inside it, untrusted repo content cannot see `$HOME`, cannot
reach the vault, cannot open a socket the allowlist does not name, and holds no credential that
outlives its task. See [SECURITY.md](SECURITY.md) for the threat model and the invariants each
gate is meant to prove.

Concretely, the boundary is four things:

- **A VM with `mounts: []`.** No host directory is mounted unless `--workspace` is given, so
  `/Users` does not exist inside the guest. A workspace mount is opt-in per run and read-only
  unless `--writable` is passed.
- **An nftables default-deny.** All input and output is dropped except loopback and the proxy
  port on the host gateway. The broker's port is deliberately not opened.
- **A host proxy that names hosts.** It admits only the hostnames in the egress allowlist
  (`internal/allowlist/egress-allow.example` unless `--egress-allow` supplies one), and it
  reads the TLS handshake so an allowlisted CDN address cannot reach another site behind it.
- **A host broker that holds the key.** GitHub tokens are minted per task and never handed to
  the guest as a long-lived credential.

## Layout

| Path             | What belongs there                                                              |
| ---------------- | ------------------------------------------------------------------------------- |
| `cmd/`           | The operator CLI: `crew-sandbox run --workspace <path> -- <command>`.           |
| `internal/`      | Implementation packages the CLI drives: VM lifecycle, proxy, broker.            |
| `nix/modules/`   | Nix modules exported to consumers, including `darwinModules.default`.           |
| `nix/packages/`  | The host package set the CLI and the darwin module depend on.                   |
| `nix/guest/`     | The guest's home-manager configuration, exported as `homeManagerModules`.       |
| `verify/gates/`  | One script per invariant in [SECURITY.md](SECURITY.md), run by `check`.         |
| `verify/redteam/`| Adversarial simulations that try to break those invariants.                     |
| `examples/`      | Minimal consumer flakes showing how to import this repo.                        |

Every directory carries a short `README.md` stating its boundary: what runs there, and
what must not.

## Using it from `agentic-dev-env`

```nix
{
  inputs.crew-sandbox.url = "github:aditya-sathish-rao/crew-sandbox";
}
```

The flake exposes `packages.default` (the CLI), `checks.unit` (the Go unit tests),
`checks.guest` (the guest configuration, evaluated), `homeManagerModules.default` (that
configuration) and `devShells.default`/`formatter`. The `darwinModules.default` module is
not written yet; it lands when the host-side options are settled.

The guest configuration names no tools of its own. Pass the fleet as `extraPackages`, and
the guest gets exactly those and a shell:

```nix
{
  homeConfigurations."agent@aarch64-linux" = home-manager.lib.homeManagerConfiguration {
    pkgs = nixpkgs.legacyPackages.aarch64-linux;
    modules = [ crew-sandbox.homeManagerModules.default ];
    extraSpecialArgs = { extraPackages = [ pi.packages.aarch64-linux.default ]; };
  };
}
```

The username and home directory are not options: `nix/guest/` fixes them to the `agent`
account `lima.yaml` creates, and activation fails anywhere else.

## Development

```console
$ nix flake show          # evaluates the flake
$ nix develop             # git, go, jq, lima, nixfmt, nodejs
$ nix build               # builds the CLI
$ nix flake check         # runs the unit tests and the two selftests
$ nix fmt                 # format the Nix sources
$ shellcheck verify/*/*.sh  # lint the gates and the red-team script
```

The CLI needs `limactl` and `node`, and the gates need `curl`, `jq` and coreutils; the
packaged binary wraps all of them. Running it from `nix develop` gives you the same tools
without the wrapper.

## Verification

`verify/` holds the gates. Each is a script that runs named assertions, one line per
assertion, exiting non-zero on any failure. `crew-sandbox check <phase>` runs them, and
the scripts are embedded in the binary so the gates that run are the gates that were
built:

```console
$ crew-sandbox check guest      # no mount, no sudo, default-deny egress, no host forward
$ crew-sandbox check root       # the ruleset, sudoers and the gate directory
$ crew-sandbox check proxy      # the allowlist names hosts, not addresses
$ crew-sandbox check model      # the key stays on the host
$ crew-sandbox check broker     # one repo, one token, revoked at teardown
$ crew-sandbox check redteam    # the adversary: seven ways out, all must fail
$ crew-sandbox check all        # every phase in one process
```

The gate is told what it cannot know, because none of it belongs to this repository:

```console
$ crew-sandbox check all \
    --expected-tools "pi gh" \
    --model-upstream https://api.example.com/v1 \
    --app-id 123456 \
    --other-repo owner/elsewhere
```

`toolchain` needs `--expected-tools`, `model` needs `--model-upstream` and a vault secret
named by `--model-key-env`, and `broker` needs `--app-id` and `--other-repo`. A phase
whose input is missing fails with the reason instead of passing over an assertion it
could not make.

`check redteam` runs a script that attacks the boundary the way a prompt-injected agent
would — host filesystem, credentials, exfiltration, pushes, host services, sudo, planted
hooks — and reports one line per attempt. The transcript is the evidence, so keep it:

```console
$ crew-sandbox check redteam --workspace /path/to/repo --redteam-log redteam.log
```

Nothing above is part of `nix flake check`. A Nix build has no VM and no network, so the
gates only run from `crew-sandbox check`. What `nix flake check` covers is what a build
can prove: the vault chain, the mount rendering, every embedded script parsing, an
evaluation of the guest configuration, and the proxy's and broker's own `--selftest`:

```console
$ crew-sandbox proxy --selftest    # 7 ClientHello parser cases
$ crew-sandbox broker --selftest   # JWT signing against the configured key
```

## License

MIT. See [LICENSE](LICENSE).
