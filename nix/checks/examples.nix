# The example consumer is only a template if it still evaluates. A module option
# renamed here, or a `services.crew-sandbox` field that moved, would otherwise stay
# invisible until someone copied the example into their dotfiles and rebuilt.
#
# This evaluates both halves of `examples/consumer/` against this flake's own
# modules — the guest as `agent@aarch64-linux`, the host as a darwin system — and
# asserts the wiring the example claims: the fleet arrives unchanged, and the host
# services are configured from the example's own values.
#
# It produces a text file for the *host* system, so `nix flake check` verifies the
# example at evaluation time without a Linux builder or a Mac.
{
  lib,
  pkgs,
  nixpkgs,
  home-manager,
  nix-darwin,
  crew-sandbox,
}:
let
  guestPkgs = nixpkgs.legacyPackages.aarch64-linux;

  example = ../../examples/consumer;

  # The fleet the example names, read the same way its own flake reads it.
  fleet = (import "${example}/home.nix" { pkgs = guestPkgs; }).home.packages;

  guest = home-manager.lib.homeManagerConfiguration {
    pkgs = guestPkgs;
    modules = [ ../guest ];
    extraSpecialArgs = {
      extraPackages = fleet;
    };
  };

  # home-manager adds packages of its own (bash, for one), so the fleet is compared
  # against a guest that was given none rather than against zero.
  bare = home-manager.lib.homeManagerConfiguration {
    pkgs = guestPkgs;
    modules = [ ../guest ];
    extraSpecialArgs = {
      extraPackages = [ ];
    };
  };

  host = nix-darwin.lib.darwinSystem {
    system = "aarch64-darwin";
    modules = [
      ../modules/darwin.nix
      "${example}/darwin.nix"
      {
        nixpkgs.hostPlatform = "aarch64-darwin";
        services.crew-sandbox.package = crew-sandbox;
      }
    ];
  };

  guestConfig = guest.config;
  hostConfig = host.config;

  expected = {
    # The guest half: the example's fleet is the guest's whole package set, and the
    # module still fixes the account the VM creates.
    "the example's fleet reaches the guest" =
      lib.length guestConfig.home.packages == lib.length bare.config.home.packages + lib.length fleet;
    "the guest account is still agent" = guestConfig.home.username == "agent";

    # The host half: the example's own values are what the module settled on, so a
    # renamed option fails here rather than in someone's dotfiles.
    "the example's upstream reaches the proxy" =
      lib.hasInfix "--model-upstream https://api.example.com" (
        lib.concatStringsSep " " hostConfig.services.crew-sandbox._proxyArgs
      );
    "the example's key secret reaches the proxy" = lib.hasInfix "--model-key-env CREW_MODEL_KEY" (
      lib.concatStringsSep " " hostConfig.services.crew-sandbox._proxyArgs
    );
    "the example's app id reaches the broker" = lib.hasInfix "--app-id 123456" (
      lib.concatStringsSep " " hostConfig.services.crew-sandbox._brokerArgs
    );
    "the example registers a user agent" =
      lib.hasAttr "crew-sandbox-proxy" hostConfig.launchd.user.agents;
  };

  failures = lib.filter (name: !expected.${name}) (lib.attrNames expected);
in
if failures != [ ] then
  throw "the example consumer is wrong: ${lib.concatStringsSep ", " failures} failed"
else
  pkgs.writeText "crew-sandbox-examples-check" (
    "example consumer ok:\n" + lib.concatMapStrings (name: "  ${name}\n") (lib.attrNames expected)
  )
