# The guest's home-manager configuration: what the agent user can run inside the VM.
#
# Exported as `homeManagerModules.default`. The fleet's tools arrive through
# `extraPackages`, so this module names no tool set of its own and imports nothing
# from another repository.
#
# Nothing here may need root. `agent` has no sudo, so this module plus
# `extraPackages` is the whole definition of what the fleet can run inside the VM.
#
# `username` and `homeDirectory` are fixed rather than options: lima.yaml creates
# exactly `agent` with `/home/agent`, and any other value fails activation there.
{
  extraPackages ? [ ],
  ...
}:
let
  # Interactive shells read .bashrc; login shells — `limactl shell` and every ssh
  # session — read .profile, which home-manager owns. Both need the same PATH.
  environment = ''
    # Single-user Nix keeps its profile in ~/.nix-profile; home-manager owns
    # .profile now, so this is what puts nix and nix-store back on PATH.
    if [ -e "$HOME/.nix-profile/etc/profile.d/nix.sh" ]; then
      . "$HOME/.nix-profile/etc/profile.d/nix.sh"
    fi
  '';
in
{
  # Not NixOS: this makes home-manager set PATH, XDG variables and locale the way
  # a plain Linux distribution expects.
  targets.genericLinux.enable = true;

  home = {
    username = "agent";
    homeDirectory = "/home/agent";
    stateVersion = "26.05";

    # Empty by default: a consumer that passes nothing gets a shell and no fleet.
    packages = extraPackages;

    # Pi's provider endpoint and defaults point at the host's model gateway, so the
    # real model key is never part of the guest's configuration or environment.
    file = {
      ".pi/agent/models.json".source = ./models.json;
      ".pi/agent/settings.json".source = ./settings.json;
    };
  };

  # The proxy lives in /etc/profile.d/crew-proxy.sh, which every login shell reads;
  # this covers interactive shells that never see a login, and login shells whose
  # .profile home-manager now writes.
  programs.bash = {
    enable = true;
    initExtra = environment;
    profileExtra = environment;
  };
}
