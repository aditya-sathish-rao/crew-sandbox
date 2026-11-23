# The host-side CLI. It drives limactl and starts the broker and proxy.
{
  lib,
  buildGoModule,
  lima,
  nodejs,
  curl,
  jq,
  coreutils,
  lsof,
  makeWrapper,
}:
buildGoModule {
  pname = "crew-sandbox";
  version = "0.1.0";

  # The repository root is the module root; cmd/ and internal/ live under it.
  src = lib.cleanSource ../../.;

  vendorHash = null;

  # limactl and node are runtime dependencies: the CLI shells out to both. Wrapping
  # them onto PATH keeps the binary honest without baking absolute store paths into
  # the Go source.
  #
  # curl, jq, coreutils and lsof are for the verification gates, which are bash
  # scripts this binary carries: they probe the running guest and the host
  # services, so they need the same tools on the operator's PATH that they would
  # have had as a separate package. `lsof` in particular is what proves a listener
  # is gone and that a guest listener did not surface on the host's loopback.
  nativeBuildInputs = [ makeWrapper ];

  postInstall = ''
    wrapProgram $out/bin/crew-sandbox \
      --prefix PATH : ${
        lib.makeBinPath [
          lima
          nodejs
          curl
          jq
          coreutils
          lsof
        ]
      }
  '';

  meta = {
    description = "Run untrusted agent work inside a Linux VM boundary";
    homepage = "https://github.com/aditya-sathish-rao/crew-sandbox";
    license = lib.licenses.mit;
    mainProgram = "crew-sandbox";
    platforms = [
      "aarch64-darwin"
      "aarch64-linux"
    ];
  };
}
