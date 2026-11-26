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

    # Only for evaluating the exported darwin module in `checks`. A consumer
    # already running nix-darwin should make it follow its own input rather than
    # take this one, so a second copy of nixpkgs is never evaluated.
    nix-darwin = {
      url = "github:nix-darwin/nix-darwin/nix-darwin-26.05";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs =
    {
      self,
      nixpkgs,
      home-manager,
      nix-darwin,
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
        # The darwin module is only a configuration once nix-darwin has evaluated
        # it against a real host: a misspelled option or a bad `ProgramArguments`
        # type stays invisible until someone rebuilds. This evaluates a
        # configuration with the services enabled and asserts what launchd would
        # actually be given.
        darwin = import ./nix/checks/darwin.nix {
          inherit pkgs nixpkgs nix-darwin;
          lib = pkgs.lib;
          crew-sandbox = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
        };

        # The example consumer is the template for the cutover, so it is evaluated
        # rather than trusted: a renamed option fails here instead of in someone's
        # dotfiles.
        examples = import ./nix/checks/examples.nix {
          inherit
            pkgs
            nixpkgs
            home-manager
            nix-darwin
            ;
          lib = pkgs.lib;
          crew-sandbox = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
        };

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

      # The host's configuration: the egress proxy and the token broker as launchd
      # user agents. A consumer with nix-darwin adds `services.crew-sandbox.enable`,
      # its `package`, its `appID` and its vault enrollment.
      darwinModules.default = ./nix/modules/darwin.nix;
    };
}
