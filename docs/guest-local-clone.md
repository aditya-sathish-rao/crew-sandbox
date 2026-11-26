# Guest-local clone instead of a host mount

Status: proposed. No code yet. This is the design for replacing `--writable` with a
workflow that never mounts a host directory read-write.

## The problem it removes

`--workspace DIR --writable` mounts a host directory into the guest read-write. The
agent can then write into it, and that is host code execution: `.git/hooks/*`,
`.git/config` (`core.fsmonitor`, `core.hooksPath`), a `Makefile`, an `.envrc`, or a
`package.json` script all execute on the operator's machine the next time the
operator runs `git`, `make`, `direnv` or `npm` in that directory. Nothing in the
guest boundary can prevent it, because the code runs outside the boundary.

Read-only mounts are unaffected by this: the guest cannot write, so it cannot plant
anything. That is why the red team's plant-in-workspace test passes today - it is
testing a read-only mount.

## What this design does

The agent never gets a host directory. It gets a clone inside the guest, and changes
leave through the broker, which already holds a repository-scoped token.

```
host                                   guest
------------------------------------   ---------------------------------------
operator                               agent
  | crew-sandbox run --repo OWNER/NAME    |
  |   -- sh -c "..."                      |
  |-------------------------------------->|
  |                                       clone OWNER/NAME from the broker's mirror
  |                                       work, commit
  |                                       push to refs/heads/agent/<task>
  |                                         (token scoped to OWNER/NAME)
  |<--------------------------------------|
  operator reviews agent/<task>, merges or discards
```

Three ways to bring work out, in order of preference:

1. **A branch pushed to the same repository**, `agent/<task>`, created with the
   token the broker already mints. The operator reviews a normal PR. This needs no
   new credential: the broker's tokens are already repository-scoped and already
   refused the default branch.
2. **A patch sent to the broker**, `POST /workspace/<task>/patch`, for repositories
   the agent cannot push to. The broker writes it to a host directory the operator
   names at startup - never a directory the guest chose.
3. **Read-only inspection** of an existing working tree, which is what
   `--workspace` without `--writable` already does, and is safe as it stands.

## Why the broker is the right seam

It already exists, already authenticates to GitHub as the App, already scopes a
token to one repository, and already refuses to let a token touch the default
branch. Pushing a branch is exactly the operation it was built to authorize, and it
keeps the "the guest holds no long-lived credential" property: the token is minted
for the push and revoked at teardown.

The alternative - mounting the host's working tree read-only and letting the agent
copy it in - keeps the review step but drags the entire host tree through the
mount. It also leaks the contents of whatever else the operator has in that
checkout (untracked files, `.env`). Cloning from the broker's token is cleaner.

## Changes by file

| File | Change |
| --- | --- |
| `cmd/crew-sandbox/main.go` | `--repository OWNER/NAME` alongside `--workspace`. With `--repository`, no host mount is created at all; the guest clones into `/home/agent/work/<task>`. `--workspace --writable` keeps working but prints the host-code-execution warning it now prints. |
| `internal/lima/lima.go` | No change. Mounts are already optional and exact-matched; this design simply stops using the writable case. |
| `internal/broker/broker.mjs` | Add `POST /workspace/<task>/patch` for the patch path, which accepts a `git format-patch` stream up to a size cap and writes it under the operator-configured directory. The token path needs no change. |
| `internal/broker/broker.go` | Pass the patch directory to the child as `CREW_PATCH_DIR`; refuse to start with the patch route enabled and no directory. |
| `internal/lima/lima.yaml` | The guest needs `git` in the provisioned toolchain. |
| `verify/gates/` | `guest` gains a case asserting no host path is mounted in repository mode; `broker` gains the patch route's allowlist and size-cap cases. |
| `README.md`, `SECURITY.md` | Replace the `--writable` warning with the workflow, and state that the review step is where the boundary ends - an agent's branch is still code. |

## What the operator experiences

```console
$ crew-sandbox run --repo acme/widget -- claude -p "fix the flaky test"
...
crew-sandbox: pushed agent/crew-7f3a to acme/widget; review it with:
  gh pr create --head agent/crew-7f3a
```

The task id is already validated (`[A-Za-z0-9._-]{1,128}`), so it can be used
directly as a ref component.

## Open questions

- **Where does the clone come from?** Cloning over https through the egress proxy
  needs `github.com` on the allowlist, which it already is for the guest's tools.
  Alternatively the broker can serve a bundle from a host-side cache, which is
  faster and works offline, at the cost of another endpoint.
- **What happens to the clone at teardown?** `destroy` already discards the disk.
  `down` keeps it, so a stopped guest keeps uncommitted work - probably right, but
  it should be stated rather than discovered.
- **Multi-repository tasks.** Today a token is scoped to one repository. A task
  touching two repositories would need two tokens and two branches, or a decision
  to refuse it. Refusing is simpler and should be the default.
- **Does this replace `--writable`?** Proposed: yes, deprecate it in one release
  with the warning it now prints, then remove it. Keeping both forever means the
  dangerous path stays one flag away.

## What this does not fix

An agent can still push a branch containing whatever it likes, and a human still
has to review it. That is the point - the review is the boundary, and the design
makes the review the only way code reaches the host rather than one option among
several.
