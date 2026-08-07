{
  lib,
  stdenv,
  runCommand,
  writeText,
  tidal-syncer,
  ffmpeg-headless,
  ...
}:

let
  # readelf, taken from the stdenv's own bintools so the test does not pull a
  # second binutils into the closure.
  readelf = "${stdenv.cc.bintools.bintools}/bin/readelf";

  # The absolute path the wrapper is expected to bake in as the TIDAL_FFMPEG
  # default. Derived, not spelled out, so an ffmpeg bump cannot desync the test
  # from the package.
  ffmpegExe = lib.getExe ffmpeg-headless;

  # Minimal *valid* config, as data rather than a heredoc, so a validator
  # regression can be reproduced by hand from the store path in the build log.
  # Only the two absolute paths are build-time dependent (they must live inside
  # $TMPDIR), hence the @TMPDIR@ placeholder.
  #
  # Everything omitted here is exercised via internal/config's Defaults():
  # daemon.mode defaults to polling and daemon.polling is derived from
  # daemon.interval (15m), so no time_window block is needed -- and must not be
  # present, since time_window mode validation demands start != end.
  # tidal_auth.client_id/client_secret are the only two fields Validate()
  # requires to be non-empty; metrics.enabled=false keeps metrics.address out of
  # validation entirely.
  selfcheckConfigTemplate = writeText "tidal-syncer-selfcheck-config.yaml" ''
    paths:
      music: @TMPDIR@/music
      data: @TMPDIR@/data
    scope:
      favorites:
        tracks: true
    daemon:
      mode: polling
    concurrency: 1
    tidal_auth:
      client_id: dummy-client-id
      client_secret: dummy-client-secret
    metrics:
      enabled: false
  '';
