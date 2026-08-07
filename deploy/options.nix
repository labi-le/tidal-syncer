# NixOS option declarations for the native tidal-syncer service. Split out from
# deploy/service.nix so the interface can be read (and diffed against
# internal/config/config.go) without wading through unit plumbing.
#
# This file is a pure options module: no `config`, no `imports`. deploy/service.nix
# imports it and is the only consumer.
#
# The tree below is a fully typed mirror of config.yaml rather than a freeform
# `settings` attrset. That is affordable precisely because the module ships in the
# same repo as the loader it mirrors, so the two cannot drift silently — and it is
# necessary because the loader neither interpolates environment variables nor
# rejects unknown keys (internal/config/config.go): a typo in a freeform attrset
# would be accepted by YAML, ignored by the app, and debugged at 2am. Every key
# here is checked at evaluation time instead.
{ lib, pkgs, ... }:

let
  # Go duration strings ("15m", "4h30m", "0s") are parsed by go-yaml straight into
  # time.Duration. A bare number is *also* valid YAML there and means NANOSECONDS,
  # so `interval = 15` would poll ~67 million times a second rather than every 15
  # minutes. Typing these as regex-checked strings makes the unit suffix mandatory
  # and turns that class of mistake into an evaluation error.
  duration = lib.types.strMatching "^[0-9]+(ns|us|ms|s|m|h)([0-9]+(ns|us|ms|s|m|h))*$";

  # Wall-clock "HH:MM" in 24-hour form. Same reasoning: the app takes these as
  # plain strings and only discovers a malformed value at runtime, mid-daemon-loop.
  # The regex pins the hour to 00-23 and the minute to 00-59 so "24:00" or "7:5"
  # fail here instead of at the first schedule evaluation.
  timeOfDay = lib.types.strMatching "^([01][0-9]|2[0-3]):[0-5][0-9]$";
