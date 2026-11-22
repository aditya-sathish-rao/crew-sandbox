{
  description = "crew-sandbox: a Linux VM boundary for untrusted agent work";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-26.05-darwin";

  outputs =
    { self, nixpkgs }:
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
    };
}