in
{
  # The tree's only `package main` is ./cmd, so `go install ./cmd` names the
  # artifact after its directory and would ship $out/bin/cmd. Nothing downstream
  # would notice at build time: meta.mainProgram, the systemd ExecStart and every
  # operator invocation all say `tidal-syncer` and would simply fail at runtime.
  # Assert both halves -- the rename happened AND nothing was left behind.
  binaryName = runCommand "tidal-syncer-test-binary-name" { } ''
    if [ ! -x ${tidal-syncer}/bin/tidal-syncer ]; then
      echo "FAIL: ${tidal-syncer}/bin/tidal-syncer is missing or not executable" >&2
      ls -la ${tidal-syncer}/bin >&2
      exit 1
    fi

    if [ -e ${tidal-syncer}/bin/cmd ]; then
      echo "FAIL: \$out/bin/cmd still exists -- the postInstall rename regressed" >&2
      ls -la ${tidal-syncer}/bin >&2
      exit 1
    fi

    touch $out
  '';

  # THE silent-breakage guard. internal/sync/wiring.go and cmd/health.go resolve
  # ffmpeg as cmp.Or(os.Getenv("TIDAL_FFMPEG"), "/usr/local/bin/ffmpeg"): the env
  # var or a path inherited from the container image that does not exist on
  # NixOS. $PATH is NEVER consulted, so propagating ffmpeg the usual ways (a
  # PATH-only wrapper, systemd `path = [ ... ]`) leaves the daemon starting fine,
  # passing every option assertion, and then failing every single DASH download
  # as a retried-forever "transient" error. No crash, no alert.
  ffmpegWrapper = runCommand "tidal-syncer-test-ffmpeg-wrapper" { } ''
    wrapper=${tidal-syncer}/bin/tidal-syncer

    if ! grep -q TIDAL_FFMPEG "$wrapper"; then
      echo "FAIL: wrapper does not mention TIDAL_FFMPEG at all" >&2
      cat "$wrapper" >&2
      exit 1
    fi

    if ! grep -qF '${ffmpegExe}' "$wrapper"; then
      echo "FAIL: wrapper does not point TIDAL_FFMPEG at ${ffmpegExe}" >&2
      cat "$wrapper" >&2
      exit 1
    fi

    # --set-default emits the ''${VAR-default} form; plain --set emits an
    # unconditional assignment. The difference is invisible until an operator
    # sets Environment=TIDAL_FFMPEG= on the unit and is silently ignored.
    if ! grep -qF 'TIDAL_FFMPEG=''${TIDAL_FFMPEG-' "$wrapper"; then
      echo "FAIL: TIDAL_FFMPEG is set unconditionally; --set-default was expected" >&2
      cat "$wrapper" >&2
      exit 1
    fi

    # A wrapper naming a path that is not there is no better than no wrapper.
    if [ ! -x '${ffmpegExe}' ]; then
      echo "FAIL: ${ffmpegExe} is not an executable file" >&2
      exit 1
    fi

    touch $out
  '';

  # `version` is the one subcommand that reads no config: it logs the
  # ldflag-injected buildinfo through the bootstrap zerolog logger, which writes
  # to *stderr*. Guards the three ldflags -X paths silently drifting from the
  # internal/buildinfo variable names -- a typo there still builds and still
  # links, the binary just reports empty metadata forever.
  #
  # testers.testVersion was tried first and does NOT work here: it greps its
  # captured output with `grep -Fw`, and zerolog's ConsoleWriter colorizes
  # unconditionally (it honours neither NO_COLOR nor TTY detection), so the
  # rendered field is `<ESC>[36mversion=<ESC>[0m0-unstable`. The reset sequence
  # ends in `m`, a word-constituent character immediately before the version, so
  # -w can never match. Hence this runCommand: strip the SGR escapes, then
  # assert the version is the value of the `version=` field specifically rather
  # than merely present somewhere -- a bare substring grep would also pass if
  # the version string leaked into `commit=` or `built=` instead.
  version = runCommand "tidal-syncer-test-version" { } ''
    # No config file exists anywhere in the sandbox, so a zero exit here is
    # itself proof that `version` short-circuits config loading.
    if ! raw=$(${tidal-syncer}/bin/tidal-syncer version 2>&1); then
      echo "FAIL: tidal-syncer version exited non-zero" >&2
      echo "$raw" >&2
      exit 1
    fi

    plain=$(printf '%s\n' "$raw" | sed -e 's/\x1b\[[0-9;]*m//g')

    if ! printf '%s\n' "$plain" | grep -qF 'version=${tidal-syncer.version}'; then
      echo "FAIL: expected 'version=${tidal-syncer.version}' in the version output" >&2
      echo "--- decolorized output was:" >&2
      printf '%s\n' "$plain" >&2
      exit 1
    fi

    touch $out
  '';

  # Highest-value test in the file. One command proves, end to end:
  #   * the YAML loader + validator accept a real minimal file,
  #   * SQLite opens and migrates (modernc pure-Go driver -- no CGO, which is
  #     why env.CGO_ENABLED=0 is safe),
  #   * ffmpeg is reachable, because runSelfcheck exec's `<ffmpeg> -version`
  #     and the ONLY thing telling it where ffmpeg lives is the wrapper.
  # Deliberately no ffmpeg in nativeBuildInputs: putting it on $PATH would make
  # this pass even with the wrapper dropped, which is the exact regression the
  # test exists to catch.
  selfcheckSmoke = runCommand "tidal-syncer-test-selfcheck" { } ''
    # Anti-tautology guard: if some stdenv change ever leaks ffmpeg onto $PATH
    # this test stops proving anything about the wrapper, so fail loudly instead.
    if command -v ffmpeg >/dev/null 2>&1; then
      echo "FAIL: ffmpeg is on \$PATH; this test can no longer prove the wrapper supplies it" >&2
      exit 1
    fi

    # store.Open() only ever sql.Open()s <data>/tidal-syncer.db -- it never
    # MkdirAll's the parent, so the directory has to exist up front.
    mkdir -p "$TMPDIR/music" "$TMPDIR/data"
    sed "s|@TMPDIR@|$TMPDIR|g" ${selfcheckConfigTemplate} > "$TMPDIR/config.yaml"

    # --config is a PERSISTENT root flag: it must precede the subcommand.
    # Passing `selfcheck --config ...` parses, then reads the /app/config.yaml
    # default and fails.
    if ! ${tidal-syncer}/bin/tidal-syncer --config "$TMPDIR/config.yaml" selfcheck 2>&1; then
      echo "FAIL: tidal-syncer selfcheck exited non-zero" >&2
      echo "--- config was:" >&2
      cat "$TMPDIR/config.yaml" >&2
      exit 1
    fi

    # selfcheck's store ping is a real migration; the db file is the receipt.
    if [ ! -f "$TMPDIR/data/tidal-syncer.db" ]; then
      echo "FAIL: selfcheck reported success but never created the SQLite store" >&2
      exit 1
    fi

    touch $out
  '';

  # `noTidalNetwork` is intentionally ABSENT. selfcheckSmoke already establishes
  # it and a separate derivation could only restate it: the test above runs in
  # the Nix sandbox, which has no network, and passes with deliberately bogus
  # tidal_auth credentials. Any test asserting "and it had no network" would be
  # asserting a property of the sandbox, not of this package -- a tautology.

  # go.senan.xyz/taglib is the one dependency that looks like it needs a C
  # toolchain. It does not: it ships TagLib compiled to WASM and executes it
  # under wazero. That premise is what lets the package build with
  # CGO_ENABLED=0 and ship a closure with no cc, no libstdc++ and no libtag --
  # and if it ever stops holding, the build keeps succeeding while the binary
  # gains a dynamic dependency on host libraries that are not in the closure.
  purity = runCommand "tidal-syncer-test-purity" { } ''
    # Derive the unwrapped binary rather than hardcoding makeWrapper's
    # ".<name>-wrapped" convention, and insist on exactly one match so a
    # renamed or vanished wrapper target fails instead of silently skipping.
    shopt -s nullglob dotglob
    candidates=( ${tidal-syncer}/bin/.*-wrapped )
    if [ ''${#candidates[@]} -ne 1 ]; then
      echo "FAIL: expected exactly one wrapped binary, found ''${#candidates[@]}" >&2
      ls -la ${tidal-syncer}/bin >&2
      exit 1
    fi
    unwrapped="''${candidates[0]}"

    # Prove readelf actually parsed an ELF; otherwise a readelf that errors out
    # produces empty output and every grep below trivially "passes".
    if ! ${readelf} -h "$unwrapped" | grep -q 'ELF'; then
      echo "FAIL: readelf could not parse $unwrapped as an ELF object" >&2
      ${readelf} -h "$unwrapped" >&2 || true
      exit 1
    fi

    # A fully static Go binary has no dynamic section at all, so an empty
    # NEEDED set is the expected result, not a sign the check misfired.
    needed=$(${readelf} -d "$unwrapped" 2>/dev/null | grep NEEDED || true)
    if echo "$needed" | grep -Eqi 'libtag|libstdc\+\+'; then
      echo "FAIL: unwrapped binary links a system C++/tag library:" >&2
      echo "$needed" >&2
      exit 1
    fi

    touch $out
  '';
}
