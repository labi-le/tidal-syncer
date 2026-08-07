# Pure-evaluation tests for the tidal-syncer NixOS module (deploy/service.nix
# + deploy/options.nix).
#
# Every case here guards a failure mode that is SILENT at runtime: the unit
# still starts, systemd still reports `active`, and the damage only shows up as
# missing music, unreadable albums or a leaked secret. Cheap eval-time
# assertions are the only place these get caught.
#
# Run:
#   nix build --impure --no-link --expr 'let pkgs = (builtins.getFlake "/home/labile/nix").inputs.nixpkgs.legacyPackages.x86_64-linux; in pkgs.callPackage ./deploy/tests/eval.nix { }'
{
  lib,
  pkgs,
  ...
}:
let
  # The module's `package` option is `lib.mkPackageOption pkgs "tidal-syncer"`,
  # so the evaluated pkgs must actually carry the attribute.
  overlay = final: _: {
    tidal-syncer = final.callPackage ../package.nix { };
  };

  machineStub = {
    nixpkgs.pkgs = pkgs.extend overlay;
    boot.loader.grub.devices = [ "nodev" ];
    fileSystems."/" = {
      device = "/dev/vda";
      fsType = "ext4";
    };
    system.stateVersion = "24.11";
  };

  # Minimum viable settings: enable + the three options with no default.
  # scope.favorites.albums is on so the module's "nothing is in scope" warning
  # does not fire -- that lets the ProtectHome cases below assert on
  # `warnings == [ ]` / `warnings != [ ]` without ever matching on prose.
  baseSettings = {
    enable = true;
    paths.music = "/drive/sync/music";
    tidalAuth.clientId = "test-client-id";
    tidalAuth.clientSecretFile = "/run/secrets/tidal-client-secret";
    scope.favorites.albums = true;
  };

  # THE shared helper: an attrset of `services.tidal-syncer` settings (deep-merged
  # over baseSettings, so a case can override a nested default without needing
  # mkForce) plus optional extra machine modules -> the evaluated `config`.
  evalCfg =
    settings: extraModules:
    (import (pkgs.path + "/nixos/lib/eval-config.nix") {
      system = null; # taken from nixpkgs.pkgs
      modules = [
        ../service.nix
        machineStub
        { services.tidal-syncer = lib.recursiveUpdate baseSettings settings; }
      ]
      ++ extraModules;
    }).config;

  cfgOf = settings: evalCfg settings [ ];

  # Prometheus + Grafana, enabled just enough for the dashboard fold-in to be
  # legal. secret_key is set because upstream's grafana module otherwise adds a
  # failing assertion of its own, which would pollute the assertion counts in
  # section H below.
  monitoringStub = {
    services.prometheus.enable = true;
    services.grafana = {
      enable = true;
      provision.enable = true;
      settings.security.secret_key = "eval-test-secret-key";
    };
  };

  # --- reading the rendered config.yaml back out of the store ----------------
  #
  # The module does not expose the generated attrset, so the only honest way to
  # check what actually lands on disk is to follow ExecStartPre to the assemble
  # script, follow that to the YAML template, and read it. Assertions are
  # therefore substring/regex based rather than structural -- deliberately, so
  # that quoting and key-ordering regressions in the generator are visible too.
  #
  # `builtins.match` discards string context, and `builtins.readFile` needs the
  # context to realise the store path it is handed. `builtins.substring` keeps
  # the context of the *whole* input string, so slice instead of matching.
  sliceOut =
    re: s:
    let
      m = builtins.match re s;
    in
    builtins.substring (builtins.stringLength (builtins.elemAt m 0)) (
      builtins.stringLength (builtins.elemAt m 1)
    ) s;

  renderedYaml =
    c: unit:
    let
      assembleInvocation = builtins.head c.systemd.services.${unit}.serviceConfig.ExecStartPre;
      script = builtins.readFile (sliceOut "()([^ ]+) .*" assembleInvocation);
    in
    builtins.readFile (sliceOut "(.*install -m 0600 )([^ ]+) .*" script);

  # Scalar lookup by leaf key name, quoted or not.
  yamlValue =
    key: text:
    let
      m = builtins.match ".*\n *${key}: '?([^'\n]*)'?\n.*" text;
    in
    if m == null then null else builtins.elemAt m 0;

  has = needle: text: lib.hasInfix needle text;

  # --- shared evaluations ----------------------------------------------------
  base = cfgOf { };
  baseYaml = renderedYaml base "tidal-syncer";

  metricsOverride = cfgOf {
    metrics = {
      listenAddress = "10.0.0.5";
      port = 19101;
    };
  };

  # A real secret living in the store, purely so case A2 can prove its content
  # never reaches the rendered template.
  secretSentinel = "th1s-must-never-reach-the-nix-store";
  secretFile = pkgs.writeText "tidal-syncer-eval-test-secret" secretSentinel;
  withStoreSecret = cfgOf { tidalAuth.clientSecretFile = "${secretFile}"; };

  timeWindowCfg = cfgOf {
    daemon = {
      mode = "time_window";
      timeWindow = {
        start = "01:00";
        end = "05:00";
        min = "1m";
        max = "20m";
      };
    };
  };
  timeWindowYaml = renderedYaml timeWindowCfg "tidal-syncer";

  underHome = cfgOf { paths.music = "/home/labile/sshfs/music"; };
  networkMount = cfgOf { paths.music = "/mnt/webdav/music"; };
  customData = cfgOf { paths.data = "/srv/ts"; };
  withTimeZone = cfgOf { timeZone = "Europe/Moscow"; };

  dashboard = evalCfg {
    metrics = {
      enable = true;
      listenAddress = "10.0.0.5";
      port = 19101;
    };
    dashboard.enable = true;
  } [ monitoringStub ];

  svc = c: unit: c.systemd.services.${unit}.serviceConfig;
  env = c: unit: c.systemd.services.${unit}.environment;

  hardeningTriple = c: unit: {
    inherit (svc c unit) MemoryDenyWriteExecute PrivateTmp UMask;
  };

  failedAssertions = c: builtins.length (lib.filter (a: !a.assertion) c.assertions);

  grafanaProviders =
    c:
    let
      settings = c.services.grafana.provision.dashboards.settings;
    in
    if settings == null then [ ] else settings.providers or [ ];

  tidalScrapeJobs = c: lib.filter (j: j.job_name == "tidal-syncer") c.services.prometheus.scrapeConfigs;

  cases = {
    # === A. rendered config.yaml ===========================================

    # A1/A2 -- failure mode 6: upstream's compose default was ":9101", which
    # binds every interface. Inside a docker network namespace that was
    # contained; on a host it exposes an unauthenticated /metrics. The module
    # composes the address from two options instead, and this pins the join.
    testMetricsAddressDefault = {
      expr = yamlValue "address" baseYaml;
      expected = "127.0.0.1:9101";
    };
    testMetricsAddressOverride = {
      expr = yamlValue "address" (renderedYaml metricsOverride "tidal-syncer");
      expected = "10.0.0.5:19101";
    };

    # A3/A4 -- failure mode 7: the template is world-readable in /nix/store.
    # Only the placeholder may appear; the real secret is injected at
    # ExecStartPre from a systemd credential.
    testClientSecretIsPlaceholder = {
      expr = yamlValue "client_secret" baseYaml;
      expected = "@TIDAL_CLIENT_SECRET@";
    };
    testSecretContentNeverInStore = {
      expr = {
        placeholder = yamlValue "client_secret" (renderedYaml withStoreSecret "tidal-syncer");
        leaked = has secretSentinel (renderedYaml withStoreSecret "tidal-syncer");
      };
      expected = {
        placeholder = "@TIDAL_CLIENT_SECRET@";
        leaked = false;
      };
    };

    # A5/A6 -- the Go loader unmarshals snake_case. A camelCase key is not an
    # error there, it is silently ignored, so the option would just stop working.
    testSnakeCaseKeysPresent = {
      expr = {
        path_template = has "\npath_template:" baseYaml;
        tidal_auth = has "\ntidal_auth:" baseYaml;
        metrics_enabled = has "\n  enabled:" baseYaml;
        time_window = has "\n  time_window:\n" timeWindowYaml;
      };
      expected = {
        path_template = true;
        tidal_auth = true;
        metrics_enabled = true;
        time_window = true;
      };
    };
    testCamelCaseKeysAbsent = {
      expr = {
        pathTemplate = has "pathTemplate" baseYaml;
        tidalAuth = has "tidalAuth" baseYaml;
        timeWindow = has "timeWindow" timeWindowYaml;
      };
      expected = {
        pathTemplate = false;
        tidalAuth = false;
        timeWindow = false;
      };
    };

    # A7 -- `paths.config` is declared upstream but read nowhere; emitting it
    # would invite an operator to "fix" the path there and wonder why --config
    # still wins.
    testPathsConfigKeyAbsent = {
      expr = has "config:" baseYaml;
      expected = false;
    };

    # A8 -- failure mode: an explicit 0 is not the same as "unset". Upstream
    # derives daemon.polling.{min,max} from daemon.interval when the keys are
    # absent, but would take a literal 0 if we emitted one.
    testNullPollingBoundsOmitted = {
      expr = has "\n  polling:" baseYaml;
      expected = false;
    };
    # `start`/`end` are unique leaf keys, so a scalar lookup is unambiguous;
    # `min`/`max` also occur under jitter.worker, hence the whole-block check
    # in the next case rather than a scalar lookup here.
    testTimeWindowBoundsRendered = {
      expr = {
        start = yamlValue "start" timeWindowYaml;
        end = yamlValue "end" timeWindowYaml;
      };
      expected = {
        start = "01:00";
        end = "05:00";
      };
    };
    testTimeWindowBlockContents = {
      expr = has "  time_window:\n    end: 05:00\n    max: 20m\n    min: 1m\n    start: 01:00\n" timeWindowYaml;
      expected = true;
    };

    # === B. ProtectHome is computed, not hardcoded (failure mode 8) =========
    #
    # The unit may deliberately run as an existing (possibly human) uid so a
    # library already sitting in a home directory need not be chown'd.
    # ProtectHome is then the only thing keeping a compromised syncer out of
    # that user's ~/.ssh and source trees. It must stay on unless a configured
    # path literally lives under /home -- and must announce itself when it does.
    testProtectHomeOnByDefault = {
      expr = (svc base "tidal-syncer").ProtectHome;
      expected = true;
    };
    testNoWarningsForOffHomePaths = {
      expr = base.warnings;
      expected = [ ];
    };
    testProtectHomeOffForHomePath = {
      expr = (svc underHome "tidal-syncer").ProtectHome;
      expected = false;
    };
    # Only that a warning exists -- the wording belongs to the module author.
    testWarningEmittedForHomePath = {
      expr = underHome.warnings != [ ];
      expected = true;
    };

    # === C. filesystem-agnostic wiring ======================================
    #
    # systemd resolves each path to whatever .mount/.automount unit provides it,
    # so the module needs no knowledge of local disk vs sshfs vs WebDAV. What it
    # must not do is forget either path: starting before the library is mounted
    # makes the daemon see an empty directory, which under removal.policy =
    # "mirror" is indistinguishable from "the user deleted everything".
    testRequiresMountsForOrdinaryPaths = {
      expr = lib.sort (a: b: a < b) base.systemd.services.tidal-syncer.unitConfig.RequiresMountsFor;
      expected = [
        "/drive/sync/music"
        "/var/lib/tidal-syncer"
      ];
    };
    testRequiresMountsForNetworkMount = {
      expr = lib.sort (a: b: a < b) networkMount.systemd.services.tidal-syncer.unitConfig.RequiresMountsFor;
      expected = [
        "/mnt/webdav/music"
        "/var/lib/tidal-syncer"
      ];
    };

    # RequiresMountsFor can only order the unit after a mount systemd owns a unit
    # for (fstab, x-systemd.automount, an explicit .mount). An out-of-band
    # sshfs/rclone/davfs mount has none, so the ordering silently resolves to
    # nothing and the daemon can start on an empty mount point -- which never
    # self-heals, because the skip decision consults only the database, never the
    # disk. requireMountPoints turns that into a hard refusal to start, and stays
    # opt-in because asserting a plain local directory would never let it start.
    testAssertPathIsMountPointAbsentByDefault = {
      expr = base.systemd.services.tidal-syncer.unitConfig ? AssertPathIsMountPoint;
      expected = false;
    };
    testAssertPathIsMountPointEmitted = {
      expr =
        (cfgOf { requireMountPoints = [ "/drive/sync/music" ]; })
        .systemd.services.tidal-syncer.unitConfig.AssertPathIsMountPoint;
      expected = [ "/drive/sync/music" ];
    };
    testAssertPathIsMountPointOnLoginUnit = {
      expr =
        (cfgOf { requireMountPoints = [ "/drive/sync/music" ]; })
        .systemd.services.tidal-syncer-login.unitConfig.AssertPathIsMountPoint;
      expected = [ "/drive/sync/music" ];
    };
    testReadWritePaths = {
      expr = lib.sort (a: b: a < b) (svc base "tidal-syncer").ReadWritePaths;
      expected = [
        "/drive/sync/music"
        "/var/lib/tidal-syncer"
      ];
    };

    # === D. the three load-bearing hardening values =========================
    #
    # failure mode 3: tag writing goes through taglib compiled to WASM and run
    #   by wazero, whose compiler mmaps RW then mprotects R+X.
    #   MemoryDenyWriteExecute=yes kills every FLAC tag write.
    # failure mode 4: taglib opens a wazero compilation cache under os.TempDir()
    #   and hard-fails if it cannot; /tmp must stay writable.
    # failure mode 5: UMask 0077 would not fix anything already on disk, it
    #   would only make *new* albums unreadable to Jellyfin/Navidrome.
    # All three break silently, and all three are exactly what a future
    # "hardening pass" would flip. Pin them on both units.
    testHardeningDaemon = {
      expr = hardeningTriple base "tidal-syncer";
      expected = {
        MemoryDenyWriteExecute = false;
        PrivateTmp = true;
        UMask = "0022";
      };
    };
    testHardeningLoginUnit = {
      expr = hardeningTriple base "tidal-syncer-login";
      expected = {
        MemoryDenyWriteExecute = false;
        PrivateTmp = true;
        UMask = "0022";
      };
    };

    # === E. unit shape ======================================================

    # --config is a PERSISTENT flag: placed after the subcommand it is rejected,
    # and the daemon would silently fall back to its own config search path.
    testExecStartFlagPrecedesSubcommand = {
      expr = has " --config /run/tidal-syncer/config.yaml daemon" (svc base "tidal-syncer").ExecStart;
      expected = true;
    };

    # selfcheck validates config, store and `ffmpeg -version` up front. Without
    # it a broken deploy turns into an infinite "transient" retry loop that
    # never leaves the `active` state.
    testExecStartPreShape = {
      expr =
        let
          pre = (svc base "tidal-syncer").ExecStartPre;
        in
        {
          count = builtins.length pre;
          selfcheckLast = has " --config /run/tidal-syncer/config.yaml selfcheck" (builtins.elemAt pre 1);
        };
      expected = {
        count = 2;
        selfcheckLast = true;
      };
    };

    testLoadCredentialDaemon = {
      expr = (svc base "tidal-syncer").LoadCredential;
      expected = [ "client_secret:/run/secrets/tidal-client-secret" ];
    };
    testLoadCredentialLoginUnit = {
      expr = (svc base "tidal-syncer-login").LoadCredential;
      expected = [ "client_secret:/run/secrets/tidal-client-secret" ];
    };

    # The daemon re-reads its config every cycle and a RuntimeDirectory is torn
    # down when its owning unit stops. A shared directory would therefore yank
    # the config out from under a running daemon the moment the (manually
    # started, short-lived) login unit exits.
    testLoginRuntimeDirectoryIsSeparate = {
      expr = {
        daemonDir = (svc base "tidal-syncer").RuntimeDirectory;
        loginDir = (svc base "tidal-syncer-login").RuntimeDirectory;
        loginConfig = has " --config /run/tidal-syncer-login/config.yaml login" (
          svc base "tidal-syncer-login"
        ).ExecStart;
      };
      expected = {
        daemonDir = "tidal-syncer";
        loginDir = "tidal-syncer-login";
        loginConfig = true;
      };
    };

    # daemon.timeWindow is evaluated in local time, so TZ is part of the
    # contract, not cosmetics. null must inherit the host zone rather than
    # pinning UTC.
    testTimeZoneSetsTZ = {
      expr = (env withTimeZone "tidal-syncer").TZ or null;
      expected = "Europe/Moscow";
    };
    testTimeZoneNullOmitsTZ = {
      expr = (env base "tidal-syncer") ? TZ;
      expected = false;
    };

    # failure mode 1: the binary resolves ffmpeg ONLY from $TIDAL_FFMPEG and
    # falls back to a hardcoded /usr/local/bin/ffmpeg that does not exist on
    # NixOS. It never consults $PATH, so `path = [ ffmpeg ]` would be a no-op
    # and every DASH download would fail as a "transient" error, forever.
    testFfmpegEnvOnBothUnits = {
      expr = lib.mapAttrs (_: p: lib.hasPrefix builtins.storeDir p && lib.hasInfix "ffmpeg" p) {
        daemon = (env base "tidal-syncer").TIDAL_FFMPEG;
        login = (env base "tidal-syncer-login").TIDAL_FFMPEG;
      };
      expected = {
        daemon = true;
        login = true;
      };
    };

    # === F. StateDirectory vs tmpfiles ======================================
    #
    # store.Open never creates its parent directory. StateDirectory= only
    # understands names under /var/lib, so a relocated data dir needs tmpfiles
    # or the daemon fails on first open.
    testStateDirectoryForDefaultDataDir = {
      expr = (svc base "tidal-syncer").StateDirectory or null;
      expected = "tidal-syncer";
    };
    testCustomDataDirUsesTmpfiles = {
      expr = {
        hasStateDirectory = (svc customData "tidal-syncer") ? StateDirectory;
        tmpfilesType = customData.systemd.tmpfiles.settings."10-tidal-syncer"."/srv/ts".d.type or null;
        tmpfilesUser = customData.systemd.tmpfiles.settings."10-tidal-syncer"."/srv/ts".d.user or null;
      };
      expected = {
        hasStateDirectory = false;
        tmpfilesType = "d";
        tmpfilesUser = "tidal-syncer";
      };
    };

    # === G. monitoring fold-in ==============================================
    testDashboardDisabledAddsNothing = {
      expr = {
        scrape = tidalScrapeJobs base;
        providers = grafanaProviders base;
      };
      expected = {
        scrape = [ ];
        providers = [ ];
      };
    };

    # The scrape target must be DERIVED from the same two options written into
    # config.yaml. The old monitoring.nix hardcoded it, so changing the port
    # silently produced a dashboard of flatlines.
    testDashboardScrapeTargetDerived = {
      expr =
        let
          jobs = tidalScrapeJobs dashboard;
        in
        {
          count = builtins.length jobs;
          targets = (builtins.head (builtins.head jobs).static_configs).targets;
          matchesConfigYaml =
            (builtins.head (builtins.head jobs).static_configs).targets
            == [ (yamlValue "address" (renderedYaml dashboard "tidal-syncer")) ];
        };
      expected = {
        count = 1;
        targets = [ "10.0.0.5:19101" ];
        matchesConfigYaml = true;
      };
    };

    # Grafana matches folders by title. Pinning a folderUid that differs from
    # the one the alert rule creates forks a second folder with the same name.
    testDashboardGrafanaProvider = {
      expr =
        let
          providers = lib.filter (p: p.name == "tidal-syncer") (grafanaProviders dashboard);
        in
        {
          count = builtins.length providers;
          folder = (builtins.head providers).folder;
          hasFolderUid = builtins.head providers ? folderUid;
        };
      expected = {
        count = 1;
        folder = "tidal-syncer";
        hasFolderUid = false;
      };
    };

    # === H. assertions actually fire ========================================
    #
    # NixOS collects assertions rather than throwing at option-eval time, so
    # inspect the list. The baseline below is 0 for every surrounding config
    # used here, which makes a count of 1 attributable to the case under test
    # without matching on the message text.
    testBaselineHasNoFailedAssertions = {
      expr = {
        plain = failedAssertions base;
        withMonitoring = failedAssertions dashboard;
      };
      expected = {
        plain = 0;
        withMonitoring = 0;
      };
    };

    # A floor above the request rejects every track that is ever offered.
    testAssertQualityFloorAboveRequest = {
      expr = failedAssertions (cfgOf {
        quality = {
          request = "LOSSLESS";
          floor = "HI_RES_LOSSLESS";
        };
      });
      expected = 1;
    };

    testAssertTimeWindowIncomplete = {
      expr = failedAssertions (cfgOf {
        daemon = {
          mode = "time_window";
          timeWindow = {
            start = "01:00";
            end = "05:00";
            min = "1m";
            # max left null
          };
        };
      });
      expected = 1;
    };

    # A zero-length window means the daemon would never sync.
    testAssertTimeWindowStartEqualsEnd = {
      expr = failedAssertions (cfgOf {
        daemon = {
          mode = "time_window";
          timeWindow = {
            start = "01:00";
            end = "01:00";
            min = "1m";
            max = "20m";
          };
        };
      });
      expected = 1;
    };

    # Go reads TZ="" as UTC, not "inherit the host zone", so an empty string would
    # silently shift a time_window schedule. null is the way to stay on
    # /etc/localtime.
    testAssertTimeZoneEmptyString = {
      expr = failedAssertions (cfgOf { timeZone = ""; });
      expected = 1;
    };

    # Nothing would answer the scrape.
    testAssertDashboardRequiresMetrics = {
      expr = failedAssertions (evalCfg {
        metrics.enable = false;
        dashboard.enable = true;
      } [ monitoringStub ]);
      expected = 1;
    };

    testAssertDashboardRequiresPrometheus = {
      expr = failedAssertions (evalCfg {
        metrics.enable = true;
        dashboard.enable = true;
      } [
        monitoringStub
        { services.prometheus.enable = lib.mkForce false; }
      ]);
      expected = 1;
    };
  };

  failures = lib.debug.runTests cases;
in
pkgs.runCommand "tidal-syncer-eval-tests" { } (
  if failures == [ ] then
    "touch $out"
  else
    ''
      echo "tidal-syncer eval tests FAILED (${toString (builtins.length failures)} case(s)):" >&2
      echo ${lib.escapeShellArg (builtins.toJSON failures)} >&2
      exit 1
    ''
)
