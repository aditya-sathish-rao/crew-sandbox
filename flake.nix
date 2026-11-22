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

      # The Go unit tests. They cover the vault chain and the mount rendering; the
      # guest-facing gates land in verify/ and are wired in as they are ported.
      checks = forAllSystems (pkgs: {
        unit = pkgs.callPackage ./nix/checks/unit.nix { };
        guest = pkgs.callPackage ./nix/checks/guest.nix { inherit nixpkgs home-manager; };
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
          ];
        };
      });

      formatter = forAllSystems (pkgs: pkgs.nixfmt);

      # The guest's own home-manager configuration. A consumer adds it to its
      # `homeConfigurations` and supplies the fleet's tools as `extraPackages`.
      homeManagerModules.default = ./nix/guest;
    };
}
