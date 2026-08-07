# NixOS VM integration test for the tidal-syncer service module.
#
# Everything here guards a failure that is SILENT in production: the daemon
# classifies almost every error as transient and retries forever, so a broken
# deploy does not crash, does not exit non-zero, and does not alert. It just
# quietly downloads nothing. Evaluation tests (deploy/tests/eval.nix) can only
# prove what the module *renders*; only a booted machine can prove that systemd
# accepts the unit, that the sandbox does not veto wazero's JIT, that the secret
# reaches /run without ever passing through the store, and that Prometheus can
# actually reach the listener the module told it about.
#
# Run standalone:
#   nix build --impure --no-link --expr 'let pkgs = (builtins.getFlake "/home/labile/nix").inputs.nixpkgs.legacyPackages.x86_64-linux; in pkgs.callPackage ./deploy/tests/module.nix { }'
{ pkgs, ... }:

let
  # Written to /etc/tidal-secret and expected verbatim in the runtime config.
  # Also the needle for the "never entered the Nix store" assertion, so it has
  # to be a string that could not plausibly occur anywhere else in the closure.
  sentinel = "s3cr3t-sentinel";
  secretFile = "/etc/tidal-secret";
in
pkgs.testers.runNixOSTest {
  name = "tidal-syncer-module";

  # runNixOSTest pins the node's `pkgs` and makes every nixpkgs.* option
  # read-only. The module's package option defaults to `pkgs.tidal-syncer`,
  # which only an overlay supplies, so the node has to build its own pkgs.
  node.pkgsReadOnly = false;

  nodes.machine =
    { config, pkgs, ... }:
    {
      imports = [ ../service.nix ];

      # The module's package option defaults to pkgs.tidal-syncer, which only
      # the flake overlay provides. Re-create it here so the test is runnable
      # via a bare callPackage of this file, with no flake in the picture.
      nixpkgs.overlays = [ (final: _: { tidal-syncer = final.callPackage ../package.nix { }; }) ];

      virtualisation.memorySize = 2048;
      virtualisation.graphics = false;

      # The secret must exist before the unit starts: LoadCredential= is a
      # hard failure, not a warning, if the source file is missing. Mode 0400
      # root-only mirrors production; PID 1 reads it before dropping privileges.
      environment.etc."tidal-secret" = {
        text = sentinel;
        mode = "0400";
      };

      services.tidal-syncer = {
        enable = true;
        paths.music = "/srv/music";
        tidalAuth.clientId = "test-client-id";
        tidalAuth.clientSecretFile = secretFile;
        scope.favorites.tracks = true;
        metrics.enable = true;
        dashboard.enable = true;
        concurrency = 1;
      };

      # ProtectSystem=strict turns ReadWritePaths= into a hard requirement: a
      # non-existent /srv/music makes the mount namespace setup fail and the
      # unit never starts. Production gets this directory from whatever mount
      # unit backs the library; the VM has to make it itself.
      systemd.tmpfiles.settings."20-tidal-syncer-test"."/srv/music".d = {
        user = config.services.tidal-syncer.user;
        group = config.services.tidal-syncer.group;
        mode = "0755";
      };

      # Exercise the dashboard.enable fold-in against the real monitoring
      # stack rather than only asserting on rendered config: the point of
      # deriving the scrape target from metrics.{listenAddress,port} is that
      # Prometheus and the daemon cannot disagree, and only a live scrape
      # proves they agree.
      services.prometheus = {
        enable = true;
        port = 9090;
        # Test-harness speed only: the default 1m interval would put a full
        # minute between boot and the first scrape result.
        globalConfig.scrape_interval = "5s";
      };

      services.grafana = {
        enable = true;
        provision.enable = true;
        settings.server.http_port = 3000;
        # The grafana module refuses to evaluate without one. Nothing in this
        # test stores a secret in Grafana's DB, so a constant is fine.
        settings.security.secret_key = "nixos-test-not-a-secret";
      };

      # The login unit's whole job is to reach auth.tidal.com. With no route
      # off the VM it would fail within milliseconds, tearing down its
      # RuntimeDirectory before the test could look inside it -- and the
      # contents of that directory are exactly what assertion 6 is about.
      # Pointing the host at a local TCP tarpit (accepts, never speaks) leaves
      # the unit parked in `activating` for its whole TimeoutStartSec, which
      # makes the assertion deterministic instead of a race. It does not touch
      # the daemon: with an empty token store the daemon returns
      # ErrReauthRequired before it ever opens a socket.
      networking.extraHosts = "127.0.0.1 auth.tidal.com";

      systemd.services.tidal-auth-tarpit = {
        description = "TCP tarpit standing in for auth.tidal.com";
        wantedBy = [ "multi-user.target" ];
        before = [ "tidal-syncer.service" ];
        serviceConfig.ExecStart = "${pkgs.socat}/bin/socat TCP-LISTEN:443,fork,reuseaddr SYSTEM:'sleep 3600'";
      };

      environment.systemPackages = [ pkgs.jq ];
    };

  testScript = ''
    import re

    SENTINEL = "${sentinel}"
    PLACEHOLDER = "@TIDAL_CLIENT_SECRET@"
    DAEMON_CFG = "/run/tidal-syncer/config.yaml"
    LOGIN_CFG = "/run/tidal-syncer-login/config.yaml"

    machine.start()

    with subtest("daemon reaches active"):
        # `active` is the correct assertion even though the credentials are
        # bogus and the VM has no route to TIDAL. The daemon only ever returns
        # a non-nil error from its loop on cancellation (cmd/daemon.go
        # runDaemonCycle): a missing token logs "re-authentication required"
        # and the loop keeps polling. So "still active on bad credentials" is
        # the designed behaviour, not a false pass -- and reaching active at
        # all proves both ExecStartPre steps ran green, i.e. the config was
        # assembled from the credential AND `selfcheck` validated it, opened
        # the store and successfully exec'd the wrapped ffmpeg.
        machine.wait_for_unit("tidal-syncer.service")

    with subtest("secret is injected at runtime and never enters the store"):
        cfg = machine.succeed(f"cat {DAEMON_CFG}")
        assert SENTINEL in cfg, f"runtime config lacks the injected secret:\n{cfg}"
        assert PLACEHOLDER not in cfg, f"placeholder survived substitution:\n{cfg}"

        # 0600 and owned by the service user: the file now holds the plaintext
        # client secret, so anything looser is a local disclosure.
        perms = machine.succeed(f"stat -c '%a %U' {DAEMON_CFG}").strip()
        assert perms == "600 tidal-syncer", f"unexpected runtime config perms/owner: {perms}"

        # Resolve the *template* that shipped in the world-readable store and
        # prove the secret is not in it. The unit references the assemble
        # script; the script references the template.
        unit = machine.succeed("systemctl cat tidal-syncer.service")
        script = re.search(r"(/nix/store/\S+-tidal-syncer-assemble-config)", unit)
        assert script, f"could not find the assemble-config script in:\n{unit}"
        body = machine.succeed(f"cat {script.group(1)}")
        template = re.search(r"(/nix/store/\S+-tidal-syncer\.yaml)", body)
        assert template, f"could not find the config template in:\n{body}"

        machine.succeed(f"grep -q -- '{PLACEHOLDER}' {template.group(1)}")
        machine.fail(f"grep -q -- '{SENTINEL}' {template.group(1)}")
        # The script itself must not carry it either (replace-secret reads the
        # secret from a file precisely so it never lands in argv or the store).
        machine.fail(f"grep -q -- '{SENTINEL}' {script.group(1)}")

    with subtest("metrics endpoint answers on loopback only"):
        machine.wait_for_open_port(9101, addr = "127.0.0.1")
        metrics = machine.succeed("curl -sf http://127.0.0.1:9101/metrics")
        assert "tidal_syncer_build_info" in metrics, "build_info gauge missing from /metrics"

        # Upstream's default metrics.address is ":9101", which binds every
        # interface. Under docker-compose that was contained by the network
        # namespace; on a host it is an unauthenticated endpoint on the LAN.
        # The module defaults listenAddress to 127.0.0.1 -- assert the socket,
        # not just the option.
        listeners = machine.succeed("ss -ltnH 'sport = :9101'")
        assert "127.0.0.1:9101" in listeners, f"9101 is not bound to loopback:\n{listeners}"
        for wildcard in ("0.0.0.0:9101", "*:9101", "[::]:9101"):
            assert wildcard not in listeners, f"9101 is bound to {wildcard}:\n{listeners}"

    with subtest("load-bearing sandbox settings survived"):
        def prop(unit, name):
            return machine.succeed(f"systemctl show -p {name} --value {unit}").strip()

        # Tag writing is TagLib compiled to WASM, executed by wazero. wazero's
        # compiler mmaps RW then mprotects READ|EXEC; W^X enforcement would
        # abort every single FLAC tag write.
        assert prop("tidal-syncer", "MemoryDenyWriteExecute") == "no"

        # Not hardening but a requirement: taglib creates a wazero compilation
        # cache under os.TempDir() at init and hard-fails without a writable
        # one. PrivateTmp supplies it; a read-only /tmp breaks tagging outright.
        assert prop("tidal-syncer", "PrivateTmp") == "yes"

        # The FLAC library must stay world-readable so media servers running as
        # other uids can serve it. Tightening to 0077 would not fix anything
        # already on disk -- it would silently make only NEW albums unreadable.
        assert prop("tidal-syncer", "UMask") == "0022"

        # Neither configured path is under /home here, so the module must
        # compute ProtectHome=true. It is the only thing keeping a compromised
        # syncer out of the home directory when the unit is deliberately run as
        # an existing human uid to avoid chowning a shared library.
        assert prop("tidal-syncer", "ProtectHome") == "yes"

    with subtest("state and music directories"):
        state = machine.succeed("stat -c '%U' /var/lib/tidal-syncer").strip()
        assert state == "tidal-syncer", f"state dir owned by {state}"
        # Created by selfcheck/daemon opening and migrating SQLite; its presence
        # proves store.Open + Migrate ran, not merely that a directory exists.
        machine.wait_until_succeeds("test -f /var/lib/tidal-syncer/tidal-syncer.db")

        machine.succeed("test -d /srv/music")
        machine.succeed("runuser -u tidal-syncer -- touch /srv/music/.write-probe")
        machine.succeed("rm /srv/music/.write-probe")

    with subtest("login unit is declared, idle, and separately scoped"):
        assert (
            machine.succeed("systemctl show -p ActiveState --value tidal-syncer-login").strip()
            == "inactive"
        ), "the login unit must never be pulled in automatically: it blocks on a human"

        login_unit = machine.succeed("systemctl cat tidal-syncer-login.service")
        exec_start = [l for l in login_unit.splitlines() if l.startswith("ExecStart=")]
        assert exec_start and exec_start[0].endswith(" login"), f"unexpected ExecStart: {exec_start}"
        # A SEPARATE RuntimeDirectory from the daemon's is load-bearing: the
        # daemon re-reads its config every cycle, and a shared RuntimeDirectory
        # would be torn down when the short-lived login unit stops, yanking the
        # config out from under a running daemon.
        assert LOGIN_CFG in login_unit, f"login unit does not use {LOGIN_CFG}:\n{login_unit}"
        assert DAEMON_CFG not in login_unit, f"login unit shares the daemon's runtime config:\n{login_unit}"

        # --no-block because the unit polls TIDAL until a human approves; the
        # tarpit keeps it parked in `activating` so its RuntimeDirectory stays.
        machine.succeed("systemctl start --no-block tidal-syncer-login.service")
        machine.wait_until_succeeds(f"test -f {LOGIN_CFG}")
        machine.succeed(f"grep -q -- '{SENTINEL}' {LOGIN_CFG}")
        assert machine.succeed(f"stat -c '%a %U' {LOGIN_CFG}").strip() == "600 tidal-syncer"

        machine.succeed("systemctl stop tidal-syncer-login.service")
        # Its directory goes away with it -- and the daemon's does not. This is
        # the exact regression the split guards against.
        machine.wait_until_fails(f"test -e {LOGIN_CFG}")
        machine.succeed(f"test -f {DAEMON_CFG}")
        machine.require_unit_state("tidal-syncer.service", "active")

    with subtest("operator login wrapper is on PATH"):
        machine.succeed("command -v tidal-syncer-login")
        machine.succeed("command -v tidal-syncer")

    with subtest("prometheus scrapes the derived target"):
        machine.wait_for_unit("prometheus.service")
        machine.wait_for_open_port(9090)
        # Proves the scrape target the module derived from
        # metrics.{listenAddress,port} matches the address it wrote into
        # config.yaml -- a mismatch is invisible until someone opens Grafana.
        machine.wait_until_succeeds(
            "curl -sf http://127.0.0.1:9090/api/v1/targets"
            " | jq -e '.data.activeTargets[]"
            ' | select(.labels.job == "tidal-syncer")'
            " | select(.health == \"up\")'",
            timeout = 180,
        )

    with subtest("grafana dashboard provider is provisioned"):
        machine.wait_for_unit("grafana.service")
        # Resolve the provisioning directory from grafana's own config rather
        # than hardcoding a store path, and stay off the HTTP API so the test
        # does not depend on Grafana's auth.
        grafana_unit = machine.succeed("systemctl cat grafana.service")
        ini = re.search(r"-config (/nix/store/\S+config\.ini)", grafana_unit)
        assert ini, f"could not find grafana's config.ini in:\n{grafana_unit}"
        prov = re.search(
            r"^\s*provisioning\s*=\s*(\S+)", machine.succeed(f"cat {ini.group(1)}"), re.M
        )
        assert prov, "grafana config.ini declares no provisioning directory"

        providers = machine.succeed(f"cat {prov.group(1)}/dashboards/dashboard.yaml")
        assert "tidal-syncer" in providers, f"no tidal-syncer provider:\n{providers}"
        path = re.search(r"path:\s*(\S+)", providers)
        assert path, f"provider declares no path:\n{providers}"
        machine.succeed(f"test -f {path.group(1)}/tidal-syncer.json")

    with subtest("restart re-assembles the runtime config"):
        # RuntimeDirectory is destroyed on stop, so a restart has to rebuild
        # config.yaml from the credential all over again. If ExecStartPre were
        # order-dependent or the credential unavailable on restart, the service
        # would come back up reading a stale or missing file.
        machine.succeed("systemctl restart tidal-syncer.service")
        machine.wait_for_unit("tidal-syncer.service")
        machine.wait_for_open_port(9101, addr = "127.0.0.1")
        assert SENTINEL in machine.succeed(f"cat {DAEMON_CFG}")
        assert "tidal_syncer_build_info" in machine.succeed(
            "curl -sf http://127.0.0.1:9101/metrics"
        )
  '';
}
