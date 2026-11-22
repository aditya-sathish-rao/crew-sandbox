# The guest module is only a configuration once home-manager has evaluated it: a
# misspelled option or a broken `extraPackages` contract stays invisible until a VM
# boots. This assembles `agent@aarch64-linux`, reads its settled options, and asserts
# what the guest depends on.
#
# It produces a text file for the *host* system, so `nix flake check` verifies the
# Linux configuration at evaluation time without needing a Linux builder.
{
  lib,
  pkgs,
  nixpkgs,
  home-manager,
}:
let
  guestPkgs = nixpkgs.legacyPackages.aarch64-linux;

  # Sentinels: passed as `extraPackages` and expected to arrive unchanged.
  sentinels = [
    guestPkgs.hello
    guestPkgs.jq
  ];

  mkGuest =
    extraPackages:
    home-manager.lib.homeManagerConfiguration {
      pkgs = guestPkgs;
      modules = [ ../guest ];
      extraSpecialArgs = { inherit extraPackages; };
    };

  guest = mkGuest sentinels;
  bare = mkGuest [ ];

  inherit (guest) config;

  # A shell only carries the fleet if `profileExtra` reaches login shells and
  # `initExtra` reaches interactive ones; `limactl shell` and ssh sessions are either.
  hasNixProfile = s: lib.hasInfix ".nix-profile/etc/profile.d/nix.sh" s;

  expected = {
    "home.username" = config.home.username == "agent";
    "home.homeDirectory" = config.home.homeDirectory == "/home/agent";
    "home.stateVersion" = config.home.stateVersion == "26.05";
    "targets.genericLinux.enable" = config.targets.genericLinux.enable;
    "programs.bash.enable" = config.programs.bash.enable;
    "programs.bash.initExtra" = hasNixProfile config.programs.bash.initExtra;
    "programs.bash.profileExtra" = hasNixProfile config.programs.bash.profileExtra;
    "home.file..pi/agent/models.json" = config.home.file ? ".pi/agent/models.json";
    "home.file..pi/agent/settings.json" = config.home.file ? ".pi/agent/settings.json";

    # `extraPackages` is the whole fleet: the module installs nothing of its own, so
    # a guest gains exactly as many packages as were passed in and loses none. The
    # counts are compared rather than the contents because forcing home-manager's own
    # documentation packages makes it emit a store-reference warning of its own.
    "home.packages" =
      lib.length config.home.packages == lib.length bare.config.home.packages + lib.length sentinels;
  };

  failures = lib.filter (name: !expected.${name}) (lib.attrNames expected);
in
if failures != [ ] then
  throw "guest configuration is wrong: ${lib.concatStringsSep ", " failures} failed"
else
  pkgs.writeText "crew-sandbox-guest-check" (
    "guest configuration ok:\n" + lib.concatMapStrings (name: "  ${name}\n") (lib.attrNames expected)
  )
