{
  description = "crew-sandbox: a Linux VM boundary for untrusted agent work";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-26.05-darwin";

    # Only for `lib.homeManagerConfiguration`, which nix/checks/guest.nix uses to
    # evaluate the exported guest module. The module itself needs nothing from here.
    home-manager = {
      url = "github:nix-community/home-manager/release-26.05";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs =
    {
      self,
      nixpkgs,
      home-manager,
    }:
    let
      # Darwin is where the operator and the host-side authority live; Linux is
      # where the guest runs. Both are evaluated so the same flake serves each.
      systems = [
        "aarch64-darwin"
        "aarch64-linux"
      ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in
    {
      packages = forAllSystems (pkgs: {
        default = pkgs.callPackage ./nix/packages/crew-sandbox.nix { };
        crew-sandbox = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
      });

      # Only what a build can prove with no VM and no network: the Go unit tests
      # (which include every embedded script parsing), the proxy's and the broker's
      # own --selftest, and an evaluation of the exported guest configuration.
      #
      # The gates are not here. They assert on a guest as it is right now, which
      # needs a running VM, the operator's secrets and outbound network, so
      # `crew-sandbox check` runs them.
      checks = forAllSystems (pkgs: {
        unit = pkgs.callPackage ./nix/checks/unit.nix { };
        selftest = pkgs.callPackage ./nix/checks/selftest.nix {
          crew-sandbox = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
          guest = pkgs.callPackage ./nix/checks/guest.nix { inherit nixpkgs home-manager; };
        };
      });

      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages = with pkgs; [
            git
            go
            jq
            lima
            nixfmt
            nodejs
            shellcheck
          ];
        };
      });

      formatter = forAllSystems (pkgs: pkgs.nixfmt);

      # The guest's own home-manager configuration. A consumer adds it to its
      # `homeConfigurations` and supplies the fleet's tools as `extraPackages`.
      homeManagerModules.default = ./nix/guest;
    };
}
