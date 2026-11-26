Sandbox the crew on a headless Mac mini
Draft for review · 21 November 2026 · revises crew-sandbox-plan.md (agentic-dev-env @ 5d2be4a) for a mini that is about to go headless, a laptop that has not arrived, and iOS and Mac work later.
Summary
• Run the whole fleet, first mate included, inside one Lima Linux VM on the mini (the first plan’s Shape A). The laptop becomes a thin client and the place you approve and sign, once it arrives.
• Make the mini a proper headless host first, using the kunchenguid/headless agent skill or its nix-darwin module, and prove it recovers from a power cut before the screen is unplugged.
• Keep authority outside the guest: the real model key stays on the host and is injected by a proxy, GitHub access comes from per-task App tokens, and the guest has no vault, no SSH key and no gh login.
• Close the holes the first plan leaves open: a sudo-capable agent user, DNS exfiltration, the real key in the guest’s environment, git hooks and symlinks crossing a shared mount, and too little disk.
• Treat iOS and Mac work as a separate macOS lane, added later, with signing kept on the laptop.
What changed from the first plan
1.  Shape A replaces Shape B. With the mini headless there is no operator at it, and firstmate’s own docs say it does not place individual workers on a remote machine. Running everything in the guest removes the herdr shim, the shared worktree mount and the whole class of problems where the host reads files the guest wrote.
2.  A new Phase 0 makes the mini a reliable headless host. The first plan assumed one already existed.
3.  Authority moves ahead of everything that relocates panes. Credentials are the largest risk, so they are fixed before the fleet moves.
4.  Guest hardening is stricter: no sudo, root-owned firewall rules, and DNS only through the proxy.
5.  ‘macOS-only work stays on the host’ becomes ‘macOS-only work runs in its own lane’, so Swift tasks do not silently run unsandboxed.
Starting point
• The mini (M2 Pro, 16 GB) is your only machine today and will go headless soon. Tailscale is already configured on it.
• A MacBook Pro with 24 GB RAM is coming. Until it arrives, anything that needs a second machine has to run against the mini itself.
• Free disk on the mini was about 20 GiB when the first plan was checked. The Linux guest alone wants about 10 GiB.
• The harness is pi with a single model provider. pi has no base-URL setting, so a proxy cannot simply be pointed at.
• Crewmates today run as your user, with your files, your hardened gh token and an open network.
• You want to build iOS and Mac apps later.
Decisions
Settled
• Lima with the vz driver for the always-on crew VM, with an Ubuntu LTS guest plus Nix and home-manager rather than a NixOS image (generating a Linux image needs a Linux builder this Mac lacks).
• Per-task credentials, never written to disk in the guest.
• Apple’s container tooling later, for one-shot untrusted jobs.
Changed in this revision
• Whole fleet in the guest (Shape A).
• Proxy-side key injection moves from a later option to a required phase.
Open
• FileVault: automatic sign-in after a reboot requires it off (see Phase 0). Decide once the mini holds no personal data.
• Whether agents run simulator tests or only compile and run unit tests, which sizes the macOS lane.
• Which client you operate from once the laptop arrives. The plan assumes ssh plus herdr from any client.
Target design
Host (the mini, headless): owns the vault, the egress proxy and allowlist, the token broker, the repository that holds the pins, and the Lima VM. Nothing the agents touch lives in the host user’s home directory.
Guest (Lima, Linux): herdr server and panes, first mate, crewmates, the worktree pool, and the same Nix pins as the host. Agents run as a dedicated non-sudo user. The guest has no mounts from the host.
What crosses the boundary:
• Operator access: ssh from your client over Tailscale into the guest, then herdr.
• Egress: guest to the host proxy only, with a default-deny allowlist (model host, GitHub, package registries). DNS goes through the proxy or one allowlisted resolver.
• Model access: the guest holds a dummy key. The proxy terminates TLS for the model host only, using a CA that exists only in the guest, and injects the real Authorization header.
• GitHub access: a host-side broker mints a GitHub App installation token per task (one repository, contents and pull requests only, 60 minutes) and hands it to the pane at spawn.
• Nothing else: no host mount, no agent forwarding, no vault, no SSH key, no gh login.
Phase 0: make the mini a reliable headless host
The headless skill
kunchenguid/headless (MIT licence, shell, no tagged releases) is an agent skill that sets up a headless Mac, Linux or Windows machine over SSH, one step at a time, so it never sleeps, signs itself in after a restart, accepts SSH keys only and can optionally join Tailscale. It is designed so the agent never sees the admin password. You install it with npx skills add kunchenguid/headless -g and invoke it with something like /headless set up my Mac mini at <address> as a headless machine. For a Mac it walks through seven steps, the first being admin access, which you grant once at the machine. The repository also ships a nix-darwin module (darwinModules.headless, flake input github:kunchenguid/headless, options documented in skills/headless/references/mac.md) and a starter flake (nix flake init -t github:kunchenguid/headless#darwin).
Requirements per the repository: macOS or Linux with bash, ssh and tar on the machine you run it from; a target with an admin account that has a password and Remote Login enabled; an Apple silicon Mac.
How to use it here
1.  Read before you run. There are no releases, so pin to a commit hash. Read SKILL.md and the scripts under skills/headless/scripts/mac/ (connect, apply, verify, restart, tailscale). This is an agent changing system settings with admin rights, so treat it as code you are installing, not documentation.
2.  Run it from a supervised session on a trusted machine, never from inside the crew sandbox, and never let a crewmate invoke it.
3.  You have one machine for now. The tool works over SSH, and the repository’s own CI runs the Mac journey over SSH to the same machine, so running it on the mini against itself should work. Test that before relying on it, or wait for the laptop and run it from there.
4.  Prefer the nix-darwin module for steady state. It keeps never-sleep and key-only SSH in the flake, so a rebuild reproduces them. Use the skill for the one-off steps a module cannot do, such as the first admin grant and installing your public key. Do not let both manage the same settings.
5.  Tailscale is already set up on the mini. Skip the skill’s Tailscale step, or check it does not install a second copy. Whichever copy you keep must run as a system daemon, so the network is up before anyone logs in.
6.  Automatic sign-in and FileVault conflict. The skill makes the machine sign itself in after a restart, and as far as I know macOS does not allow automatic login with FileVault on. That is acceptable for a blank agent host and a real exposure for a machine that holds your personal files. Do not turn FileVault off until the mini holds no personal data, or use the alternative below.
Alternative to auto sign-in: run the VM and the proxy as LaunchDaemons with UserName set, so they start at boot without a login. Test whether Lima’s vz driver works that way before choosing it. Unattended starts then work with FileVault on, at the cost of a manual unlock after a power loss.
Host checklist
• Never sleep; restart after power failure.
• Key-only sshd, with Screen Sharing enabled as a recovery path.
• A dummy HDMI plug if you will use Screen Sharing on a headless Mac, since resolution is otherwise poor.
• Tailscale as a system daemon, with ACL tags so the mini can be reached but cannot start connections to your other devices.
• A hard monthly spend cap on the model provider key, set at the provider.
• Free disk raised well above the Linux guest’s needs, and an external SSD planned for later macOS VM images.
• A client you can reach it from while the laptop is missing, such as a phone SSH app over Tailscale.
Gate
1.  With the screen still attached, cut power and restore it. Within a few minutes the mini must be reachable over Tailscale SSH with no one touching it.
2.  The skill’s own verify script passes after that reboot.
3.  Repeat the same recovery from your remote client with the screen disconnected.
4.  Only then unplug the screen.
Phase 1: the box
A Lima VM (vz, 4 to 6 GiB while the mini is also your daily machine, sparse 30 GiB disk) with mounts: [], no port forwards and no agent forwarding.
• Agents run as a dedicated guest user with no sudo, and the firewall rules are owned by root.
• nftables default-deny, with one allowed destination: the host proxy port. Lima’s host address can reach services listening on the host’s loopback, so allow only that port.
• DNS resolves only through the proxy or one allowlisted resolver.
Gate: the guest is Linux; ls /Users fails; there are no 9p or virtiofs mounts; a direct curl to an arbitrary host fails; sudo -n true fails for the agent user; nft flush ruleset fails; a DNS query to an arbitrary resolver fails; the proxy port answers.
Phase 2: the toolchain
Nix inside the guest and one home-manager module that imports the same pinned tool set as the host, including pi, herdr and the crew tools. Host and guest run the same herdr release. Clones go through the proxy, and installs run with scripts disabled.
Gate: every tool resolves under /nix/store; pi --version matches the host; a clone through the proxy works; nix-store --verify is clean.
Phase 3: authority
• Model key: a dummy key in the guest, the real key on the host, injected by the TLS-terminating proxy for the model host only. Check whether the vault’s av proxy already does this before building one. Keep the provider cap and plan key rotation.
• GitHub: a host broker holding the App private key (kept in the vault, never in the guest) mints one installation token per task, for one repository, with contents and pull request permissions, valid 60 minutes.
• Turn on branch protection and required CI, and give agent tokens no merge rights, on a throwaway repository first.
• Hard caps per task on tokens, iterations and wall-clock time; a daily budget at the proxy; and a remote kill command that stops the VM and revokes tokens.
Gate: a token pushes to its own repository and is refused on any other; the guest’s environment holds no real key; killing the proxy stops model traffic; ssh -T git@github.com fails; the kill command works from the remote client.
Phase 4: move the fleet in
Run herdr, the first mate, crewmates and the worktree pool inside the guest. Each worktree is an independent clone, so no repository metadata lives outside the guest. Fork firstmate, pin it, and disable its self-update inside the guest; pull upstream changes only after reading the diff.
Gate: no worker process runs on the host; the operator can attach from a remote client and reconnect after a guest reboot. Unpushed work is lost if the guest disk is deleted, so crewmates push to agent branches often.
Phase 5: adversarial proof
Run a red-team script as a crewmate that simulates a prompt injection. Every attempt must fail, with the evidence recorded.
• Read /Users and the host’s home directory.
• Dump the environment and look for a real key.
• Send data to an arbitrary host, including over DNS.
• Push to main; push to a different repository.
• Reach host services through Lima’s host address.
• Use sudo; flush the firewall.
• Plant a git hook or a symlink in a worktree and confirm nothing outside the guest ever acts on it.
Phase 6: the macOS lane (later)
Linux can build pure Swift packages but cannot run Xcode, the simulator or code signing. Add a separate lane once the laptop and external storage exist.
• A macOS guest VM (Tart is the established tool) for Xcode builds and, if wanted, simulator tests. As far as I know a Mac can run two macOS VMs at once, physical devices cannot be attached, and the image is large, so budget external SSD space.
• It may need a graphical login session on the host, which pushes towards auto sign-in. Check this before committing to a headless macOS lane.
• Builds are unsigned. Signing certificates, Apple ID and App Store Connect keys stay on the laptop and never enter a sandbox.
• Memory: with 16 GB, do not run the Linux guest and a macOS guest at full size together. Start the macOS guest on demand and shut it down afterwards.
• Interactive Xcode work, device debugging and signing happen on the laptop.
Gaps this plan closes
1.  The agent could remove its own firewall rules through sudo. Fixed in Phase 1.
2.  DNS was an open exfiltration channel. Fixed in Phase 1.
3.  The real model key sat in the guest’s environment and could leave in a commit or pull request body. Fixed in Phase 3.
4.  A shared, writable worktree mount let the guest plant git hooks, config and symlinks that the host would later act on. Removed by Shape A.
5.  A long-lived token gave one task more authority than it needed. Fixed with per-task App tokens.
6.  Unattended runs had no spend ceiling. Fixed with per-task caps, a daily budget, a provider cap and a kill command.
7.  Not enough disk. Addressed in Phase 0.
8.  A self-updating upstream tool could change behaviour under you. Addressed by forking and pinning firstmate.
9.  No plan for a headless host: boot order, FileVault, secrets at boot, recovery. Addressed in Phase 0 and the next section.
Running unattended
• Secrets at boot: the vault needs interactive approval, which a headless host cannot give. Either unseal manually through Screen Sharing after each reboot, or keep the real key in a host-bound encrypted secret such as agenix that decrypts to a root-only file. The second works unattended but leaves the key at rest on disk, which is acceptable only because the provider cap bounds the loss.
• Health: one status command that names which of the VM, the proxy and herdr is down.
• Recovery order after a power loss: network and Tailscale, then the VM, then the proxy, then herdr.
• Keep a break-glass path: Screen Sharing, and a way to reach the machine that does not depend on the VM.
What this plan does not claim
• A scoped token can still open a malicious pull request in its one repository. Review, branch protection and required CI are the control.
• The first mate runs inside the boundary but still reads untrusted issue text, so it holds only broker-issued credentials.
• It does not make model traffic private from the provider.
• It does not protect your own shell or editor on the laptop.
• None of this has been run. The headless skill’s behaviour is taken from its README and surrounding pages, not from reading its scripts.
Order of work
1.   set the provider cap, and record the baseline of what a crewmate can reach today.
2.  Phase 0, ending with the power-cut test.
3.  Phases 1 and 2.
4.  Phase 3.
5.  Phase 4, then Phase 5.
6.  Phase 6 after the laptop and external storage arrive.
