# Handoff: finish the crew-sandbox cutover

Written 2026-11-26. This is the brief for an agent working in a fresh window. Read it
top to bottom before running anything.

---

## 0. The one thing that blocks everything

`crew-sandbox` has an **unpushed commit and an unpushed tag**. The cutover pins to that
tag, so nothing downstream can start until it is on the remote.

```console
$ cd ~/projects/crew-sandbox
$ git log --oneline origin/main..HEAD
# two commits: the consumer template, then this handoff
$ git ls-remote --tags origin | wc -l
0
```

**Do this first, and confirm it before anything else:**

```console
$ cd ~/projects/crew-sandbox
$ git push origin main
$ git push origin v0.1.0
$ git ls-remote --tags origin          # must now list v0.1.0
$ git rev-parse v0.1.0^{commit}        # record this hash; step 3 pins to the tag
```

The tag is annotated and points at the commit that carries this file. Do not hardcode a
hash from this document — read it from `git rev-parse` and use the tag name in the flake
input. If `git ls-remote --tags origin` still shows nothing after the push, stop and
report it rather than pinning to `main`.

---

## 1. What the two repositories are

| Repo | Path | Role |
| --- | --- | --- |
| `aditya-sathish-rao/crew-sandbox` | `~/projects/crew-sandbox` | The extracted tool. **Done.** Tagged `v0.1.0`. |
| `aditya-sathish-rao/agentic-dev-env` | not cloned yet | The dotfiles. Still carries its own copy at `nix/crew-sandbox/`. **This is what you change.** |

`agentic-dev-env` is the consumer. The cutover replaces its in-tree copy with a flake
input pinned to `crew-sandbox`'s tag.

Clone it somewhere outside the crew-sandbox checkout:

```console
$ gh repo clone aditya-sathish-rao/agentic-dev-env ~/projects/agentic-dev-env
```

Note: cloning triggers an Automic Vault approval prompt. That is expected; approve it.

---

## 2. What `crew-sandbox` already gives you

Do not rebuild any of this. It exists, is tested, and is tagged.

**Flake outputs** (`~/projects/crew-sandbox/flake.nix`):

| Output | What it is |
| --- | --- |
| `packages.<system>.default` | The Go CLI, `crew-sandbox` |
| `homeManagerModules.default` | The guest's home-manager module (`nix/guest/`) |
| `darwinModules.default` | The host module: proxy + broker as launchd **user agents** (`nix/modules/darwin.nix`) |
| `checks.unit` | Go unit tests |
| `checks.selftest` | Both `--selftest` runs + guest config evaluation |
| `checks.darwin` | Host module evaluation |
| `checks.examples` | The example consumer, evaluated |
| `checks.formatting` | prettier on the two `.mjs` files |

**The template to copy** is `~/projects/crew-sandbox/examples/consumer/`:

- `flake.nix` — inputs and the two outputs
- `home.nix` — the fleet, passed as `extraPackages`
- `darwin.nix` — provider upstream, vault secret name, App ID

`checks.examples` evaluates this template against the real modules, so it cannot drift.

**The guest module takes `extraPackages`.** It installs no tools of its own. The old
`guest.nix` hardcoded `piPackage` and imported `../crew-tools`; the new one does neither.

---

## 3. The cutover, step by step

### 3.1 Add the input

In `~/projects/agentic-dev-env/flake.nix`, add to `inputs`:

```nix
crew-sandbox.url = "github:aditya-sathish-rao/crew-sandbox/v0.1.0";
```

Pin to the **tag**, not `main`. Then `nix flake lock` to record it.

### 3.2 Replace the package output

Current (`flake.nix:71-74`):

```nix
crew-sandbox = import ./nix/crew-sandbox {
  pkgs = systemPkgs;
  piPackage = pi.packages.${system}.default;
};
```

Replace with the input's package:

```nix
crew-sandbox = crew-sandbox.packages.${system}.default;
```

Add `crew-sandbox` to the `outputs` function's argument set (it is currently `...`).

### 3.3 Replace the guest configuration

Current (`flake.nix:84-91`):

