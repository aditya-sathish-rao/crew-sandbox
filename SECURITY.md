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

### 3.3 Egress is default-deny, with two named doors

The guest's nftables ruleset drops all input and output except loopback and
established connections. The only outbound rules are the egress proxy and the model
gateway on the host gateway:

```
ip daddr $gw tcp dport 3128 accept
ip daddr $gw tcp dport 3130 accept
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
- **Addresses, not just names:** the proxy resolves an allowlisted name itself and
  refuses the tunnel or request if any answer is loopback, link-local (including the
  cloud metadata address), private, CGNAT or multicast, then connects to the address
  it checked rather than the name. A name that resolved somewhere harmless and is
  re-pointed before the connect therefore cannot reach the host - the connect never
  asks the resolver a second time. The model gateway's upstream is exempt on purpose:
  it is operator configuration, and a model server on the operator's own loopback is a
  legitimate upstream.
- **TLS:** the proxy reads the ClientHello and refuses the tunnel unless the SNI
  name is exactly the name that was allowed. A CONNECT proxy cannot see inside
  TLS, so without this check an allowlisted CDN address would reach any site
  behind that CDN. A malformed handshake costs one tunnel, not the process.
  A ClientHello that carries the `encrypted_client_hello` extension is refused
  outright: its outer SNI is a front for a name inside the encrypted payload, so
  the name the proxy checked would say nothing about where the client is going.
  This is what makes the allowlist mean something, but it is not a guarantee about
  what the client says inside the tunnel: a CDN that routes on the `Host` header
  rather than the SNI still allows domain fronting under an allowed name, and a
  client that skips certificate validation can talk to whatever answers on the
  allowlisted address. Both limits are stated below.

The model gateway is a separate listener on 127.0.0.1:3130, not a route on the
CONNECT proxy. It holds the model key and replaces the `Authorization` header, so the
guest's model client reaches the provider without ever holding the key. The listener
is not opened unless the key, the guest's token and an upstream are all configured.
Keeping it off the proxy's socket means a bug in either route cannot expose the
other, and the guest firewall names both ports explicitly. The gateway is bounded
because the guest's token is world-readable by design and so cannot be what limits
abuse: a request body is capped at 32 MiB, counted while it streams so a chunked body
is not exempt, and the token is limited to 120 requests a minute. A client that routes the
gateway through the proxy is still refused, which is why every login shell sets
`no_proxy=host.lima.internal`.

The gateway treats the provider as a setting rather than a constant: the upstream URL
and the secret name are configuration, so no vendor is hardcoded and switching
provider is a flag, not a code change. It speaks one surface, though - `GET /models`
and `POST /chat/completions`, authenticated with `Authorization: Bearer`, over https -
so it fronts OpenAI-compatible providers. A provider that authenticates with a
different header (`x-api-key`) or serves different routes needs a change here, not
just a flag. With no upstream configured the gateway is disabled and the vault is not
consulted.

### 3.5 Credentials are brokered, never handed over

The broker mints GitHub App installation tokens scoped to one repository and one
task. Tokens are memory-only: a restart strands them. `crew-sandbox down` stops the
guest first and then asks the broker to revoke every token it remembers, even when
the stop fails; a broker that is not running has none to revoke. The private key
stays in the vault and is passed to the broker's child process through its
environment, never argv. The model key stays in the proxy. The guest's own model
token is a random value generated at first boot and stored `root:root 0644`, so
every process in the guest can read it by design: the model client runs as `agent`.
It authenticates the guest to the host gateway and is not a credential to anything
else. Destroying and recreating the VM writes a new one; `up` on an existing VM does
not rotate it.

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
| 3.3 default-deny egress | `guest` | arbitrary host, arbitrary resolver, and the host on any port but 3128 and 3130 all fail |
| 3.3 broker port closed | `root` | the token broker is not reachable from the guest |
| 3.4 allowlist names hosts | `proxy` | allowlisted host answers; a host off the allowlist is refused over https and http; a spoofed `Host` cannot retarget a plain-HTTP request; a tunnel whose SNI names another host is refused; CONNECT is refused on port 80 |
| 3.5 no credential in the guest | `model` | no environment value is the model key; the gateway token is not a model key; the gateway refuses a wrong token and a path outside the model route |
| 3.6 reduced privilege | `guest`, `root` | sudo unavailable; ruleset not readable, not flushable, not writable; `agent` in no privileged group and in no sudoers rule |
| 3.7 no host forwarding | `guest` | a guest listener on 0.0.0.0 and on 127.0.0.1 stays off the host |
| 3.5 broker mints per task | `broker` | a repository outside the allowlist is refused before minting; a minted token sees only its own repository, cannot write the default branch, and stops working after a revoke |
| the fleet's tools resolve from the store | `toolchain` | `nix` resolves under `/nix/store`, and the store has no registration errors (a location check, not content integrity) |
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

The proxy and broker each carry a `--selftest` that runs without a VM: eight
ClientHello parser cases for the proxy, and the signing path for the broker. Those are
what `nix flake check` runs, along with the unit tests (which include `node --check` on
both scripts) and a prettier check; the gates above are not, because
a Nix build has no VM and no network.

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
- **The Nix store is owned by the agent.** The guest uses single-user Nix, so `/nix`
  and `/nix/store` belong to `agent` and a crewmate can replace the very tools it
  runs. The `toolchain` gate proves each tool *resolves under* `/nix/store`, and
  `nix-store --verify` checks registration and metadata, not file contents — and the
  agent runs both itself, so neither is a provenance or integrity guarantee. The pins
  stop *drift* (the guest and the host agree on what to install), not tampering from
  inside the boundary. The blast radius is the guest, which is already the untrusted
  side; a root-owned store with a daemon would be needed to make this an integrity
  boundary, and that contradicts the "no root afterwards" design.
- **The host services stop at logout.** `darwinModules.default` runs the egress proxy
  and the token broker as launchd *user agents*, because the vault will not hand a
  daemon a key without a human: every unattended `av inject` read was recorded as
  `Manual`, while only a tool-specific Authorization Gate answered `Auto`. So the
  services start at login and stop when the last user logs out, and a Mac that reboots
  unattended has no egress proxy until someone logs in. Running them as root daemons
  would need a vault policy that approves an exact signed executable without a human —
  a decision this repository does not make for the operator.
- **Phase 0 is not implemented here.** The plan's headless-host phase — never sleep,
  restart after a power cut, autologin — is host configuration this repository does
  not write. Until the operator's host configuration sets those, Phase 0's own gate
  (cut the power, come back) cannot be attempted.
- **Phase 4's fleet is the consumer's.** The guest module installs no tools and sets
  no git identity. A consumer supplies herdr, firstmate and the rest through
  `extraPackages`, and `crew-sandbox check toolchain --expected-tools …` asserts only
  that those names resolve under `/nix/store`. A guest with no fleet still passes
  every boundary gate, because the boundary does not depend on the fleet — so a
  passing `check toolchain` is not evidence that the fleet is installed.
- **Nothing applies the guest configuration for you.** This repository exports
  `homeManagerModules.default`; it does not run `home-manager switch`. Lima fixes
  mounts at creation and the guest has no host mount, so the consumer applies its own
  pinned `agent@aarch64-linux` generation from inside the guest, as `agent` — see the
  README's "Applying it to the guest". There is no `crew-sandbox apply` command. The
  `model` gate fails until `~/.pi/agent/models.json` and `settings.json` exist, which
  only a successful activation produces; it does not compare generation identities.
- **A CONNECT proxy cannot see inside TLS.** The guarantee is "only allowlisted
  hostnames", not "only their ordinary traffic". A client can use a permitted name
  for something else in two ways: it can skip certificate validation and talk to
  whatever answers on that address, or it can domain-front - present the allowed SNI
  and then name a different site in the `Host` header inside the tunnel, which some
  CDNs still route. The SNI check is what stops an allowlisted CDN address reaching
  arbitrary sites; it says nothing about what the client sends once the tunnel is
  open. Encrypted ClientHello used to be a third way, hiding the real name behind a
  permitted outer SNI; the handshake is now refused rather than admitted.
- **The workspace mount is a real widening.** A writable mount lets the guest modify
  host files. It is read-only by default for that reason.
- **No per-task authentication on the broker.** Until it exists, the broker is an
  operator tool on host loopback and its port stays closed to the guest.
- **The host is trusted.** A compromised host kernel, hypervisor or Lima release is
  outside the model. So is an attacker who already has the operator's account.
