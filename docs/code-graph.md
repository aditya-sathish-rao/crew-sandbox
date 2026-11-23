# The committed code graph

`graphify-out/graph.json` is a symbol-level graph of this repository: every function, type,
method, embedded script and config key, plus the connections between them, grouped into
named communities. `graphify-out/GRAPH_REPORT.md` is the same graph written out as prose -
hub symbols, surprising connections, community membership and knowledge gaps. Both are
committed, so a fresh clone can answer "where does this happen?" and "what touches this?"
without building anything.

They are produced by [graphify](https://docs.graphify.com), which is not a dependency of the
flake, the devShell or the package set. Install it once on the machine doing the lookup:

```console
$ uv tool install "graphifyy[openai]"
```

The `openai` extra is what lets graphify name communities through a model provider. The
lookups below and `graphify update` are local: no key and no API spend.

## Lookups

Run these from the repo root and pass `--graph graphify-out/graph.json` explicitly. The
default is the same path, but naming it is what makes the answer come from the committed
file rather than from whatever a previous local run left behind.

| Command | The question it answers |
| --- | --- |
| `graphify query "<question>"` | Which symbols does this question touch, and how are they connected? |
| `graphify explain "<symbol>"` | What is this symbol, where does it live, and what points at it? |
| `graphify path "<A>" "<B>"` | What is the shortest chain of relationships between two symbols? |
| `graphify god-nodes` | Which symbols is the code most organised around? |
| `graphify affected "<symbol>"` | What has to be re-read or re-tested if this symbol changes? |

```console
$ graphify query "how does the egress proxy decide which hosts it admits" --graph graphify-out/graph.json
$ graphify explain "Guest" --graph graphify-out/graph.json
$ graphify path "cmdRun()" "Start()" --graph graphify-out/graph.json
$ graphify god-nodes --graph graphify-out/graph.json
$ graphify affected "Guest" --graph graphify-out/graph.json
```

A query answer is a set of nodes with their file, line and community, plus a note when the
result was truncated to the token budget (`--budget` raises it). `explain` and `affected`
print edges with their provenance: `EXTRACTED` edges come from the parser, `INFERRED` ones
are model guesses, so treat those as leads rather than facts. Query is a starting point, not
an answer: follow the nodes it names into the source.

## Refreshing it

`graphify update .` from the repo root re-extracts the code files whose content changed,
reclusters, and rewrites `graph.json` and `GRAPH_REPORT.md`. It is the local AST pass only,
so it costs no API calls and needs no key - run it in the same commit as any change to
`cmd/`, `internal/`, `verify/`, `nix/guest/*.json` or `.github/`.

```console
$ graphify update .
```

`graphify update` keeps the existing community labels. When the clustering itself has to be
rebuilt (a rename that moves symbols between communities, or labels that no longer describe
their members), re-cluster explicitly:

```console
$ graphify cluster-only .            # reclusters and relabels through the model provider
$ graphify cluster-only . --no-label # reclusters, keeps the labels already in graph.json
```

Either form also rewrites `graphify-out/graph.html`, the interactive view. It is not
committed - 309 KB of regenerable output that pulls vis-network from a CDN - so it is
ignored by `.gitignore` along with graphify's caches, its `manifest.json`, its
`.graphify_*` state and its dated regeneration backups. Only `graph.json` and
`GRAPH_REPORT.md` are in git.

To throw the extraction away and rebuild all of it from the sources (still local, still no
API cost):

```console
$ graphify extract . --code-only
```

`--code-only` is what keeps this graph about code: without it graphify also indexes
markdown, and a README edit would change the graph.

## Is it stale?

Two fields say what the committed graph was built from:

- `graph.json`'s `built_at_commit` records the commit whose sources were extracted.
- The date in `GRAPH_REPORT.md`'s title is the day the run happened.

Compare the first against `git rev-parse HEAD`. They will not match in the commit that
carries the regeneration itself, because a commit cannot contain its own hash: the graph is
built from `HEAD`, then committed on top of it. Any other mismatch means code moved after
the graph did, and the next change to those files should carry a `graphify update .`.

## What it does not cover

- **No Nix.** graphify has no grammar for Nix, so `flake.nix`, `nix/modules/`,
  `nix/packages/` and `nix/guest/` contribute no nodes at all. The two JSON files the guest
  module writes, `nix/guest/models.json` and `nix/guest/settings.json`, appear only through
  their own content (the `Model Provider Config` and `Default Model Settings` communities),
  not through the Nix options that place them. A question about wiring belongs in
  [`docs/darwin-module.md`](darwin-module.md), the README, or the module itself.
- **Symbols, not lines.** A node is a named thing with a file and a line, and an edge is a
  relationship between two of them. The graph will point at `isAllowedHost()` in
  `internal/proxy/proxy.mjs`; it will not tell you what that function's body does. Read the
  file for behaviour - the graph is the map, not the territory.
- **Doc prose.** Markdown is not extracted in a `--code-only` graph, so this file and the
  rest of `docs/` are invisible to it.
