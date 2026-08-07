# NixOS module: run tidal-syncer as a native systemd service.
#
# This is the opt-in alternative to the docker-compose deploy. It renders
# config.yaml from typed options (so the module and internal/config/config.go
# cannot drift), injects the TIDAL client secret out-of-band at unit start, and
# runs the daemon under a hardened unit.
#
# Consume from a host:
#   inputs.tidal-syncer.url = "git+ssh://git@github.com/labi-le/tidal-syncer";
#   imports = [ inputs.tidal-syncer.nixosModules.tidal-syncer ];
#   services.tidal-syncer = { enable = true; paths.music = "/srv/music"; ... };
#
# The older `nixosModules.monitoring` remains as-is for compose deployments;
# `services.tidal-syncer.dashboard.enable` folds the same scrape config and
# dashboard provider in here, but with the target derived from the metrics
# options instead of hardcoded.
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.services.tidal-syncer;

  # paths.music/paths.data/tidalAuth.clientSecretFile are lib.types.path, which
  # accepts a bare Nix path literal (`/srv/music`). Interpolating such a value
  # would copy the target into the /nix/store -- a slow no-op for a music
  # library and an outright secret leak for the client secret file. Stringify
  # once, here, and use only these bindings below.
  musicDir = toString cfg.paths.music;
  dataDir = toString cfg.paths.data;
  clientSecretFile = toString cfg.tidalAuth.clientSecretFile;

  exe = lib.getExe cfg.package;

  settingsFormat = pkgs.formats.yaml { };

  # An explicit null means "the operator did not choose", which is not the same
  # as "zero". Dropping the key lets the Go loader apply its own default -- most
  # importantly daemon.polling.{min,max}, which upstream derives from
  # daemon.interval when absent but would take a literal 0 if we emitted one.
  pruneNull = lib.filterAttrs (_: v: v != null);

  pollingSettings = pruneNull {
    inherit (cfg.daemon.polling) min max;
  };

  timeWindowSettings = pruneNull {
    inherit (cfg.daemon.timeWindow) start end min max;
  };

  # camelCase option names -> the snake_case YAML keys that
  # internal/config/config.go unmarshals. `paths.config` is deliberately not
  # emitted: it is declared upstream but read nowhere, and the real path is
  # already carried by --config.
  configTemplate = settingsFormat.generate "tidal-syncer.yaml" {
    paths = {
      music = musicDir;
      data = dataDir;
    };
    path_template = cfg.pathTemplate;
    scope = {
      inherit (cfg.scope) all;
      favorites = {
        inherit (cfg.scope.favorites) tracks albums playlists;
      };
    };
    quality = {
      inherit (cfg.quality) request floor;
    };
    lyrics = {
      inherit (cfg.lyrics) embed sidecar;
    };
    removal = {
      inherit (cfg.removal) policy;
    };
    daemon =
      {
        inherit (cfg.daemon) mode interval;
      }
      // lib.optionalAttrs (pollingSettings != { }) { polling = pollingSettings; }
      // lib.optionalAttrs (timeWindowSettings != { }) { time_window = timeWindowSettings; };
    jitter.worker = {
      inherit (cfg.jitter.worker) min max;
    };
    inherit (cfg) concurrency;
    tidal_auth = {
      client_id = cfg.tidalAuth.clientId;
      # Never the real secret: this file lands world-readable in the store. The
      # placeholder is substituted at ExecStartPre from a systemd credential.
      client_secret = secretPlaceholder;
    };
    log = {
      inherit (cfg.log) level format;
    };
    metrics = {
      enabled = cfg.metrics.enable;
      address = metricsTarget;
    };
  };

  secretPlaceholder = "@TIDAL_CLIENT_SECRET@";
  credentialId = "client_secret";

  # One target string feeds both the daemon's listener and Prometheus' scrape
  # config, so the two cannot disagree.
  metricsTarget = "${cfg.metrics.listenAddress}:${toString cfg.metrics.port}";

  # The daemon re-reads its config file on every cycle, and a RuntimeDirectory
  # is torn down when its owning unit stops. Sharing one directory between the
  # daemon and the (manually started, short-lived) login unit would therefore
  # yank the config out from under a running daemon the moment login exits.
  runtimeConfig = "/run/tidal-syncer/config.yaml";
  loginRuntimeConfig = "/run/tidal-syncer-login/config.yaml";

  # Materialise the real config into a RuntimeDirectory. Takes the destination
  # as $1 so both units share a single script. replace-secret reads the secret
  # from a *file*, so it never appears in argv, in the journal, or in the store.
  assembleConfig = pkgs.writeShellScript "tidal-syncer-assemble-config" ''
    set -euo pipefail
    umask 077
    ${lib.getExe' pkgs.coreutils "install"} -m 0600 ${configTemplate} "$1"
    ${lib.getExe pkgs.replace-secret} '${secretPlaceholder}' "$CREDENTIALS_DIRECTORY/${credentialId}" "$1"
  '';

  # StateDirectory= only understands names under /var/lib. If the operator
  # pointed paths.data elsewhere we must create it ourselves, because
  # store.Open never creates its parent directory (internal/store/store.go).
  dataDirIsDefault = dataDir == "/var/lib/tidal-syncer";

  # ProtectHome is load-bearing rather than hygiene here: this unit may
  # deliberately run as an existing (possibly human) uid so a library already
  # sitting in someone's home does not have to be chown'd. ProtectHome is then
  # the only thing keeping a compromised syncer out of that user's ~/.ssh,
  # dotfiles and source trees -- isolation the container got for free from its
  # own uid. So keep it on unless a configured path literally lives in /home.
  pathsUnderHome = lib.filter (p: p == "/home" || lib.hasPrefix "/home/" p) [
    dataDir
    musicDir
  ];

  commonEnvironment = {
    # The binary resolves ffmpeg *only* from $TIDAL_FFMPEG, falling back to a
    # hardcoded /usr/local/bin/ffmpeg that does not exist on NixOS
    # (internal/sync/wiring.go, cmd/health.go). It never consults $PATH, so
    # `path = [ cfg.ffmpegPackage ]` would be a no-op -- and the resulting
    # ErrFFmpeg is classified TRANSIENT and retried forever, i.e. every DASH
    # download would fail silently and permanently.
    TIDAL_FFMPEG = lib.getExe cfg.ffmpegPackage;
  }
  // lib.optionalAttrs (cfg.timeZone != null) { TZ = cfg.timeZone; };

  commonServiceConfig = {
    User = cfg.user;
    Group = cfg.group;
    WorkingDirectory = dataDir;

    LoadCredential = [ "${credentialId}:${clientSecretFile}" ];

    # The FLAC library is deliberately world-readable so media servers (Jellyfin,
    # Navidrome, ...) running as other uids can read it. Do not "harden" this to
    # 0077: it would not fix anything already on disk, it would only make *new*
    # albums silently unreadable. The SQLite state is chmod-forced to 0600 by
    # the application itself, so tightening the umask buys nothing.
    UMask = "0022";

    NoNewPrivileges = true;
    ProtectSystem = "strict";
    ProtectHome = pathsUnderHome == [ ];
    PrivateDevices = true;
    PrivateMounts = true;
    ProtectClock = true;
    ProtectHostname = true;
    ProtectKernelLogs = true;
    ProtectKernelModules = true;
    ProtectKernelTunables = true;
    ProtectControlGroups = true;
    ProtectProc = "invisible";
    ProcSubset = "pid";
    RestrictNamespaces = true;
    RestrictRealtime = true;
    RestrictSUIDSGID = true;
    LockPersonality = true;
    RemoveIPC = true;
    CapabilityBoundingSet = [ "" ];
    AmbientCapabilities = [ "" ];
    SystemCallArchitectures = "native";
    SystemCallFilter = [
      "@system-service"
      "~@privileged"
      "~@resources"
    ];
    RestrictAddressFamilies = [
      "AF_INET"
      "AF_INET6"
      "AF_UNIX"
    ];

    # --- The next two are spelled out because a future hardening pass would
    # --- otherwise flip them, and both failures are SILENT. Do not "fix" this.

    # Tag writing goes through go.senan.xyz/taglib, which is TagLib compiled to
    # WASM and executed by wazero. wazero's compiler mmaps RW then mprotects
    # PROT_READ|PROT_EXEC; W^X enforcement kills every single FLAC tag write.
    MemoryDenyWriteExecute = false;

    # A requirement, not hardening: taglib opens a wazero compilation cache
    # under os.TempDir() at init and hard-fails if it cannot create it. Keep
    # this true and never add /tmp to ReadOnlyPaths.
    PrivateTmp = true;

    ReadWritePaths = lib.unique [
      dataDir
      musicDir
    ];
  }
  // lib.optionalAttrs dataDirIsDefault {
    StateDirectory = "tidal-syncer";
    StateDirectoryMode = "0750";
  };

  # This is what makes the module filesystem-agnostic: systemd resolves each
  # path to whatever .mount/.automount unit provides it -- local disk, NFS,
  # sshfs or any other FUSE, WebDAV, autofs -- and orders the unit after it,
  # so we need no knowledge of how the library is stored. Residual caveat: a
  # network mount that drops and remounts *after* the unit started can leave a
  # stale view inside the unit's mount namespace; signalling that is the mount
  # unit's job, not this module's.
  commonUnitConfig = {
    RequiresMountsFor = lib.unique [
      dataDir
      musicDir
    ];
  }
  # Ordering alone cannot help when systemd owns no mount unit for the path (an
  # out-of-band sshfs/rclone/davfs mount), so an explicit assertion is the only
  # way to refuse to start on a still-empty mount point. Opt-in: asserting a
  # plain local directory is a mount point would never let the unit start.
  // lib.optionalAttrs (cfg.requireMountPoints != [ ]) {
    AssertPathIsMountPoint = cfg.requireMountPoints;
  };

  wildcardAddresses = [
    ""
    "0.0.0.0"
    "::"
    "[::]"
  ];
