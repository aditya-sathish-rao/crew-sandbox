# Threat Model & Security Architecture

This describes the system as it is built, not as it was originally sketched. The
boundary is a Linux VM, not a container: there is no bind mount by default, no
capability set to drop, and no read-only root filesystem to configure. Those
controls belong to a container runtime and do not exist here. What exists is a
guest kernel, a default-deny firewall, and two host services that hold the only
credentials.

## 1. Protected assets

- **Long-lived credentials.** The GitHub App private key and the model API key.
  They live in Atomic Vault on the host and are read only by the broker and the
  proxy. The guest never receives either.
- **The host filesystem.** Everything outside the guest's own disk: `$HOME`,
  `~/.ssh`, browser profiles, other repositories. The guest has no mount to it.
- **Host network services and the LAN.** The guest's only route out is the proxy
  port on the host gateway.
- **The host's own loopback.** A listener inside the guest must not become a
  listener on the host. Lima's port forwarding is disabled for this reason.

## 2. Threat actors and entry points

- **Indirect prompt injection.** Untrusted repository content — a README, an issue
  body, a dependency's install script — that instructs the agent to exfiltrate or
  to reach a host it should not. This is the primary threat: the agent is assumed
  to be fully compromised by the content it reads.
- **A compromised agent process.** Once injected, the agent runs as the guest's
  `agent` user and will try to read credentials, reach the network, escalate to
  root, or reach the host.
- **The operator's own mistakes.** A widened allowlist, a reopened broker port, a
  workspace mounted writable. These are in scope because they are the realistic
  way the boundary degrades.

Out of scope: a compromised host kernel or hypervisor, a malicious Lima release,
and an attacker who already has the operator's host account.

## 3. Trust boundaries and enforced invariants

### 3.1 The guest kernel is the boundary

The guest is a Lima VM (`vmType: vz`, 4 CPU, 6 GiB, 30 GiB disk) running an Ubuntu
LTS image pinned by digest. A rebuild runs the bytes the definition names. The
guest's disk is the only place untrusted code runs.

### 3.2 Nothing from the host is mounted

`mounts: []` in the embedded definition. The guest has no virtiofs or 9p mount unless
`--workspace` is given, so `/Users` does not exist inside it and there is no host
directory with host permissions reachable from the guest.

A workspace mount is the one deliberate exception and it is opt-in per run:
`crew-sandbox run --workspace DIR`. It is **read-only unless `--writable` is
passed**, and it is applied only when the VM is created. Lima fixes mounts at
creation time, so asking for a workspace on an existing VM is refused rather than
silently ignored — running the command without the workspace would be a quiet lie
about what the guest can see.

### 3.3 Egress is default-deny, with one door

The guest's nftables ruleset drops all input and output except loopback and
established connections. The only outbound rule is the proxy port on the host
gateway:

```
ip daddr $gw tcp dport 3128 accept
```

The broker's port 3129 is deliberately absent. The broker authenticates no caller,
so while the port is open any crewmate could mint a GitHub token. It stays closed
until per-task authentication exists. The guest's firewall configuration is
root-owned and not writable by `agent`.

### 3.4 The proxy is the only way out, and it names hosts

The host-side proxy is a CONNECT proxy on 127.0.0.1:3128. It admits only the
hostnames in the egress allowlist — `internal/allowlist/egress-allow.example` unless
`--egress-allow` supplies one — and only on ports 443 and 80. Two properties make the
allowlist mean what it says:

- **Plain HTTP:** the guest's `Host` header is discarded and set from the
  destination, so a request cannot be retargeted to another site on the same CDN
  address.
- **TLS:** the proxy reads the ClientHello and refuses the tunnel unless the SNI
  name is exactly the name that was allowed. A CONNECT proxy cannot see inside
  TLS, so without this check an allowlisted CDN address would reach any site
  behind that CDN. A malformed handshake costs one tunnel, not the process.

The model gateway is a separate route on the same proxy. It holds the model key and
replaces the `Authorization` header, so the guest's model client reaches the provider
without ever holding the key. The route is closed unless a key is configured.

The gateway is provider-agnostic. The upstream URL and the secret name are settings,
not constants, so the same gateway serves any provider and no vendor is hardcoded.
With no upstream configured the gateway is disabled and the vault is not consulted.

### 3.5 Credentials are brokered, never handed over

The broker mints GitHub App installation tokens scoped to one repository and one
task. Tokens are memory-only: a restart strands them. The private key stays in the
vault and is passed to the broker's child process through its environment, never
argv. The model key stays in the proxy. The guest's own model token is a random
value generated at first boot and readable only inside the guest; it identifies the
guest to the proxy and is not a credential to anything else.

### 3.6 Privilege is reduced by construction

The guest's `agent` user has no sudo. The firewall ruleset cannot be read or
flushed by `agent`, and its configuration file is not writable. The admin account
(`crew`) exists for provisioning and is not the account the fleet runs as. These
are asserted as capabilities — what the guest *can do* — not as configuration, so
re-enabling sudo or reopening egress fails the gate.

