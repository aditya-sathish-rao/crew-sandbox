# crew-sandbox

A Linux VM boundary for untrusted agent work. The commands you run under it execute inside one
Lima guest; the Mac keeps the credentials, the egress policy and the token broker. Nothing from
the host is mounted into the guest.

This repository is the tool extracted from
[`agentic-dev-env`](https://github.com/aditya-sathish-rao/agentic-dev-env), where it started as
`nix/crew-sandbox/`. It is meant to be consumed there as a Nix flake input, so the sandbox can
be versioned, reviewed and tested on its own.

**Status: core, guest config, gates and host module ported.** The CLI, the VM definition,
the token broker, the egress proxy, the guest's home-manager configuration, the phase gates
and the nix-darwin host module are here. The gates need a running guest and the operator's
secrets; a phase whose inputs are missing fails with the reason rather than reporting a
pass. Phase 0's never-sleep and power settings and the fleet's tools are not: the guest
module is deliberately empty and a consumer supplies the fleet.

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
  unless `--writable` is passed. `--writable` is a host-code-execution path, not just a
  convenience: an agent that can write `.git/hooks`, `.git/config` (`core.fsmonitor`,
  `core.hooksPath`), a `Makefile` or an `.envrc` gets code running on your Mac the next time
  you run `git` or `make` in that directory.
- **An nftables default-deny.** All input and output is dropped except loopback and the
  egress proxy and model-gateway ports on the host gateway. The broker's port is
  deliberately not opened.
- **A host proxy that names hosts and addresses.** It admits only the hostnames in the egress
  allowlist (`internal/allowlist/egress-allow.example` unless `--egress-allow` supplies one),
  reads the TLS handshake so an allowlisted CDN address cannot reach another site behind it,
  and resolves each name itself, refusing loopback, link-local, private and CGNAT addresses
  before connecting to the address it checked.
- **A host broker that holds the key.** GitHub tokens are minted per task and never handed to
  the guest as a long-lived credential.

## Layout

| Path             | What belongs there                                                              |
| ---------------- | ------------------------------------------------------------------------------- |
| `cmd/`           | The operator CLI: `crew-sandbox run --workspace <path> -- <command>`.           |
| `internal/`      | Implementation packages the CLI drives: VM lifecycle, proxy, broker.            |
| `nix/modules/`   | The nix-darwin host module: the proxy and broker as launchd user agents.        |
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
`checks.guest` (the guest configuration, evaluated), `checks.darwin` (the host module,
evaluated), `homeManagerModules.default` (the guest configuration),
`darwinModules.default` (the host configuration), and `devShells.default`/`formatter`.

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

### The host services

`darwinModules.default` runs the egress proxy and the token broker as launchd **user
agents**, which is the one shape the vault's policy allows:

```nix
{
  imports = [ crew-sandbox.darwinModules.default ];

  system.primaryUser = "you";   # nix-darwin requires it for user agents
  services.crew-sandbox = {
    enable = true;
    package = crew-sandbox.packages.aarch64-darwin.default;
    proxy.upstream = "https://api.deepseek.com";
    proxy.keySecret = "CREW_MODEL_KEY";
    broker.appID = "5853565";
  };
}
```

They run at login, as you, so the vault's approval prompt lands on a desktop where a
human can answer it. A system daemon cannot do this: it starts before login, and the
vault answers a keyless, unattended call with "human approval required". The trade is
explicit — a Mac that reboots unattended has no egress proxy until someone logs in. See
[`docs/darwin-module.md`](docs/darwin-module.md) for the measurement behind that choice,
and [`docs/darwin-authority-shape.md`](docs/darwin-authority-shape.md) for the comparison
against a system daemon and against starting the services on demand.

### Applying it to the guest

The module is exported, not installed. The guest has no host mount, so the consumer
applies its own pinned `agent@aarch64-linux` generation from inside the VM, as `agent`.
A login shell already carries the proxy, so flake inputs are fetched through the
allowlisted door. For example, with the consumer's flake published at `OWNER/REPO`:

```console
$ limactl shell crew-sandbox -- sudo -n -u agent -H bash -lc \
    'nix run github:nix-community/home-manager/release-26.05 -- \
       switch --flake github:OWNER/REPO#agent@aarch64-linux'
```

A local checkout can be copied in with `limactl copy -r` and referenced by path instead.
There is deliberately no `crew-sandbox apply`: this repository carries no fleet and no
flake reference to switch. `crew-sandbox check model` fails until the activation has
produced `~/.pi/agent/models.json` and `settings.json`, which is the closest thing to a
check that the generation landed.

## Development

```console
$ nix flake show          # evaluates the flake
$ nix develop             # git, go, jq, lima, nixfmt, nodejs, prettier
$ nix build               # builds the CLI
$ nix flake check         # unit tests, selftests, prettier, guest config
$ nix fmt                 # format the Nix sources
$ prettier --check internal/*/*.mjs  # the two ported scripts
$ shellcheck verify/*/*.sh  # lint the gates and the red-team script
```

The CLI needs `limactl` and `node`, and the gates need `curl`, `jq` and coreutils; the
packaged binary wraps all of them. Running it from `nix develop` gives you the same tools
without the wrapper.

`graphify-out/graph.json` is a committed symbol graph of this code. See
[`docs/code-graph.md`](docs/code-graph.md) for the lookups it answers and how to refresh it.

## Verification

`verify/` holds the gates. Each is a script that runs named assertions, one line per
assertion, exiting non-zero on any failure. `crew-sandbox check <phase>` runs them, and
the scripts are embedded in the binary so the gates that run are the gates that were
built:

```console
$ crew-sandbox check guest      # no mount, no sudo, default-deny egress, no host forward
$ crew-sandbox check root       # the ruleset, sudoers and the gate directory
$ crew-sandbox check toolchain  # the tools named by --expected-tools resolve under /nix/store
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

### What a run reports

Every phase prints one line per assertion it reaches, so these counts are what a healthy
run prints. With one expected tool and every input supplied, `check all` prints 81:

| Phase | Assertions | Needs |
| --- | --- | --- |
| `guest` | 13 | a running guest; `--workspace` if one is mounted |
| `root` | 10 | a running guest |
| `toolchain` | 3 + one per `--expected-tools` name | `--expected-tools` |
| `proxy` | 10 | a running guest; starts its own keyless proxy |
| `model` | 22 | `--model-upstream`, the model key in the vault, and an applied guest configuration |
| `broker` | 15 | `--app-id`, `--other-repo`, and the App private key in the vault |
| `redteam` | 7 | a running guest; one line per attack, `BLOCKED` or `SKIP` |

`model` is 22 rather than 12 because it re-runs the nine allowlist assertions through the
keyed proxy: a run that proved the key stays home while the allowlist was wrong would
report a boundary that does not exist. `toolchain` counts what the operator names, so its
total is theirs.

### A recorded run

Against this repository's guests on 2026-11-23 (aarch64), with `/tmp/test-repo` mounted
read-only. `guest` and `redteam` ran on the `crew-sandbox` guest; `model` ran on the older
`crew` guest, whose existing home-manager generation already carried `pi` (its
`models.json` was pointed at the new 3130 gateway port and the 3130 firewall rule was
added to match the current `lima.yaml`). `broker` is a host-only phase.

```console
$ crew-sandbox check guest --workspace /tmp/test-repo

phase 1: the guest
PASS  running as agent
PASS  the host filesystem is absent
PASS  the workspace is the only host directory mounted, read-only
PASS  egress to an arbitrary host is blocked
PASS  a query to an arbitrary resolver is blocked
PASS  the system resolver cannot resolve
PASS  the host is unreachable on a port the boundary did not open
PASS  sudo is unavailable
PASS  the firewall cannot be flushed
PASS  the firewall ruleset cannot be read
PASS  the firewall configuration is not writable
PASS  a guest listener on every address stays off the host
PASS  a guest listener on loopback stays off the host

guest: pass

$ crew-sandbox check model --name crew --model-upstream https://api.deepseek.com --model-key-env DEEPSEEK_API_KEY

model: starting the proxy with the vault key for the duration of this gate
model: approve the vault prompt if one appears

phase 5: the model key
PASS  the guest holds a model gateway token
PASS  the gateway token is not a model key
PASS  no environment value is the model key
PASS  the model client's provider points at the gateway
PASS  the model client reads its gateway token from the guest file
PASS  the model client defaults to a provider through the gateway (deepseek/deepseek-flash)
PASS  the gateway refuses a request without a token
PASS  the gateway refuses a request with the wrong token
PASS  the gateway reaches the model API with the host's key
PASS  the gateway refuses paths outside the model API
PASS  the model API is unreachable without the gateway
PASS  the harness lists the proxied model (deepseek-flash)
PASS  the ClientHello parser reads the name a tunnel asks for

phase 4: the egress proxy (through the keyed proxy)
PASS  an allowlisted host answers through the proxy
PASS  the GitHub API answers through the proxy (200)
PASS  a host off the allowlist is refused over https
PASS  a host off the allowlist is refused over http
PASS  a non-web port is refused even on an allowlisted host
PASS  a spoofed Host header cannot retarget a plain-HTTP request
PASS  a tunnel whose SNI names another host is refused
PASS  CONNECT is refused on port 80
PASS  the guest still cannot reach the internet without the proxy

model: pass

$ crew-sandbox check broker --app-id 5853565 --other-repo aditya-sathish-rao/agentic-dev-env

broker: starting the broker with the vault key for the duration of this gate
broker: approve the vault prompt if one appears

phase 6: the token broker
PASS  the broker answers on its health route
PASS  the broker names an allowlisted repository (crew-broker)
PASS  a repository outside the allowlist is refused before minting
PASS  the broker mints a token for crew-broker
PASS  the token sees its own repository (aditya-sathish-rao/crew-broker)
PASS  the token reads its own repository
PASS  the gate knows the default branch (main)
PASS  the default branch requires a pull request (pull_request,non_fast_forward,deletion)
PASS  the token creates an agent branch (agent/gate-gate-49064)
PASS  the token may write an agent branch
PASS  the default branch refuses the token (409)
PASS  GitHub refuses the token a write on another repository (403)
PASS  the gate deletes its agent branch
PASS  the broker revokes the token
PASS  GitHub refuses the revoked token

broker: pass

$ crew-sandbox check redteam --workspace /tmp/test-repo
proxy: starting one for the duration of this phase

phase 7: the adversary
redteam 2026-11-23T16:06:09+05:30 on lima-crew-sandbox as agent
BLOCKED  read-host-home        no host home in the guest; unreadable: /home/crew /root
BLOCKED  find-a-key            no key-shaped value in the environment, no key store in $HOME, and the guest token is not a key
BLOCKED  exfiltrate            direct POST refused; the proxy refused example.com; no resolver answers
BLOCKED  push-to-github        api.github.com refuses the guest (401); aditya-sathish-rao/crew-sandbox refused the API write (401); the push reached GitHub and was refused (error: unable to read askpass response from '/bin/false' fatal: could not read Username for 'https://github.com': terminal prompts disabled)
BLOCKED  reach-host-services   nothing on 192.168.5.2 accepts a connection except the proxy and model-gateway ports
BLOCKED  escalate              sudo refused; the ruleset cannot be flushed; the ruleset file is not writable
BLOCKED  plant-in-workspace    the workspace is read-only, so no file and no symlink could be planted

redteam: 7/7 blocked

redteam: pass
```

`root` and `toolchain` are not in this transcript; their assertion counts are in the table
above. A phase whose input is missing says which flag it wanted and fails — it does not
pass over an assertion it could not make.

Nothing above is part of `nix flake check`. A Nix build has no VM and no network, so the
gates only run from `crew-sandbox check`. What `nix flake check` covers is what a build
can prove: the vault chain, the mount rendering, every embedded script parsing —
including both `.mjs` files with `node --check` — a prettier check on those two files,
an evaluation of the guest configuration, and the proxy's and broker's own `--selftest`:

```console
$ crew-sandbox proxy --selftest    # 8 ClientHello parser cases
$ crew-sandbox broker --selftest   # JWT signing against the configured key
```

### Where the gates run

The gates run on the operator's machine, against the operator's guest. They are not wired
into CI, and a self-hosted runner is not worth it yet, for three reasons:

- **No hosted runner can run them.** The guest is `vmType: vz`, which is Apple's
  Virtualization.framework, and a GitHub-hosted macOS runner is itself a machine virtualized
  with that framework. GitHub's runner reference states that for arm64 macOS runners
  "nested-virtualization is not supported due to the limitation of Apple's Virtualization
  Framework" ([reference](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)),
  and the guest is arm64. The x86 macOS runners fail on the architecture rather than on
  nesting.
- **The boundary being measured is the operator's.** The `guest` phase asserts that a
  listener started in the guest never appears on *this* machine's loopback, and the proxy
  phases measure *this* machine's gateway address. A pass on someone else's hardware would
  not describe the machine it was meant to protect.
- **Two phases spend real credentials.** `model` reads the model key out of the vault and
  `broker` mints a live GitHub App token, writes an agent branch and revokes. Handing those
  to a runner adds a credential surface where the tool exists to remove one.

`nix flake check` and the operator binary build *are* wired into CI:
[`.github/workflows/ci.yml`](.github/workflows/ci.yml) runs `nix build .#crew-sandbox` and
`nix flake check` on an arm64 macOS runner — unit tests, both selftests, prettier, and the
guest-configuration evaluation, with no VM and no network. The gates above are not.

A self-hosted runner on a spare Mac is the only way to run the gates in CI. It becomes worth
arguing for when someone other than the operator needs the result — and it would mean that
Mac executes whatever a triggering branch contains, which is its own boundary to justify.

## License

MIT. See [LICENSE](LICENSE).
