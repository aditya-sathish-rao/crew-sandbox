# Graph Report - crew-sandbox  (2026-11-24)

## Corpus Check
- 35 files · ~50,565 words
- Verdict: corpus is large enough that graph structure adds value.
- Unclassified: 14 file(s) not represented in the graph (top: .nix 8, (none) 3, .example 2)

## Summary
- 338 nodes · 707 edges · 20 communities (19 shown, 1 thin omitted)
- Extraction: 91% EXTRACTED · 9% INFERRED · 0% AMBIGUOUS · INFERRED: 65 edges (avg confidence: 0.83)
- Token cost: 0 input · 0 output

## Graph Freshness
- Built from commit: `d1b14f65`
- Run `git rev-parse HEAD` and compare to check if the graph is stale.
- Run `graphify update .` after code changes (no API cost).

## Community Hubs (Navigation)
- proxy_test.go
- testing.T
- proxy.mjs
- lima.yaml
- main.go
- lima.go
- check.sh
- GitHub Actions Workflow
- Vault Provider Configuration
- App JWT Broker
- Red Team Exfiltration
- Broker Script Workflow
- Model Provider Config
- Model Script Test Outcomes
- Default Model Settings
- Guest Shell Script
- Proxy Shell Script
- Toolchain Shell Script
- Root Shell Script
- Crew Sandbox Repository

## God Nodes (most connected - your core abstractions)
1. `Guest` - 13 edges
2. `Command()` - 11 edges
3. `run()` - 10 edges
4. `cmdProxy()` - 10 edges
5. `check.sh script` - 9 edges
6. `require_host_tools()` - 8 edges
7. `stage()` - 8 edges
8. `phase_proxy()` - 8 edges
9. `phase_model()` - 8 edges
10. `redteam.sh script` - 8 edges

## Surprising Connections (you probably didn't know these)
- `cmdDown()` --calls--> `RevokeAll()`  [EXTRACTED]
  cmd/crew-sandbox/main.go → internal/broker/client.go
- `cmdProxy()` --calls--> `Command()`  [EXTRACTED]
  cmd/crew-sandbox/main.go → internal/proxy/proxy.go
- `cmdProxy()` --calls--> `SelfTest()`  [EXTRACTED]
  cmd/crew-sandbox/main.go → internal/proxy/proxy.go
- `cmdCheck()` --calls--> `Stage()`  [EXTRACTED]
  cmd/crew-sandbox/main.go → verify/verify.go
- `secret()` --calls--> `Chain()`  [EXTRACTED]
  cmd/crew-sandbox/main.go → internal/vault/vault.go

## Import Cycles
- None detected.

## Communities (20 total, 1 thin omitted)

### Community 0 - "proxy_test.go"
Cohesion: 0.08
Nodes (26): Revoked, TestScriptParses(), RevokeAll(), TestRevokeAll(), TestRevokeAllReportsError(), fakeCreatingLimactl(), TestCreateStartsTheInstance(), Command() (+18 more)

### Community 1 - "testing.T"
Cohesion: 0.12
Nodes (26): HostAllowed(), Parse(), ParseChecked(), RepoAllowed(), TestEmbeddedEgressNamesTheGitHubAPI(), TestEmbeddedFilesAreValid(), TestHostAllowedCaseAndTrailingDot(), TestHostAllowedDotMatchesSubdomains() (+18 more)

### Community 2 - "proxy.mjs"
Cohesion: 0.09
Nodes (24): RFC-7230, allowFile, argv, deny(), handleModel(), hopByHop, isAllowedHost(), isAllowedPort() (+16 more)

### Community 3 - "lima.yaml"
Cohesion: 0.06
Nodes (30): arch, containerd, system, user, cpus, disk, images, digest (+22 more)

### Community 4 - "main.go"
Cohesion: 0.17
Nodes (19): Config, Process, cmdBroker(), cmdCheck(), cmdDestroy(), cmdDown(), cmdProxy(), cmdRun() (+11 more)

