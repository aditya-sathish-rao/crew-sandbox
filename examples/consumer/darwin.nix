# The host side of a consumer: the egress proxy and the token broker as launchd
# user agents.
#
# Imported by `flake.nix` in this directory and evaluated by
# `nix/checks/examples.nix`. `system.primaryUser` is required by nix-darwin for
# `launchd.user.agents`, and it is the user the agents run as.
{ ... }:
{
  system.primaryUser = "operator";
  system.stateVersion = 6;

  services.crew-sandbox = {
    enable = true;

    # The provider seam: no vendor is the default, so a consumer names one. The
    # key itself stays in the vault under `keySecret` and never enters the guest.
    proxy.upstream = "https://api.example.com";
    proxy.keySecret = "CREW_MODEL_KEY";

    # Public configuration, not a secret. The private key is read from the vault
    # as `GITHUB_APP_PRIVATE_KEY`.
    broker.appID = "123456";
  };
}
