# The host side of the boundary, as a nix-darwin module.
#
# This configures the authority — the egress proxy, which holds the model key, and
# the token broker, which holds the GitHub App key. The guest is Lima's business;
# nothing here creates or starts it.
#
# ## Why a user LaunchAgent and not a system daemon
#
# Both services hold a secret, and every secret comes from the vault. A job that
# starts at boot has no login session and nobody at the keyboard, and the vault's
# default policy answers that with "human approval required". That was measured
# rather than assumed: `av inject` run from a `launchd` job with `ppid 1` and no
# TTY returned the value, but Authorization History recorded every read as
# `approvalSource: Manual`, "Approved in prompt" — including one through a Blessed
# Script with `capabilities: {}`. The only `Auto` approvals on this host came from
# a tool-specific Authorization Gate (`gh`), and a per-tool gate cannot answer for
# a service that starts before any tool runs.
#
# So a `LaunchAgent` in the operator's own GUI session is the narrowest thing that
# works: it runs as the operator, so a vault prompt lands on a desktop where a
# human can answer it and can be granted a Temporary Access Grant bound to the
# job. A `LaunchDaemon` would run as root before login and simply fail.
#
# The cost is stated rather than hidden: the services start at login, so a Mac
# that reboots unattended has no egress proxy until someone logs in. That is
# cheaper than a daemon holding a key nobody approved.
#
# `system.primaryUser` must be set in the consuming configuration: nix-darwin
# requires it for `launchd.user.agents`, and it is the user the agents run as.
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.services.crew-sandbox;

  proxyArgs = [
    "--name"
    cfg.instanceName
    "--port"
    (toString cfg.proxy.port)
    "--model-port"
    (toString cfg.proxy.modelPort)
    "--model-key-env"
    cfg.proxy.keySecret
  ]
  ++ lib.optionals (cfg.proxy.upstream != "") [
    "--model-upstream"
    cfg.proxy.upstream
  ]
  ++ lib.optionals (cfg.proxy.allowFile != null) [
    "--egress-allow"
    (toString cfg.proxy.allowFile)
  ];

  brokerArgs = [
    "--app-id"
    cfg.broker.appID
    "--port"
    (toString cfg.broker.port)
  ]
  ++ lib.optionals (cfg.broker.reposAllow != null) [
    "--repos-allow"
    (toString cfg.broker.reposAllow)
  ]
  ++ cfg.broker.extraArgs;

  # A wrapper per service. launchd runs a program with an argument list rather
  # than a shell, so the flags are assembled as a list here and no quoting bug can
  # reinterpret one. `exec` matters: without it the job's main process is the
  # wrapper, so launchd's exit status and signals describe the shell rather than
  # the service.
  #
  # `escapeShellArgs` quotes each element, so a path with a space stays one
  # argument.
  proxyScript = pkgs.writeShellScript "crew-sandbox-proxy" ''
    set -eu
    export PATH="${lib.makeBinPath cfg.runtimePackages}:$PATH"
    exec ${cfg.package}/bin/crew-sandbox proxy ${lib.escapeShellArgs proxyArgs}
  '';

  brokerScript = pkgs.writeShellScript "crew-sandbox-broker" ''
    set -eu
    export PATH="${lib.makeBinPath cfg.runtimePackages}:$PATH"
    exec ${cfg.package}/bin/crew-sandbox broker ${lib.escapeShellArgs brokerArgs}
  '';

  agentConfig = script: {
    ProgramArguments = [ "${script}" ];
    RunAtLoad = true;
    # A service that dies is a boundary that is down, so restart it — but only
    # on failure, and no faster than the throttle: a vault prompt can take
    # longer than a tight restart loop would tolerate.
    KeepAlive = {
      SuccessfulExit = false;
    };
    ThrottleInterval = 30;
  };
