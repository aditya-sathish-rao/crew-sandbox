# The Go unit tests, run as a flake check. These are the tests that do not need a
# guest: the vault chain, the mount rendering, and the fact that every script the
# binary carries parses. Anything that needs a VM runs from `crew-sandbox check`.
{
  lib,
  buildGoModule,
  lima,
  nodejs,
}:
buildGoModule {
  pname = "crew-sandbox-unit";
  version = "0.1.0";
  src = lib.cleanSource ../..;
  vendorHash = null;
  nativeBuildInputs = [
    lima
    nodejs
  ];

  # The tests are the product; there is no binary to install. A check derivation
  # must still produce an output path, so the test log is written to $out.
  buildPhase = ''
    runHook preBuild
    go test -v ./... 2>&1 | tee test.log
    runHook postBuild
  '';
  installPhase = ''
    runHook preInstall
    mkdir -p $out
    cp test.log $out/test.log
    runHook postInstall
  '';
  doCheck = false;
}
