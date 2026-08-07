{
  description = "tidal-syncer: Nix package + NixOS service module (Prometheus scrape + Grafana dashboard optional)";

  # No inputs, deliberately. The package is exposed as an OVERLAY rather than a
  # `packages.<system>` output, so it is a function of the CONSUMER's nixpkgs:
  # no second nixpkgs in their flake.lock, no `follows` boilerplate, and the
  # binary is built against the very nixpkgs that evaluates the NixOS config.
  # The trade-off is that `nix build .#` does not work inside this repo; use the
  # existing shell.nix for development.
  outputs =
    { self }:
    let
      # Reproducible build metadata for internal/buildinfo, taken from the flake
      # itself instead of `git describe`/`date`: the repo carries no vX.Y.Z tags,
      # and a wall-clock BuildTime would break reproducibility.
      versionDate = builtins.substring 0 8 (self.lastModifiedDate or "19700101");
      version = "0-unstable-${builtins.substring 0 4 versionDate}-${
        builtins.substring 4 2 versionDate
      }-${builtins.substring 6 2 versionDate}";
    in
    {
      overlays.default = final: _prev: {
        tidal-syncer = final.callPackage ./deploy/package.nix {
          inherit version;
          commitHash = self.shortRev or self.dirtyShortRev or "unknown";
          buildTime = self.lastModifiedDate or "unknown";
        };
      };

      # The daemon as a native systemd service: a full typed mirror of
      # config.yaml, the client secret injected via LoadCredential (never in the
      # store), and an opt-in `dashboard.enable` that folds in the Prometheus
      # scrape job and the Grafana dashboard provider.
      #
      # Requires `pkgs.tidal-syncer`, i.e. the consumer must apply
      # `inputs.tidal-syncer.overlays.default`.
      nixosModules.default = import ./deploy/service.nix;
      nixosModules.tidal-syncer = import ./deploy/service.nix;

      # Monitoring-only module for a host that runs the daemon some OTHER way
      # (today: docker-compose publishing 127.0.0.1:9101). Kept because it is
      # live on the server; it hardcodes the scrape target, which the service
      # module derives instead. Do not import both: two identical job_names fail
      # `promtool check config` at build time.
      nixosModules.monitoring = import ./deploy/monitoring.nix;
    };
}
