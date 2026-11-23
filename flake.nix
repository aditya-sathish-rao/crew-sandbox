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
      # (which include every embedded script parsing, including the two .mjs),
      # the proxy's and the broker's own --selftest, prettier on both scripts, and
      # an evaluation of the exported guest configuration.
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
        # A syntax error in a .mjs costs a live proxy, and an unformatted one is a
        # review blind spot. The unit tests run `node --check`; this enforces the
        # style without needing a network or a VM.
        formatting = pkgs.runCommand "crew-sandbox-formatting" { nativeBuildInputs = [ pkgs.prettier ]; } ''
          export HOME=$TMPDIR
          prettier --check --no-cache \
            ${./internal/proxy/proxy.mjs} \
            ${./internal/broker/broker.mjs}
          mkdir -p $out
        '';
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
            prettier
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
