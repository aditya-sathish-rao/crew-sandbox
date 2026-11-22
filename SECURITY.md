# Threat Model & Security Architecture

## 1. Protected Assets

- Long-lived credentials stored in Atomic Vault / host keychain (LLM API keys, GitHub tokens, cloud credentials).
- Host filesystem outside the explicitly mounted project workspace (`~/.ssh`, `~/.aws`, browser profiles, unrelated repos).
- Host network services and internal LAN endpoints.

## 2. Threat Actors & Entry Points

- **Compromised agent session via indirect prompt injection:** Malicious instructions embedded in a cloned repo, README, issue description, or dependency file.
- **Over-permissive tool execution:** An agent executing destructive shell commands or exfiltrating files via `curl`, DNS, or git remotes.

## 3. Trust Boundaries & Enforced Invariants

1. **Credential Isolation:** Secrets remain in host memory/Atomic Vault. The container never receives raw long-lived tokens in `env` or mounted files; upstream requests are brokered/proxied by the host with scoped permissions.
2. **Filesystem Scoping:** Only the target project directory is bind-mounted into the container. Host home directories and credential stores are unmounted.
3. **Network Egress Control:** Default-deny outbound traffic except explicit allowlisted endpoints (e.g., approved model APIs and package registries).
4. **Privilege Reduction:** Containers run non-root with dropped Linux capabilities and read-only root filesystems where possible.

## 4. Verification & Red-Team Findings

Each security invariant maps to an automated test in `verify/`:

- `verify/gates/env_audit`: Asserts zero secret patterns exist in container process environments or `/proc`.
- `verify/gates/fs_boundary`: Verifies attempts to traverse or read host credential paths fail.
- `verify/redteam/exfil_sim`: Simulates prompt-injected exfiltration attempts (env dump, unauthorized HTTP/DNS egress) and confirms containment.

_(Document 2–3 real failures you caught during development—such as DNS egress gaps, header forwarding issues, or log spoofing—along with how you fixed them.)_

## 5. Non-Goals & Known Limitations

- **Host-side execution of agent output:** If an agent writes malicious build scripts or git hooks into the mounted workspace and you run them directly on the host outside the sandbox, containment ends.
- **Container runtime / VM boundary:** Relies on the underlying container/VM runtime isolation guarantees on macOS (`nix-darwin`) and Linux.
- **Defense in depth:** Designed to contain blast radius and require explicit intent for sensitive actions, not to provide formal cryptographic isolation.