```nix
homeConfigurations."agent@aarch64-linux" = home-manager.lib.homeManagerConfiguration {
  pkgs = nixpkgs.legacyPackages.aarch64-linux;
  modules = [ ./nix/crew-sandbox/guest.nix ];
  extraSpecialArgs = {
    piPackage = pi.packages.aarch64-linux.default;
  };
};
```

Replace with the input's module plus the fleet the old `guest.nix` installed:

```nix
homeConfigurations."agent@aarch64-linux" = home-manager.lib.homeManagerConfiguration {
  pkgs = nixpkgs.legacyPackages.aarch64-linux;
  modules = [ crew-sandbox.homeManagerModules.default ];
  extraSpecialArgs = {
    extraPackages = [
      pi.packages.aarch64-linux.default
    ] ++ builtins.attrValues (import ./nix/crew-tools/default.nix {
      pkgs = nixpkgs.legacyPackages.aarch64-linux;
    });
  };
};
```

The old `guest.nix` installed exactly `piPackage` plus `crew-tools`; this preserves that
set. Verify by reading `~/projects/agentic-dev-env/nix/crew-sandbox/guest.nix` before
deleting it.

### 3.4 Wire the host services

`agentic-dev-env` currently runs **no** proxy or broker daemon — the old CLI started them
in the foreground. The new module runs them as launchd user agents.

`configuration.nix` does **not** see `inputs`: it is handed
`specialArgs = { inherit identity automic-vault-tap; }` (`flake.nix:93`). So the module
goes in the `modules` list in `flake.nix`, not in `configuration.nix`:

```nix
darwinConfigurations.${identity.host} = nix-darwin.lib.darwinSystem {
  specialArgs = { inherit identity automic-vault-tap; };
  modules = [
    nix-homebrew.darwinModules.nix-homebrew
    ./configuration.nix
    ./tailscale.nix
    home-manager.darwinModules.home-manager
    crew-sandbox.darwinModules.default          # <-- add
    {
      system.primaryUser = identity.username;   # <-- add
      services.crew-sandbox = {                 # <-- add
        enable = true;
        package = crew-sandbox.packages.aarch64-darwin.default;
        proxy.upstream = "https://api.deepseek.com";
        proxy.keySecret = "DEEPSEEK_API_KEY";
        broker.appID = "5853565";
      };
    }
    { /* existing home-manager block */ }
  ];
};
```

Two values to confirm rather than copy blindly:

- `proxy.keySecret` — the old code read `DEEPSEEK_API_KEY`. The new module defaults to
  `CREW_MODEL_KEY`. Use whichever name the vault actually holds.
- `broker.appID` — `5853565` is the value in the old `default.nix`. Confirm it is still
  the App you want.

`system.primaryUser` is required by nix-darwin for `launchd.user.agents`. `identity.nix`
already computes the username.

### 3.5 Delete the old copy

```console
$ git rm -r nix/crew-sandbox
```

Then fix every reference. Known ones:

| File | What to change |
| --- | --- |
| `flake.nix` | The three blocks in §3.2–3.4 |
| `GLOSSARY.md:284` | Points at `nix/crew-sandbox/lima.yaml` |
| `docs/adr/0010-crew-runs-in-one-vm.md:31` | Points at `nix/crew-sandbox/lima.yaml` |
| `docs/README.md:300,339,367,376,465` | Points at the old paths |
| `.lavish/crew-sandbox-*.md` | Historical notes; leave them, they are archived context |

Repoint the docs at the new repo, or at the flake input. Do not leave dead links.

The check in §5 step 5 finds these. Run it before you think you are done — it caught
`GLOSSARY.md`, which is easy to miss.

### 3.6 Run the gates

The old repo's gates are gone with the directory. The new ones live in the package:

```console
$ nix run .#crew-sandbox -- check all \
    --expected-tools "pi gh" \
    --model-upstream https://api.deepseek.com \
    --app-id 5853565 \
    --other-repo aditya-sathish-rao/agentic-dev-env
```

This needs a running guest and vault approvals. Expect prompts.

---

## 4. Known gaps — read before you promise anything

These are real differences between the old CLI and the new one. Decide each explicitly;
do not let them pass silently.

### 4.1 The new CLI has no `kill`, `ruleset`, `shell`, or `apply`