in
{
  options.services.tidal-syncer = {
    enable = lib.mkEnableOption "tidal-syncer, a TIDAL-to-local-FLAC sync daemon";

    package = lib.mkPackageOption pkgs "tidal-syncer" { };

    ffmpegPackage = lib.mkPackageOption pkgs "ffmpeg" {
      default = "ffmpeg-headless";
      example = "ffmpeg-full";
    };

    user = lib.mkOption {
      type = lib.types.str;
      default = "tidal-syncer";
      description = ''
        System user the unit runs as. It must be able to WRITE the music tree at
        {option}`services.tidal-syncer.paths.music`, since that is where albums are
        created.

        If the library is shared with other software — a media server, an NFS or
        Samba export, another sync tool — set this to the identity that already
        owns those files rather than chowning the whole tree away from them.

        `DynamicUser` is deliberately not used: the one-time `tidal-syncer login`
        has to run as the same identity to write its token into the store under
        {option}`services.tidal-syncer.paths.data`, and a transient uid owning
        files on a shared or exported library is wrong — the numeric owner would
        change on every restart and mean something different to every NFS client.
      '';
    };

    group = lib.mkOption {
      type = lib.types.str;
      default = "tidal-syncer";
      description = ''
        System group the unit runs as. Reuse the group that already owns a shared
        music library instead of chowning the tree; see
        {option}`services.tidal-syncer.user` for why a static identity is used
        rather than `DynamicUser`.
      '';
    };

    paths = {
      music = lib.mkOption {
        type = lib.types.path;
        example = "/srv/media/music";
        description = ''
          Absolute path to the root of the FLAC library. Required; there is no
          sensible default for someone else's media layout.

          Any absolute path is acceptable and the filesystem type is irrelevant to
          the module — local disk, NFS, sshfs, WebDAV, autofs. `RequiresMountsFor`
          is derived from this path, so systemd orders the unit after whichever
          `.mount`/`.automount` unit provides it (triggering an automount on first
          access).

          That ordering only exists if systemd knows about the mount: an fstab
          entry, `x-systemd.automount`, or an explicit `.mount`/`.automount` unit.
          A share mounted out-of-band — a user-session sshfs, a hand-run
          `mount.fuse` or rclone — has no such unit, `RequiresMountsFor` silently
          resolves to nothing, and the daemon may start on the still-empty mount
          point. See {option}`services.tidal-syncer.requireMountPoints`.

          If the path lives under `/home`, `ProtectHome` is relaxed automatically
          and the service module emits a warning explaining the trade-off.
        '';
      };

      data = lib.mkOption {
        type = lib.types.path;
        default = "/var/lib/tidal-syncer";
        description = ''
          Directory holding the SQLite state database and the OAuth token written
          by `tidal-syncer login`. The application does not create this directory,
          so the module provisions it.
        '';
      };
    };

    pathTemplate = lib.mkOption {
      type = lib.types.str;
      default = "{albumartist}/{album}/{track} - {title}.{ext}";
      example = "{albumartist}/{year} - {album}/{track}. {title}.{ext}";
      description = ''
        Layout of each file below {option}`services.tidal-syncer.paths.music`,
        expanded per track. Changing it after a sync does not move existing files;
        the old layout stays and the new one is written alongside it.
      '';
    };

    scope = {
      all = lib.mkOption {
        type = lib.types.bool;
        default = false;
        description = ''
          Sync the entire TIDAL library rather than only the collections selected
          under {option}`services.tidal-syncer.scope.favorites`.
        '';
      };

      favorites = {
        tracks = lib.mkOption {
          type = lib.types.bool;
          default = false;
          description = "Sync favorited individual tracks.";
        };

        albums = lib.mkOption {
          type = lib.types.bool;
          default = false;
          description = "Sync favorited albums.";
        };

        playlists = lib.mkOption {
          type = lib.types.bool;
          default = false;
          description = "Sync favorited playlists.";
        };
      };
    };

    quality = {
      request = lib.mkOption {
        type = lib.types.enum [
          "LOSSLESS"
          "HI_RES_LOSSLESS"
        ];
        default = "HI_RES_LOSSLESS";
        description = ''
          Highest audio tier to ask TIDAL for. Requesting a tier the account is not
          entitled to is not an error: the grant is simply lower, and the outcome is
          then decided by {option}`services.tidal-syncer.quality.floor`.
        '';
      };

      floor = lib.mkOption {
        type = lib.types.enum [
          "LOSSLESS"
          "HI_RES_LOSSLESS"
        ];
        default = "LOSSLESS";
        description = ''
          Lowest audio tier that is still acceptable. The floor may not exceed
          {option}`services.tidal-syncer.quality.request` — the service module
          asserts this, since a floor above the request can never be satisfied and
          would fail every single track.

          A grant below the floor is refused and nothing is written: TIDAL answers
          HTTP 200 with a lossy AAC stream when the account lacks the entitlement,
          and muxing that into a `.flac` would quietly seed the library with files
          that are lossy despite their extension.
        '';
      };
    };

    lyrics = {
      embed = lib.mkOption {
        type = lib.types.bool;
        default = true;
        description = "Embed fetched lyrics into the FLAC tags.";
      };

      sidecar = lib.mkOption {
        type = lib.types.bool;
        default = true;
        description = ''
          Write fetched lyrics next to the track as a `.lrc` sidecar, for players
          that ignore embedded lyrics tags.
        '';
      };
    };

    removal = {
      policy = lib.mkOption {
        type = lib.types.enum [
          "keep"
          "mirror"
          "trash"
        ];
        default = "keep";
        description = ''
          What to do with a local file once the track leaves the remote library:
          `keep` leaves it alone, `mirror` deletes it so the tree matches TIDAL
          exactly, `trash` moves it aside instead of unlinking it.

          The default is `keep` because it is the only non-destructive option: a
          transient API hiccup that under-reports the library must never be able to
          delete music.
        '';
      };
    };

    daemon = {
      mode = lib.mkOption {
        type = lib.types.enum [
          "polling"
          "time_window"
        ];
        default = "polling";
        description = ''
          `polling` runs a sync every {option}`services.tidal-syncer.daemon.interval`
          around the clock. `time_window` instead confines syncing to a daily
          wall-clock window, which is what you want on a metered link or a shared
          NAS that should stay quiet during the day.
        '';
      };

      interval = lib.mkOption {
        type = duration;
        default = "15m";
        example = "1h";
        description = ''
          Base delay between sync cycles in `polling` mode, and the value from which
          the application derives the polling bounds when they are left unset. Must
          carry a unit suffix.
        '';
      };

      polling = {
        min = lib.mkOption {
          type = lib.types.nullOr duration;
          default = null;
          example = "10m";
          description = ''
            Lower bound of the randomized delay between cycles in `polling` mode.

            `null` omits the key from the generated YAML entirely, so the
            application's own derivation applies — upstream defaults both polling
            bounds to {option}`services.tidal-syncer.daemon.interval`. Emitting an
            explicit zero here would instead pin the bound to zero, which is a
            different thing.
          '';
        };

        max = lib.mkOption {
          type = lib.types.nullOr duration;
          default = null;
          example = "20m";
          description = ''
            Upper bound of the randomized delay between cycles in `polling` mode.

            `null` omits the key from the generated YAML, leaving the application to
            derive it from {option}`services.tidal-syncer.daemon.interval`.
          '';
        };
      };

      timeWindow = {
        start = lib.mkOption {
          type = lib.types.nullOr timeOfDay;
          default = null;
          example = "02:00";
          description = ''
            Local wall-clock time at which the daily sync window opens, `HH:MM`.

            `null` omits the key from the generated YAML. `time_window` mode
            requires all four `timeWindow` options to be set — the service module
            asserts this, because a half-specified window is accepted by the loader
            and then silently never opens.

            See {option}`services.tidal-syncer.timeZone`: the window is evaluated
            against the process's local clock.
          '';
        };

        end = lib.mkOption {
          type = lib.types.nullOr timeOfDay;
          default = null;
          example = "06:00";
          description = ''
            Local wall-clock time at which the daily sync window closes, `HH:MM`. A
            window that wraps midnight (`end` earlier than `start`) is fine.

            `null` omits the key from the generated YAML; `time_window` mode
            requires all four `timeWindow` options, which the service module
            asserts.
          '';
        };

        min = lib.mkOption {
          type = lib.types.nullOr duration;
          default = null;
          example = "30m";
          description = ''
            Lower bound of the randomized delay between cycles while the window is
            open.

            `null` omits the key from the generated YAML; `time_window` mode
            requires all four `timeWindow` options, which the service module
            asserts.
          '';
        };

        max = lib.mkOption {
          type = lib.types.nullOr duration;
          default = null;
          example = "90m";
          description = ''
            Upper bound of the randomized delay between cycles while the window is
            open.

            `null` omits the key from the generated YAML; `time_window` mode
            requires all four `timeWindow` options, which the service module
            asserts.
          '';
        };
      };
    };

    jitter = {
      worker = {
        min = lib.mkOption {
          type = duration;
          default = "0s";
          example = "500ms";
          description = ''
            Lower bound of the random pause each download worker takes between
            tracks. Non-zero jitter makes the request pattern look less like a
            scraper and spreads load off a single instant.
          '';
        };

        max = lib.mkOption {
          type = duration;
          default = "0s";
          example = "3s";
          description = ''
            Upper bound of the random pause each download worker takes between
            tracks.
          '';
        };
      };
    };

    concurrency = lib.mkOption {
      type = lib.types.ints.between 1 8;
      default = 3;
      description = ''
        Number of tracks downloaded in parallel. The upper bound is a deliberate
        guard rail rather than a performance ceiling: TIDAL rate-limits, and a
        higher number buys throttling and failed tracks, not speed.
      '';
    };

    tidalAuth = {
      clientId = lib.mkOption {
        type = lib.types.str;
        example = "zU4XHVVkc2tDPo4t";
        description = ''
          TIDAL OAuth client id. Required and non-empty; the loader rejects a blank
          value.

          This is NOT a secret: it is rendered directly into the config template in
          the Nix store. Only the matching client secret needs protecting, and it
          has its own option.
        '';
      };

      clientSecretFile = lib.mkOption {
        type = lib.types.path;
        example = lib.literalExpression "config.age.secrets.tidal-syncer.path";
        description = ''
          Path to a file whose CONTENT is the raw TIDAL OAuth client secret — the
          secret itself, not a `KEY=value` line and not a path to one.

          The file is handed to the unit through systemd `LoadCredential`, which
          PID 1 reads as root before privileges are dropped, so the file needs no
          particular owner or mode and may stay `0400 root:root` — it does not have
          to be readable by {option}`services.tidal-syncer.user`. ExecStartPre then
          substitutes the credential into the runtime config under `/run`, so the
          secret NEVER enters the world-readable Nix store.

          This is why the secret has no plain-string counterpart: any such option
          would end up in a store path readable by every user on the machine.
        '';
      };
    };

    log = {
      level = lib.mkOption {
        type = lib.types.enum [
          "trace"
          "debug"
          "info"
          "warn"
          "error"
          "fatal"
          "panic"
        ];
        default = "info";
        description = "Minimum severity written to the journal.";
      };

      format = lib.mkOption {
        type = lib.types.enum [
          "console"
          "json"
        ];
        default = "console";
        description = ''
          `console` is human-readable and is the right default under `journalctl`.
          Choose `json` when a log shipper parses the journal downstream.
        '';
      };
    };

    metrics = {
      enable = lib.mkOption {
        type = lib.types.bool;
        default = false;
        description = ''
          Expose the Prometheus `/metrics` endpoint from the daemon. This only makes
          the daemon serve metrics; it does not make anything scrape them — see
          {option}`services.tidal-syncer.dashboard.enable`.
        '';
      };

      listenAddress = lib.mkOption {
        type = lib.types.str;
        default = "127.0.0.1";
        example = "0.0.0.0";
        description = ''
          Address the metrics endpoint binds to.

          The default is loopback deliberately, and differs from the application's
          own default of `:9101`. That upstream default binds EVERY interface, which
          was safe under docker-compose only because the container's network
          namespace contained it and the compose file republished it on loopback.
          Running natively there is no namespace, so the same value would put an
          unauthenticated endpoint on the LAN. Loopback is the correct default
          because Prometheus scrapes it over loopback; widen this only when the
          scraper genuinely lives on another host.
        '';
      };

      port = lib.mkOption {
        type = lib.types.port;
        default = 9101;
        description = ''
          TCP port for the metrics endpoint. Combined with
          {option}`services.tidal-syncer.metrics.listenAddress` to form the
          `metrics.address` the application binds.
        '';
      };
    };

    dashboard = {
      enable = lib.mkOption {
        type = lib.types.bool;
        default = false;
        description = ''
          Make THIS host scrape the daemon and provision its Grafana dashboard.

          This is distinct from {option}`services.tidal-syncer.metrics.enable`,
          which only makes the daemon expose the endpoint. A host that ships metrics
          to a remote Prometheus wants `metrics.enable = true` with
          `dashboard.enable = false`; enabling this without a local Prometheus and
          Grafana would provision a dashboard with nothing behind it.
        '';
      };

      jobName = lib.mkOption {
        type = lib.types.str;
        default = "tidal-syncer";
        description = ''
          Prometheus `job_name` for the generated scrape config. Change it only to
          avoid colliding with an existing job on the same Prometheus, since the
          label ends up on every sample.
        '';
      };

      folder = lib.mkOption {
        type = lib.types.str;
        default = "tidal-syncer";
        description = ''
          Grafana folder the dashboard is provisioned into. Grafana matches folders
          by title, so leaving this aligned with any other tidal-syncer provider on
          the host keeps them in one folder instead of forking a second.
        '';
      };
    };

    requireMountPoints = lib.mkOption {
      type = lib.types.listOf lib.types.path;
      default = [ ];
      example = [ "/srv/media/music" ];
      description = ''
        Paths that must already be mount points when the daemon starts, emitted as
        systemd `AssertPathIsMountPoint=`. The unit refuses to start otherwise.

        Use this for a library on a share systemd does not own a mount unit for
        (out-of-band sshfs/rclone/davfs), where the derived `RequiresMountsFor`
        cannot order anything. The failure it prevents is severe and silent: the
        skip decision consults only the database, never the disk, so an empty music
        root does not trigger re-downloads — the library would stay empty forever
        while every track keeps counting as done. An assertion turns that into a
        loud refusal to start.

        Leave empty for a library on ordinary local storage: asserting a plain
        directory is a mount point would never let the unit start.
      '';
    };

    timeZone = lib.mkOption {
      type = lib.types.nullOr lib.types.str;
      default = null;
      example = "Europe/Moscow";
      description = ''
        Time zone the unit runs in.

        This matters because the `time_window` schedule is evaluated against the
        process's LOCAL clock: a host whose zone differs from the one the window was
        written for silently shifts that window by the offset, with no error and no
        log line — the sync just happens at the wrong hours.

        `null` inherits the host's {option}`time.timeZone`. A string pins `TZ` for
        this unit only, leaving the rest of the system untouched, which is what you
        want when the schedule is expressed in a zone the host does not use.
      '';
    };
  };
}
