Extracting crew-sandbox from agentic-dev-env
Review of commit c414a5c on main · 21 November 2026
What's there
The sandbox is about 2,800 lines of real code, excluding lockfiles: lima.yaml, proxy.mjs (egress proxy and model gateway), broker/broker.mjs, crew-sandbox.sh, check.sh, six checks/*.sh scripts, and guest.nix. ADR-0010 and the review doc are strong material for a write-up.
A grep of the tracked files found no keys, .ts.net names or hostnames. The one leak is the commit author, m2-pro <m2-pro@m2-pro.local>, which is another reason to start the new repo from a clean first commit. There is also a leftover remote branch, fm/adev-opencode-av-inject, to delete before publishing.
Is it independent of firstmate? Nearly yes
The code mentions firstmate or herdr once, in a comment in lima.yaml. The real firstmate coupling is in the reasoning (ADR-0010) and in the unbuilt phase 4. That makes extraction much easier.
Is it derived from nix-darwin? No, but it does need Nix
Nothing depends on nix-darwin. It does depend on Nix: the CLI is a writeShellApplication, you run it with nix run, and the guest toolchain is home-manager. A plain brew install lima path isn't realistic today. Keep Nix as the packaging and drop nix-darwin from the story.
The five seams to cut
1.  guest_flake=${../..} in default.nix. The guest applies your whole dotfiles flake, so the new repo needs its own flake.
2.  Automic Vault. av inject is hardcoded in crew-sandbox.sh and check.sh. Put it behind a small secret-backend script, with av as one backend and an environment-variable backend for everyone else.
3.  DeepSeek. proxy.mjs reads DEEPSEEK_API_KEY and defaults to api.deepseek.com. Make that a provider config with an upstream URL and a key variable name.
4.  pi. guest.nix takes piPackage, and models.json is pi's format. Move those into a harness adapter directory.
5.  crew-tools. guest.nix imports ../crew-tools, which are firstmate's companions. Change it to take an extraPackages argument and let your dotfiles pass them in.
Also move brokerAppId = "5853565" out of default.nix into config, and make repos-allow an example file.
Before you market it
The README is honest that the box, proxy, model gateway and broker exist and that moving the fleet in is still to do. Your own review lists open items:
• The broker authenticates no caller; the port is only closed.
• Per-task token binding is unbuilt.
• One high-severity finding remains (H3, the agent owns the Nix store), along with 9 medium ones.
• The "79 assertions" figure predates the C4 fix and needs re-measuring.
Pitch it as "a verified VM boundary for coding agents, with the fleet integration in progress." The Lima finding, that portForwards: [] doesn't stop Lima publishing guest listeners on the host's loopback, is the most shareable thing in the repo.
Proposed repo layout
One repo with three layers: the VM boundary, swappable adapters for secrets, model provider and agent harness, and a firstmate example. The names are proposals.
crew-sandbox/
  README.md
  LICENSE
  flake.nix                packages.crew-sandbox, guest homeConfiguration
  docs/
    threat-model.md
    design/0001-one-vm.md
    review-2026-11.md
  vm/lima.yaml
  egress/
    proxy.mjs
    allowlist
  broker/
    broker.mjs
    repos-allow.example
  secrets/                 backend scripts: env.sh, automic-vault.sh
  providers/               model upstream + key variable name
  guest/
    guest.nix
    harness/pi/            models.json, settings.json
  cli/crew-sandbox.sh
  checks/                  check.sh plus the per-phase scripts
  examples/firstmate/      dispatch config and launch wrapper
Where the current files go:
Current path
New location
Change needed
nix/crew-sandbox/lima.yaml
vm/lima.yaml
None
proxy.mjs, egress-allow
egress/
Read the model upstream and key variable from providers/
broker/, repos-allow
broker/, repos-allow.example
Take the App ID from config
crew-sandbox.sh, check.sh
cli/, checks/
Call the secrets backend instead of av inject
default.nix
flake.nix
Replace guest_flake=${../..} with this repo's own flake
guest.nix
guest/guest.nix
Take an extraPackages argument instead of importing ../crew-tools
guest/models.json, settings.json
guest/harness/pi/
None
ADR-0010, crew-sandbox-review.md
docs/
Repoint links
home/firstmate/, pi wrapper, crew-tools
Stay in dotfiles
Dotfiles consume this repo as a flake input
README intro (draft)
Copy this into the new repo's README.md. Add a host, VM, proxy and broker diagram above it, and the 60-second recording of a blocked injection.
# crew-sandbox

A verified VM boundary for autonomous coding agents on macOS. Agents run in a Lima guest with no mounted files, no root, default-deny egress and no credentials of their own. A gate script re-proves each of those properties against the running VM.

Worktrees isolate git state, not authority. An agent running as you can read your files, use your GitHub token and open any connection your machine can, so one prompt injection in issue text can become an exfiltration or a push. crew-sandbox puts the agents behind a boundary you can check.

## What it enforces

- Nothing from `$HOME` is mounted, and nothing the guest listens on reaches the host, including through Lima's fallback port-forward rule.
- Agents have no sudo.
- Egress goes through one proxy with a hostname allowlist. The proxy checks the SNI on TLS and sets `Host` from the destination on HTTP. DNS is closed.
- The model API key stays on the host. The guest holds a gateway token only.
- GitHub access is a per-task App token for one allowlisted repository, backed by a branch ruleset that keeps merging a human step.
- `crew-sandbox kill` stops the VM first, then revokes every issued token.

## Status

The VM, egress proxy, model gateway, guest toolchain and GitHub token broker exist, each with a gate. Moving a full agent fleet into the VM is in progress. The broker does not yet authenticate callers; its port is closed to the guest instead. Open findings are listed in [docs/review-2026-11.md](docs/review-2026-11.md).

This is defense in depth with a stated threat model, not a guarantee. A CONNECT proxy cannot see inside TLS, so the guarantee is which hostnames a guest can reach, not what it sends them.

## Quick start

```sh
nix run github:<you>/crew-sandbox -- up
nix run github:<you>/crew-sandbox -- check
```

The `model` and `broker` phases also need a secrets backend and a GitHub App. See [docs/threat-model.md](docs/threat-model.md).
Revised migration plan
Scaffolding and most of the core are done. This plan changes three things from your original: the gates run through crew-sandbox check instead of nix flake check, the guest config gets its own step, and the darwin module moves to the end.
1. Cleanup before more code
[x] Rewrite SECURITY.md from the real design: a VM with mounts: [], nftables default-deny, host proxy and broker, optional read-only workspace
[x] Remove the template placeholder text and the gate names that don't exist
[x] Delete the 14 empty .gitkeep directories that aren't in the README layout
[x] Keep one copy of each allowlist in internal/allowlist/ and embed it
[x] Set your GitHub noreply email, rewrite the three commit authors, force-push
[x] Add a LICENSE
[x] Change "container" to "VM" in the README and this plan, and add "unless --workspace is given" to the mounts claim
2. Scaffold
[x] flake.nix, README.md, SECURITY.md and the directory skeleton exist
[x] Run nix flake show and nix flake check on the Mac
3. Core
[x] Lima definition, vault provider chain, broker, proxy and unit tests are ported
[x] Run crew-sandbox run --workspace /tmp/test-repo -- env and confirm no secrets appear in the guest environment
[x] Move the hardcoded DEEPSEEK_API_KEY and api.deepseek.com into a provider setting
[x] Ship egress-allow and repos-allow as .example files and drop herdr and firstmate names from core comments
4. Guest config and verification
[x] Port guest.nix as an exported home-manager configuration with an extraPackages argument, with no import of ../crew-tools
[x] Port check.sh and the phase scripts into verify/gates/: guest, root and sudo, toolchain, proxy, model, broker
[x] Port the red-team script into verify/redteam/
[x] Make crew-sandbox check run them; keep nix flake check for unit tests and selftests only, because Nix builds have no VM or network
[x] Re-count the assertions after the fixes
[x] Decide where the gates run: locally or on a self-hosted runner (GitHub-hosted macOS runners probably can't nest VMs; verify)
[x] Put the assertion count and a recorded run in the README
5. Open findings
[x] Fix the high-severity items, including H3 (the agent owns the Nix store), or list each under Known limitations in SECURITY.md
[x] Do the same for the 9 medium findings
[x] Note that the broker authenticates no caller and its port is only closed to the guest
6. Darwin module, last
[x] Test whether a launchd daemon can read the vault key non-interactively (av refuses with "human approval required")
[x] Choose between a system daemon, a user agent for the proxy, or a broker the CLI starts on demand — a user agent; the comparison and the measurement are in [darwin-authority-shape.md](darwin-authority-shape.md)
[x] Write nix/modules/darwin.nix with services.crew-sandbox.enable
7. Cutover to agentic-dev-env
[x] Tag v0.1.0 (this repository)
[x] Ship the consumer template: `examples/consumer/` is a standalone flake that imports both halves, and `checks.examples` evaluates it so a renamed option fails here rather than in the dotfiles
[ ] Add the flake input pinned to that tag (agentic-dev-env)
[ ] Run the old repo's gates against the new package and compare assertion counts
[ ] Consume the package and guest config from the flake input
[ ] Delete nix/crew-sandbox/ from agentic-dev-env

The three unchecked items all land in `agentic-dev-env`, not here: they change that
repository's flake inputs and delete its copy of the code. This repository's side of the
cutover is the tag and the template, and both exist. `examples/consumer/` is the file to
copy: replace the fleet in `home.nix` and the provider and App ID in `darwin.nix`, and
the rest of the wiring is already correct.
