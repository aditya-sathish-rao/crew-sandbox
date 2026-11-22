# The host-side CLI. It drives limactl and starts the broker and proxy.
{
  lib,
  buildGoModule,
  lima,
  nodejs,
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
  nativeBuildInputs = [ makeWrapper ];

  postInstall = ''
    wrapProgram $out/bin/crew-sandbox \
      --prefix PATH : ${
        lib.makeBinPath [
          lima
          nodejs
        ]
      }
  '';

  meta = {
    description = "Run untrusted agent work inside a Linux VM boundary";
    homepage = "https://github.com/aditya-sathish-rao/crew-sandbox";
    mainProgram = "crew-sandbox";
    platforms = [
      "aarch64-darwin"
      "aarch64-linux"
    ];
  };
}