in
{
  imports = [ ./options.nix ];

  config = lib.mkIf cfg.enable (lib.mkMerge [
    {
      systemd.services.tidal-syncer = {
        description = "tidal-syncer: TIDAL-to-local-FLAC sync daemon";
        documentation = [ "https://github.com/labi-le/tidal-syncer" ];
        wantedBy = [ "multi-user.target" ];
        wants = [ "network-online.target" ];
        after = [ "network-online.target" ];

        environment = commonEnvironment;
        unitConfig = commonUnitConfig;

        serviceConfig = commonServiceConfig // {
          Type = "simple";
          Restart = "on-failure";
          RestartSec = "30s";
          # Parity with the compose deployment's stop_grace_period: a cycle in
          # flight gets a chance to finish the current file.
          TimeoutStopSec = 30;
          SyslogIdentifier = "tidal-syncer";

          RuntimeDirectory = "tidal-syncer";
          RuntimeDirectoryMode = "0700";

          ExecStartPre = [
            "${assembleConfig} ${runtimeConfig}"
            # selfcheck loads and validates the config, pings the store and
            # execs `ffmpeg -version`, so a broken deploy fails here rather
            # than turning into an infinite "transient" retry loop later.
            # --config is a persistent flag: it must precede the subcommand.
            "${exe} --config ${runtimeConfig} selfcheck"
          ];
          ExecStart = "${exe} --config ${runtimeConfig} daemon";
        };
      };

      # Device-code login. Never started automatically: it blocks until a human
      # approves the code, and the token it writes into the store outlives it.
      systemd.services.tidal-syncer-login = {
        description = "tidal-syncer: TIDAL device-code login";
        documentation = [ "https://github.com/labi-le/tidal-syncer" ];
        wantedBy = [ ];
        wants = [ "network-online.target" ];
        after = [ "network-online.target" ];

        environment = commonEnvironment;
        unitConfig = commonUnitConfig;

        serviceConfig = commonServiceConfig // {
          Type = "oneshot";
          SyslogIdentifier = "tidal-syncer-login";
          # Wall-clock bound on how long the operator has to open the URL and
          # approve; the command polls TIDAL until then.
          TimeoutStartSec = 900;

          # Deliberately not the daemon's directory -- see runtimeConfig above.
          RuntimeDirectory = "tidal-syncer-login";
          RuntimeDirectoryMode = "0700";

          ExecStartPre = [ "${assembleConfig} ${loginRuntimeConfig}" ];
          ExecStart = "${exe} --config ${loginRuntimeConfig} login";
        };
      };

      environment.systemPackages = [
        cfg.package
        # `login` is not tty-interactive: it prints the verification URL at
        # zerolog's no-level and then polls, so the URL surfaces in the journal.
        # Start the unit and tail it in one command (same idiom as the restic
        # module's createWrapper).
        (pkgs.writeShellScriptBin "tidal-syncer-login" ''
          set -euo pipefail
          systemctl start --no-block tidal-syncer-login.service
          exec journalctl -f -u tidal-syncer-login.service
        '')
      ];

      # store.Open does not create its parent, and StateDirectory= only covers
      # the default location, so a relocated data dir needs tmpfiles instead.
      systemd.tmpfiles.settings = lib.mkIf (!dataDirIsDefault) {
        "10-tidal-syncer".${dataDir}.d = {
          user = cfg.user;
          group = cfg.group;
          mode = "0750";
        };
      };

      # Only ever declare the *default* account. If the operator pointed
      # user/group at an existing (possibly human) account, that account is
      # theirs to manage -- redeclaring it here would fight their own
      # users.users entry and could silently change its shell, home or type.
      users.users = lib.mkIf (cfg.user == "tidal-syncer") {
        tidal-syncer = {
          isSystemUser = true;
          group = cfg.group;
          home = dataDir;
        };
      };

      users.groups = lib.mkIf (cfg.group == "tidal-syncer") {
        tidal-syncer = { };
      };

      assertions = [
        {
          # An empty TZ is not "inherit the host zone" -- Go reads TZ="" as UTC,
          # silently shifting a time_window schedule. Omitting the option (null)
          # is what leaves the unit on /etc/localtime.
          assertion = cfg.timeZone != "";
          message = "services.tidal-syncer.timeZone must not be the empty string: an empty TZ means UTC, not the host zone. Use null to inherit time.timeZone.";
        }
        {
          assertion = cfg.dashboard.enable -> cfg.metrics.enable;
          message = "services.tidal-syncer.dashboard.enable requires services.tidal-syncer.metrics.enable: nothing would answer the scrape.";
        }
        {
          assertion = cfg.dashboard.enable -> config.services.prometheus.enable;
          message = "services.tidal-syncer.dashboard.enable adds a scrape config but services.prometheus.enable is false.";
        }
        {
          assertion =
            cfg.dashboard.enable
            -> (config.services.grafana.enable && config.services.grafana.provision.enable);
          message = "services.tidal-syncer.dashboard.enable requires services.grafana.enable and services.grafana.provision.enable.";
        }
        {
          # The floor is a minimum acceptable tier, so it can never sit above
          # what we ask for -- every track would be rejected.
          assertion = !(cfg.quality.request == "LOSSLESS" && cfg.quality.floor == "HI_RES_LOSSLESS");
          message = "services.tidal-syncer.quality.floor (HI_RES_LOSSLESS) exceeds quality.request (LOSSLESS); no track could ever satisfy it.";
        }
        {
          assertion =
            cfg.daemon.mode == "time_window"
            -> (
              cfg.daemon.timeWindow.start != null
              && cfg.daemon.timeWindow.end != null
              && cfg.daemon.timeWindow.min != null
              && cfg.daemon.timeWindow.max != null
              && cfg.daemon.timeWindow.start != cfg.daemon.timeWindow.end
            );
          message = "services.tidal-syncer.daemon.mode = \"time_window\" requires daemon.timeWindow.{start,end,min,max} to all be set, with start != end.";
        }
      ];

      warnings =
        lib.optional
          (
            !cfg.scope.all
            && !cfg.scope.favorites.tracks
            && !cfg.scope.favorites.albums
            && !cfg.scope.favorites.playlists
          )
          "services.tidal-syncer: nothing is in scope (scope.all and all scope.favorites.* are false); the daemon will run every cycle and sync nothing."
        ++ lib.optional (cfg.metrics.enable && lib.elem cfg.metrics.listenAddress wildcardAddresses)
          "services.tidal-syncer.metrics.listenAddress is a wildcard (${cfg.metrics.listenAddress}); the /metrics endpoint is unauthenticated and would be reachable from off-host."
        ++ lib.optional (pathsUnderHome != [ ]) (
          "services.tidal-syncer: ProtectHome had to be disabled so the unit can reach "
          + lib.concatStringsSep ", " pathsUnderHome
          + ". If the service runs as a human's uid this widens the blast radius to that user's entire home (~/.ssh, dotfiles, source trees). Move the library outside /home, or bind-mount it in, to restore ProtectHome."
        );
    }

    (lib.mkIf cfg.dashboard.enable {
      # Target derived from the same options written into config.yaml, so the
      # daemon's listener and Prometheus cannot disagree.
      services.prometheus.scrapeConfigs = [
        {
          job_name = cfg.dashboard.jobName;
          static_configs = [ { targets = [ metricsTarget ]; } ];
        }
      ];

      # `providers` is a listOf and `apiVersion` is a types.int merged with
      # mergeEqualOption, so declaring both here coexists with any other module
      # provisioning its own dashboards (as long as it also says apiVersion = 1).
      services.grafana.provision.dashboards.settings = {
        apiVersion = 1;
        providers = [
          {
            name = cfg.dashboard.jobName;
            type = "file";
            disableDeletion = true;
            # Matched by title, so it lands in the same folder the alert rule
            # creates instead of forking a duplicate. No folderUid: pinning a
            # uid that differs from the alert folder's would fork a second one.
            folder = cfg.dashboard.folder;
            options = {
              path = ./grafana;
              foldersFromFilesStructure = false;
            };
          }
        ];
      };
    })
  ]);
}
