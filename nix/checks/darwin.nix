# The darwin module is only a configuration once nix-darwin has evaluated it: a
# misspelled option, a bad `ProgramArguments` type or a missing `system.primaryUser`
# stays invisible until someone runs `darwin-rebuild`. This evaluates a host
# configuration with the services enabled and asserts what launchd would be given.
#
# It produces a text file, so `nix flake check` verifies the host configuration at
# evaluation time on the host it is built for, without activating anything.
{
  lib,
  pkgs,
  nixpkgs,
  nix-darwin,
  crew-sandbox,
}:
let
  # The module is Darwin-only, but it must evaluate from either system so a Linux
  # CI job still type-checks it rather than silently skipping the check.
  darwinPkgs = nixpkgs.legacyPackages.aarch64-darwin;

  mkHost =
    extra:
    nix-darwin.lib.darwinSystem {
      system = "aarch64-darwin";
      modules = [
        ../modules/darwin.nix
        {
          # nix-darwin requires this for `launchd.user.agents`, and the agents run
          # as this user. A consuming configuration sets it to the operator.
          system.primaryUser = "operator";
          system.stateVersion = 6;
          nixpkgs.hostPlatform = "aarch64-darwin";
          services.crew-sandbox = {
            enable = true;
            package = crew-sandbox;
          }
          // extra;
        }
      ];
    };

  # Every setting named, so the assertion covers the flags rather than the
  # defaults alone.
  full = mkHost {
    instanceName = "crew-sandbox";
    proxy = {
      port = 3128;
      modelPort = 3130;
      upstream = "https://api.example.test";
      keySecret = "CREW_MODEL_KEY";
    };
    broker = {
      appID = "123";
      port = 3129;
    };
    proxy.extraArgs = [
      "--model-token-file"
      "/etc/crew-sandbox/model-token"
    ];
  };

  disabled = mkHost {
    enable = false;
  };

  inherit (full) config;

  # The plist is generated from `serviceConfig`, which is what launchd reads.
  proxy = config.launchd.user.agents.crew-sandbox-proxy.serviceConfig;
  broker = config.launchd.user.agents.crew-sandbox-broker.serviceConfig;

  joined = args: lib.concatStringsSep " " (map toString args);

  cfgLogDir = config.services.crew-sandbox.logDirectory;

  # The vault question the module exists to answer: these are user agents in a GUI
  # session, not root daemons that start before login.
  isUserAgent = lib.hasAttr "crew-sandbox-proxy" config.launchd.user.agents;
  # nix-darwin brings its own daemons (`nix-daemon`, `activate-system`), so the
  # assertion is that this module added none, not that the attrset is empty.
  noCrewDaemon =
    !(lib.any (n: lib.hasPrefix "crew-sandbox" n) (lib.attrNames config.launchd.daemons));

  # The flags the wrapper will run with, settled by the module.
  proxyArgs = config.services.crew-sandbox._proxyArgs;
  brokerArgs = config.services.crew-sandbox._brokerArgs;

  expected = {
    # A user agent, and no system daemon: a daemon would start before login, where
    # the vault has nobody to approve its prompt.
    "a user agent is registered" = isUserAgent;
    "no crew-sandbox launch daemon is registered" = noCrewDaemon;

    # RunAtLoad is what starts the service in the session; without it the agent
    # exists but never runs until something asks for it.
    "the proxy runs at login" = proxy.RunAtLoad;
    "the broker runs at login" = broker.RunAtLoad;

    # Restart on failure only: a clean exit is a deliberate stop.
    "the proxy restarts only on failure" = proxy.KeepAlive.SuccessfulExit == false;
    "the broker restarts only on failure" = broker.KeepAlive.SuccessfulExit == false;

    # The proxy is given the provider and the secret name as flags, not baked
    # into the binary: that is the provider seam.
    "the proxy names its upstream" = lib.hasInfix "--model-upstream https://api.example.test" (
      joined proxyArgs
    );
    "the proxy names the key secret" = lib.hasInfix "--model-key-env CREW_MODEL_KEY" (joined proxyArgs);
    "the proxy listens on the configured ports" =
      lib.hasInfix "--port 3128" (joined proxyArgs)
      && lib.hasInfix "--model-port 3130" (joined proxyArgs);

    # A fleet that runs on the host has no guest to read the token from, so the
    # operator's file has to reach the proxy as a flag.
    "the proxy reads the token from the operator's file" =
      lib.hasInfix "--model-token-file /etc/crew-sandbox/model-token" (joined proxyArgs);

    # The App ID is public configuration, so it is a flag rather than a secret.
    "the broker names its app id" = lib.hasInfix "--app-id 123" (joined brokerArgs);
    "the broker listens on its own port" = lib.hasInfix "--port 3129" (joined brokerArgs);

    # Each service has one argument: the wrapper, which carries the flags. A
    # hand-built multi-argument list is how a quoting bug gets in.
    "each service runs one program" =
      lib.length proxy.ProgramArguments == 1 && lib.length broker.ProgramArguments == 1;

    # Logs land under the primary user's home, not in /tmp.
    "the proxy logs to the operator's home" =
      lib.hasPrefix "${config.system.primaryUserHome}/Library/Logs" proxy.StandardOutPath;

    # launchd does not create the directory above StandardOutPath, and a job whose
    # parent directory is missing never spawns. Activation has to make it.
    "the log directory is created at activation" =
      lib.hasInfix cfgLogDir config.system.activationScripts.crew-sandbox-logdir.text;

    # Disabling the module leaves launchd untouched.
    "a disabled module registers no agent" = disabled.config.launchd.user.agents == { };

    # An enabled broker with no App ID must warn rather than start a broker that
    # can mint nothing.
    "an unconfigured broker warns" =
      let
        warnHost = mkHost {
          broker.appID = "";
        };
      in
      lib.any (w: lib.hasInfix "broker.appID is empty" w) warnHost.config.warnings;
  };

  failures = lib.filter (name: !expected.${name}) (lib.attrNames expected);
in
if failures != [ ] then
  throw "darwin module is wrong: ${lib.concatStringsSep ", " failures} failed"
else
  pkgs.writeText "crew-sandbox-darwin-check" (
    "darwin module ok:\n" + lib.concatMapStrings (name: "  ${name}\n") (lib.attrNames expected)
  )