in
{
  options.services.crew-sandbox = {
    enable = lib.mkEnableOption "the crew-sandbox host services (egress proxy and token broker)";

    package = lib.mkOption {
      type = lib.types.package;
      defaultText = lib.literalExpression "crew-sandbox.packages.\${system}.default";
      description = ''
        The crew-sandbox CLI. Pass this repository's
        `packages.<system>.default`: there is no nixpkgs attribute of this name,
        so it has no default.
      '';
    };

    instanceName = lib.mkOption {
      type = lib.types.str;
      default = "crew-sandbox";
      description = ''
        The Lima instance the proxy reads the guest's model token from. It must
        match the name the guest was created with.
      '';
    };

    runtimePackages = lib.mkOption {
      type = lib.types.listOf lib.types.package;
      default = with pkgs; [
        nodejs
        lima
        curl
        jq
        lsof
      ];
      defaultText = lib.literalExpression "with pkgs; [ nodejs lima curl jq lsof ]";
      description = ''
        Placed on the services' `PATH`. The proxy and the broker are both Node
        scripts, and the proxy reaches the guest through `limactl`.
      '';
    };

    logDirectory = lib.mkOption {
      type = lib.types.str;
      default = "${config.system.primaryUserHome}/Library/Logs/crew-sandbox";
      defaultText = lib.literalExpression ''"''${system.primaryUserHome}/Library/Logs/crew-sandbox"'';
      description = "Where the services write stdout and stderr.";
    };

    proxy = {
      enable = lib.mkOption {
        type = lib.types.bool;
        default = true;
        description = "Run the egress proxy. The broker runs independently.";
      };

      port = lib.mkOption {
        type = lib.types.port;
        default = 3128;
        description = ''
          The guest's only way out. `lima.yaml` and the guest's nftables rules
          both name this port, so changing it here means changing them too.
        '';
      };

      modelPort = lib.mkOption {
        type = lib.types.port;
        default = 3130;
        description = ''
          The model gateway's own listener, kept off `port` so the socket holding
          the model key is never the egress socket.
        '';
      };

      upstream = lib.mkOption {
        type = lib.types.str;
        default = "";
        example = "https://api.deepseek.com";
        description = ''
          The model provider's base URL. Empty disables the gateway and leaves
          egress enforcement in force; no vendor is the default.
        '';
      };

      keySecret = lib.mkOption {
        type = lib.types.str;
        default = "CREW_MODEL_KEY";
        description = ''
          The vault Secret Name holding the model key. It is read on the host at
          start-up and never enters the guest.
        '';
      };

      allowFile = lib.mkOption {
        type = lib.types.nullOr lib.types.path;
        default = null;
        example = "/etc/crew-sandbox/egress-allow";
        description = ''
          Hostname allowlist. Null uses the example compiled into the binary, so
          a module that names nothing still enforces a boundary rather than
          removing one.
        '';
      };
    };

    broker = {
      enable = lib.mkOption {
        type = lib.types.bool;
        default = true;
        description = "Run the token broker. It needs `broker.appID` to start.";
      };

      appID = lib.mkOption {
        type = lib.types.str;
        default = "";
        example = "5853565";
        description = ''
          The GitHub App's numeric ID. Public configuration, not a secret: the
          private key is read from the vault as `GITHUB_APP_PRIVATE_KEY`.
        '';
      };

      port = lib.mkOption {
        type = lib.types.port;
        default = 3129;
        description = ''
          The broker's listen port. The guest's firewall deliberately does not
          open it, so a crewmate cannot mint itself a token.
        '';
      };

      reposAllow = lib.mkOption {
        type = lib.types.nullOr lib.types.path;
        default = null;
        example = "/etc/crew-sandbox/repos-allow";
        description = "Repository allowlist. Null uses the example compiled into the binary.";
      };

      extraArgs = lib.mkOption {
        type = lib.types.listOf lib.types.str;
        default = [ ];
        description = "Extra flags for `crew-sandbox broker`.";
      };
    };

    # The flags each wrapper will run with, once every option has settled. They
    # are internal and read-only: launchd is handed a single wrapper path, so
    # without this there is nothing an evaluation check can assert on short of
    # building the wrapper and reading it back.
    _proxyArgs = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      internal = true;
      readOnly = true;
      description = "The settled flags for `crew-sandbox proxy`.";
      default = proxyArgs;
    };

    _brokerArgs = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      internal = true;
      readOnly = true;
      description = "The settled flags for `crew-sandbox broker`.";
      default = brokerArgs;
    };
  };

  config = lib.mkIf cfg.enable {
    # launchd creates the log file but not the directory above it, and a job whose
    # StandardOutPath parent is missing fails to spawn with no useful error. The
    # directory is declared here rather than left to the operator.
    system.activationScripts.crew-sandbox-logdir.text =
      lib.mkIf (cfg.proxy.enable || (cfg.broker.enable && cfg.broker.appID != ""))
        ''
          install -d -m 0700 ${lib.escapeShellArg cfg.logDirectory}
          chown ${lib.escapeShellArg config.system.primaryUser} ${lib.escapeShellArg cfg.logDirectory}
        '';

    launchd.user.agents = lib.mkMerge [
      (lib.mkIf cfg.proxy.enable {
        crew-sandbox-proxy = {
          serviceConfig = agentConfig proxyScript // {
            StandardOutPath = "${cfg.logDirectory}/proxy.log";
            StandardErrorPath = "${cfg.logDirectory}/proxy.log";
          };
        };
      })
      (lib.mkIf (cfg.broker.enable && cfg.broker.appID != "") {
        crew-sandbox-broker = {
          serviceConfig = agentConfig brokerScript // {
            StandardOutPath = "${cfg.logDirectory}/broker.log";
            StandardErrorPath = "${cfg.logDirectory}/broker.log";
          };
        };
      })
    ];

    warnings = lib.optional (cfg.broker.enable && cfg.broker.appID == "") ''
      services.crew-sandbox.broker.enable is true but broker.appID is empty, so
      the token broker will not be started. Set broker.appID to the GitHub App's
      numeric ID, or set broker.enable = false if the App is managed elsewhere.
    '';
  };
}
