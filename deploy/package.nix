{
  lib,
  callPackage,
  buildGo126Module,
  makeWrapper,
  ffmpeg-headless,
  version ? "0-unstable",
  commitHash ? "unknown",
  buildTime ? "unknown",
}:

buildGo126Module (finalAttrs: {
  pname = "tidal-syncer";
  inherit version;

  # An explicit allow-list rather than a bare `src = ../.`. Under the flake the
  # two are equivalent (the store copy is already git-clean), but a bare
  # `callPackage ./deploy/package.nix {}` from a working checkout takes the
  # literal directory: that drags in the gitignored operator state — a ~19 GB
  # `Music/` tree — and, worse, `config.yaml`, which holds the real TIDAL client
  # secret, into the world-readable store. Listing what the compiler and the
  # tests actually need keeps both paths byte-identical and safe.
  # `cmd`/`internal`/`pkg` are the only directories in the tree holding .go
  # files, and they contain every `testdata/` fixture. config.example.yaml is
  # not build input but test input: internal/config asserts that the shipped
  # example still parses into the expected struct, reaching it as ../../.
  src = lib.fileset.toSource {
    root = ../.;
    fileset = lib.fileset.unions [
      ../go.mod
      ../go.sum
      ../config.example.yaml
      ../cmd
      ../internal
      ../pkg
    ];
  };

  vendorHash = "sha256-KVoXQ1veu0Fy340rmD2/uYbmcNCCBHdA/jKTkRdx1rw=";

  # The sole `package main` in the tree. `go install ./cmd` therefore names the
  # binary after its directory, hence the rename in postInstall.
  subPackages = [ "cmd" ];

  # buildGoModule inherits the toolchain default of CGO_ENABLED=1, which would
  # put a cc reference in the closure. Nothing here needs it: the one apparent
  # C dependency, go.senan.xyz/taglib, ships TagLib compiled to WASM and runs it
  # under wazero, so the project is CGO-free end to end.
  env.CGO_ENABLED = 0;

  nativeBuildInputs = [ makeWrapper ];

  # The DASH download tests shell out to ffmpeg and t.Fatalf when
  # exec.LookPath cannot find it. Everything else is httptest-backed, so the
  # suite is hermetic inside the sandbox.
  nativeCheckInputs = [ ffmpeg-headless ];

  # `subPackages` is consumed by getGoDirs in BOTH the build and the check
  # phase, so leaving it set would narrow `go test` to ./cmd alone and silently
  # skip the entire suite — including the real DASH demux tests that the ffmpeg
  # nativeCheckInput exists for. Clearing the variable makes getGoDirs fall back
  # to discovering every package holding a _test.go. getGoDirs and its $exclude
  # are defined at buildPhase top level and survive into checkPhase, which is
  # why this is safe to do from preCheck.
  preCheck = ''
    unset subPackages
  '';

  # -trimpath and -buildid= are supplied by buildGoModule itself, and
  # -extldflags '-static' is a no-op with cgo disabled; passing any of them here
  # only risks fighting the builder.
  ldflags = [
    "-s"
    "-w"
    "-X=github.com/labi-le/tidal-syncer/internal/buildinfo.Version=${finalAttrs.version}"
    "-X=github.com/labi-le/tidal-syncer/internal/buildinfo.CommitHash=${commitHash}"
    "-X=github.com/labi-le/tidal-syncer/internal/buildinfo.BuildTime=${buildTime}"
  ];

  # ffmpeg is resolved exclusively from $TIDAL_FFMPEG, falling back to a
  # hardcoded /usr/local/bin/ffmpeg inherited from the container image; $PATH is
  # never consulted. So propagating ffmpeg via a PATH-only wrapper (or a
  # systemd `path = [ ... ]`) would silently leave every DASH download failing
  # as a retried-forever transient error. --set-default rather than --set so an
  # operator's unit-level `Environment=TIDAL_FFMPEG=` still takes precedence.
  postInstall = ''
    mv $out/bin/cmd $out/bin/tidal-syncer
    wrapProgram $out/bin/tidal-syncer \
      --set-default TIDAL_FFMPEG ${lib.getExe ffmpeg-headless}
  '';

  # Derivation-level regression tests for the packaging itself (binary name, the
  # TIDAL_FFMPEG wrapper, a real `selfcheck` run, absence of a libtag/libstdc++
  # NEEDED entry). `callPackage` returns an attrset with `override` and
  # `overrideDerivation` lambdas spliced in, which break any consumer doing
  # attrValues over it, so keep only the derivations. The Go suite itself is
  # already covered by doCheck above; the module- and eval-level tests live in
  # ./tests and are aggregated by ./tests/default.nix.
  passthru.tests = lib.filterAttrs (_: lib.isDerivation) (
    callPackage ./tests/package.nix { tidal-syncer = finalAttrs.finalPackage; }
  );

  meta = {
    description = "TIDAL-to-local-FLAC sync daemon";
    homepage = "https://github.com/labi-le/tidal-syncer";
    license = lib.licenses.asl20;
    mainProgram = "tidal-syncer";
    platforms = lib.platforms.linux;
  };
})
