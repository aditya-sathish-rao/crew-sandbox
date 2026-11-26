# A minimal consumer of crew-sandbox, as a standalone flake.
#
# This is the template for the cutover: `agentic-dev-env` replaces the fleet in
# `home.nix` and the provider and App ID in `darwin.nix`, and changes nothing else.
# The two halves are separate outputs because they are separate machines —
# `homeConfigurations` is applied inside the Linux VM, `darwinConfigurations` on
# the Mac.
#
# `nix/checks/examples.nix` evaluates both halves from the parent flake, so this
# file cannot drift from the module's contract without failing `nix flake check`.
{
  description = "A minimal consumer of crew-sandbox";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-26.05-darwin";

    home-manager = {
      url = "github:nix-community/home-manager/release-26.05";
      inputs.nixpkgs.follows = "nixpkgs";
    };

    nix-darwin = {
      url = "github:nix-darwin/nix-darwin/nix-darwin-26.05";
      inputs.nixpkgs.follows = "nixpkgs";
    };

    # Pin to a tag once one exists, so a rebuild is reproducible:
    #   url = "github:aditya-sathish-rao/crew-sandbox/v0.1.0";
    crew-sandbox.url = "github:aditya-sathish-rao/crew-sandbox";
  };

  outputs =
    {
      self,
      nixpkgs,
      home-manager,
      nix-darwin,
      crew-sandbox,
    }:
    let
      guestPkgs = nixpkgs.legacyPackages.aarch64-linux;

      # The fleet, read once so the module and the check agree on it.
      fleet = (import ./home.nix { pkgs = guestPkgs; }).home.packages;
    in
    {
      # Applied inside the guest, as `agent`. The username and home directory are
      # fixed by the module, so the attribute name is not a choice.
      homeConfigurations."agent@aarch64-linux" = home-manager.lib.homeManagerConfiguration {
        pkgs = guestPkgs;
        modules = [ crew-sandbox.homeManagerModules.default ];
        extraSpecialArgs = {
          extraPackages = fleet;
        };
      };

      # Applied on the Mac.
      darwinConfigurations."mini" = nix-darwin.lib.darwinSystem {
        system = "aarch64-darwin";
        modules = [
          crew-sandbox.darwinModules.default
          ./darwin.nix
          {
            nixpkgs.hostPlatform = "aarch64-darwin";
            services.crew-sandbox.package = crew-sandbox.packages.aarch64-darwin.default;
          }
        ];
      };
    };
}