### 3.7 The host's loopback stays the host's

Lima's default port forwarding publishes every guest listener on the host's
127.0.0.1. The definition overrides it with an ignore rule covering all addresses,
ports and protocols. Without that, a listener started inside the guest would be
reachable from the host as if it were a host service.

## 4. Verification

The gates are phases in `verify/gates/check.sh`, each backed by a script in the same
directory, and `crew-sandbox check <phase>` runs them. They are embedded in the operator
binary, so the gates that run are the gates that were built.

| Invariant | Phase | What it asserts |
| --- | --- | --- |
| 3.2 no host mount | `guest` | `/Users` absent; no 9p or virtiofs mount |
| 3.3 default-deny egress | `guest` | arbitrary host, arbitrary resolver, and the host on any port but 3128 all fail |
| 3.3 broker port closed | `root` | the token broker is not reachable from the guest |
| 3.4 allowlist names hosts | `proxy` | allowlisted host answers; a host off the allowlist is refused over https and http; a spoofed `Host` cannot retarget a plain-HTTP request; a tunnel whose SNI names another host is refused; CONNECT is refused on port 80 |
| 3.5 no credential in the guest | `model` | no environment value is the model key; the gateway token is not a model key; the gateway refuses a wrong token and a path outside the model route |
| 3.6 reduced privilege | `guest`, `root` | sudo unavailable; ruleset not readable, not flushable, not writable; `agent` in no privileged group and in no sudoers rule |
| 3.7 no host forwarding | `guest` | a guest listener on 0.0.0.0 and on 127.0.0.1 stays off the host |
| 3.5 broker mints per task | `broker` | a repository outside the allowlist is refused before minting; a minted token sees only its own repository, cannot write the default branch, and stops working after a revoke |
| the fleet is installed | `toolchain` | `nix` resolves under `/nix/store` and the store verifies |
| all of the above, adversarially | `redteam` | seven scripted attacks — host filesystem, credentials, exfiltration, pushes, host services, sudo, planted hooks — each blocked with the evidence printed |

`check all` runs every phase in one process, because a boundary that holds in isolation
can still fail in combination.

The gate is told the facts it cannot know — the fleet's tools, the model provider's base
URL, the App ID, the allowlists — because none of them belongs to this repository. A
phase whose input is missing fails with the reason instead of passing over an assertion
it could not make.

`check redteam` is the adversarial half: a script in `verify/redteam/` that assumes the
boundary is broken and goes looking for the way through. It reports `BLOCKED`, `ESCAPED`
or `SKIP` per attempt rather than pass/fail, and `--redteam-log FILE` keeps the transcript
— the evidence an ADR cites. It never prints a secret value, only the name or path where
one was found.

The proxy and broker each carry a `--selftest` that runs without a VM: seven
ClientHello parser cases for the proxy, and the signing path for the broker. Those run
in `nix flake check` today.

## 5. Failures found during development

These are real defects caught while building the original system, kept here because
each one is a way the boundary looked correct and was not.

**The allowlist named addresses, not hosts.** The proxy validated the CONNECT
destination and then spliced bytes without reading the TLS handshake. An allowlisted
CDN address served any site behind that CDN, so the allowlist was effectively "any
host on these CDNs". Fixed by parsing the ClientHello and requiring the SNI to equal
the allowed name. A crafted handshake could also throw out of the socket handler and
kill the proxy; the parse is now bounded and wrapped.

**The broker port was open to the guest.** The firewall admitted 3129, and the
broker authenticated no caller. Any crewmate could mint a GitHub token for any
repository in the allowlist. Fixed by removing 3129 from the output chain; the port
stays closed until per-task authentication exists.

**Gate scripts were staged in an agent-writable directory.** The gate copied its
scripts to `/tmp` and ran them as root. A crewmate could pre-create the path. It
failed closed — rsync refused to clobber — but the failure read as broken
infrastructure rather than tampering. Fixed by staging into a root-owned directory
on tmpfs and treating a failed copy as a named tampering failure.

## 6. Non-goals and known limitations

- **Everything runs in one guest.** Commands share the same kernel boundary and are
  not separated from each other. A compromise of one is contained by the VM, but not
  from the others inside it.
- **A CONNECT proxy cannot see inside TLS.** The guarantee is "only allowlisted
  hostnames", not "only their ordinary traffic". A client that skips certificate
  validation can present an allowed SNI and talk to whatever answers.
- **The workspace mount is a real widening.** A writable mount lets the guest modify
  host files. It is read-only by default for that reason.
- **No per-task authentication on the broker.** Until it exists, the broker is an
  operator tool on host loopback and its port stays closed to the guest.
- **The host is trusted.** A compromised host kernel, hypervisor or Lima release is
  outside the model. So is an attacker who already has the operator's account.
