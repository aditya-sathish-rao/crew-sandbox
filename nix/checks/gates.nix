# The gates and the red-team script are bash, and the only other thing that would
# notice a syntax error is a run against a real guest — which needs a VM, the
# operator's secrets, and minutes. Linting them here keeps `nix flake check` honest
# about the scripts the binary carries without needing any of that.
{
  runCommand,
  bash,
  shellcheck,
}:
runCommand "crew-sandbox-gates-lint"
  {
    nativeBuildInputs = [
      bash
      shellcheck
    ];
  }
  ''
    set -e
    for script in ${../..}/verify/gates/*.sh ${../..}/verify/redteam/*.sh; do
      bash -n "$script"
      shellcheck --severity=warning --shell=bash "$script"
    done
    mkdir -p $out
    printf 'linted %s\n' ${../..}/verify/gates/*.sh ${../..}/verify/redteam/*.sh > $out/report
  ''