| Old command | New equivalent | Status |
| --- | --- | --- |
| `kill` | `down` — stops the guest, **then** revokes broker tokens | Covered, renamed |
| `shell` | `run -- <cmd>` | No interactive shell; `run` requires a command |
| `ruleset <repo>` | none | **Missing.** The ruleset JSON ships at `verify/gates/broker-ruleset.json` but nothing applies it |
| `apply` | none | Deliberate — see README "Applying it to the guest" |

`kill` → `down` is fine: `cmdDown` stops first, then revokes, for the same reason the old
`kill` did. But `ruleset` has no replacement, and the branch ruleset is the human-merge
backstop the whole broker design leans on. Either apply it by hand with `gh api`, or add
the command to `crew-sandbox`.

### 4.2 The instance name changed

Old: `crew`. New: `crew-sandbox` (the `--name` default). Any script, doc, or muscle memory
that says `limactl shell crew` needs updating. The new module's `instanceName` option
defaults to `crew-sandbox` and must match.

### 4.3 Assertion counts are not comparable

The old gates printed 56 assertions across 6 files. The new ones print 81 across 7 phases,
but the phases were restructured (`box` split into `guest` + `root`, `redteam` added), so
the numbers do not line up one-to-one. The migration plan's "compare assertion counts" box
should be closed as *not comparable*, with the reason recorded — not ticked as if the
numbers matched.

### 4.4 The old repo never ran the host services

`configuration.nix` has no proxy or broker daemon. Step 3.4 is new behaviour, not a port.
The first `darwin-rebuild switch` after this will start two services that were not running
before. Watch the logs at `~/Library/Logs/crew-sandbox/`.

---

## 5. Verification checklist

Run these in order. Do not skip to the end.

```console
# 1. the tag is on the remote and points where you think
$ git -C ~/projects/crew-sandbox ls-remote --tags origin
$ git -C ~/projects/crew-sandbox rev-parse v0.1.0^{commit}

# 2. the input resolves
$ cd ~/projects/agentic-dev-env && nix flake lock
$ nix flake show --impure 2>&1 | head -40

# 3. the configuration still evaluates
$ nix eval --impure --raw .#darwinConfigurations.<host>.config.system.build.toplevel.drvPath

# 4. the repo's own checks pass
$ nix develop --impure --command ./.githooks/check --all

# 5. no reference to the deleted directory survives
$ grep -rn "nix/crew-sandbox" --include=*.nix --include=*.md --include=*.sh . | grep -v '^./.lavish/'

# 6. the gates run against a real guest
$ nix run .#crew-sandbox -- check all --expected-tools "pi gh" ...
```

Step 5 must print nothing outside `.lavish/`.

---

## 6. What "done" means

- [ ] `crew-sandbox` `main` and `v0.1.0` are pushed
- [ ] `agentic-dev-env` has `crew-sandbox` as a flake input pinned to `v0.1.0`
- [ ] `packages.crew-sandbox` comes from the input
- [ ] `homeConfigurations."agent@aarch64-linux"` uses `homeManagerModules.default` with the fleet as `extraPackages`
- [ ] `services.crew-sandbox.enable` is set, with the real provider and App ID
- [ ] `nix/crew-sandbox/` is deleted and no reference to it survives outside `.lavish/`
- [ ] `./.githooks/check --all` passes
- [ ] `crew-sandbox check all` passes against a real guest
- [ ] The `ruleset` gap is either closed or recorded as a known limitation

Then update `~/projects/crew-sandbox/docs/extract-crew-sandbox.md` §7 and close the
remaining boxes.

---

## 7. Things not to do

- **Do not** re-implement the proxy, broker, gates, or guest module. They are done and
  tagged. If something looks missing, check `~/projects/crew-sandbox` first.
- **Do not** pin the input to `main`. Pin to `v0.1.0`.
- **Do not** delete `.lavish/` — it is archived design context, deliberately kept.
- **Do not** tick the "compare assertion counts" box as if the numbers matched. They do
  not; see §4.3.
- **Do not** run the gates from CI. They need a real guest, real secrets, and
  `vmType: vz` cannot nest in a hosted runner. See README "Where the gates run".