### Community 5 - "lima.go"
Cohesion: 0.17
Nodes (10): fakeLimactl(), isNotInstalled(), mountsSatisfied(), TestStartAcceptsAlreadyAppliedMount(), TestStartRefusesMountsOnExistingInstance(), TestStartRefusesWritableMountWhenReadOnlyRequested(), TestStartWithoutMountsIsIdempotent(), Guest (+2 more)

### Community 6 - "check.sh"
Cohesion: 0.24
Nodes (24): build_proxy_args(), cleanup(), ensure_gate_dir(), flunk(), free_host_port(), pass(), phase_broker(), phase_guest() (+16 more)

### Community 7 - "GitHub Actions Workflow"
Cohesion: 0.09
Nodes (22): concurrency, cancel-in-progress, group, name, runs-on, steps, timeout-minutes, jobs (+14 more)

### Community 8 - "Vault Provider Configuration"
Cohesion: 0.16
Nodes (7): unavailable(), AtomicVault, chain, errProvider, FromEnv, Provider, Static

### Community 9 - "App JWT Broker"
Cohesion: 0.21
Nodes (12): api(), appJwt(), base64url(), installationId(), issued, mint(), port, privateKeyObject (+4 more)

### Community 10 - "Red Team Exfiltration"
Cohesion: 0.45
Nodes (10): escalate(), exfiltrate(), find_a_key(), plant_in_workspace(), push(), reach_host_services(), read_host_home(), record() (+2 more)

### Community 11 - "Broker Script Workflow"
Cohesion: 0.48
Nodes (5): flunk(), gh_api(), pass(), broker.sh script, with_branch()

### Community 12 - "Model Provider Config"
Cohesion: 0.33
Nodes (5): apiKey, baseUrl, providers, deepseek, $schema

### Community 13 - "Model Script Test Outcomes"
Cohesion: 0.67
Nodes (5): flunk(), missing(), pass(), model.sh script, skip()

### Community 14 - "Default Model Settings"
Cohesion: 0.40
Nodes (4): defaultModel, defaultProvider, defaultThinkingLevel, $schema

### Community 15 - "Guest Shell Script"
Cohesion: 0.90
Nodes (4): expect_fail(), flunk(), pass(), guest.sh script

### Community 16 - "Proxy Shell Script"
Cohesion: 0.70
Nodes (4): answer_for(), flunk(), pass(), proxy.sh script

### Community 17 - "Toolchain Shell Script"
Cohesion: 0.70
Nodes (4): flunk(), from_store(), pass(), toolchain.sh script

### Community 18 - "Root Shell Script"
Cohesion: 0.83
Nodes (3): flunk(), pass(), root.sh script

## Knowledge Gaps
- **2 isolated node(s):** `github.com/aditya-sathish-rao/crew-sandbox`, `RFC-7230`
  These have ≤1 connection - possible missing edges. (Counts symbols only; 77 node(s) total have ≤1 connection when file, concept and rationale nodes are included.)
- **1 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **Why does `Guest` connect `lima.go` to `main.go`?**
  _High betweenness centrality (0.017) - this node is a cross-community bridge._
- **Are the 4 inferred relationships involving `Command()` (e.g. with `TestCommandPassesUpstreamOnlyWhenSet()` and `TestCommandStartsWithoutProvider()`) actually correct?**
  _`Command()` has 4 INFERRED edges - model-reasoned connections that need verification._
- **What connects `github.com/aditya-sathish-rao/crew-sandbox`, `RFC-7230` to the rest of the system?**
  _2 weakly-connected nodes found - possible missing edges._
- **Should `proxy_test.go` be split into smaller, more focused modules?**
  _Cohesion score 0.07529411764705882 - nodes in this community are weakly interconnected._
- **Why does `Command()` connect `proxy_test.go` to `testing.T`, `main.go`?**
  _High betweenness centrality (0.007) - this node is a cross-community bridge._
- **Should `testing.T` be split into smaller, more focused modules?**
  _Cohesion score 0.12310606060606061 - nodes in this community are weakly interconnected._
- **Should `proxy.mjs` be split into smaller, more focused modules?**
  _Cohesion score 0.09462365591397849 - nodes in this community are weakly interconnected._