# What the build can prove with no VM and no network: the proxy's and the broker's
# own --selftest, plus an evaluation of the guest configuration this flake exports.
#
# The gates are deliberately absent. They assert on a guest as it is right now, which
# means a running VM, the operator's secrets and outbound network — none of which a
# Nix build has. They run from `crew-sandbox check` instead.
{
  linkFarm,
  runCommand,
  crew-sandbox,
  guest,
  openssl,
}:
let
  # A throwaway key for the broker's self-test, which signs a JWT and verifies it
  # with the public half of the same key. Generating one here keeps a private key
  # out of the repository; it authenticates nothing.
  key = runCommand "crew-sandbox-selftest-key" { nativeBuildInputs = [ openssl ]; } ''
    openssl genrsa 2048 > $out
  '';

  proxySelftest = runCommand "crew-sandbox-proxy-selftest" { } ''
    ${crew-sandbox}/bin/crew-sandbox proxy --selftest
    mkdir -p $out
  '';

  brokerSelftest = runCommand "crew-sandbox-broker-selftest" { } ''
    GITHUB_APP_PRIVATE_KEY="$(cat ${key})" \
      ${crew-sandbox}/bin/crew-sandbox broker --app-id 1 --selftest
    mkdir -p $out
  '';
in
# linkFarm rather than symlinkJoin: the guest evaluation writes a file, not a
# directory, and a farm links either.
linkFarm "crew-sandbox-selftest" [
  {
    name = "proxy";
    path = proxySelftest;
  }
  {
    name = "broker";
    path = brokerSelftest;
  }
  {
    name = "guest";
    path = guest;
  }
]
