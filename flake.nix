{
  description = "tidal-syncer monitoring: Prometheus scrape + Grafana dashboard NixOS module";

  # No inputs: the monitoring module is a plain NixOS module (Prometheus scrape +
  # Grafana dashboard provisioning) evaluated in the consumer's nixpkgs, so it
  # pulls in nothing of its own.
  outputs =
    { self }:
    {
      # Scrape the daemon's loopback /metrics and provision its Grafana dashboard
      # on a host already running Prometheus + Grafana. Imported by the NixOS repo
      # as inputs.tidal-syncer.nixosModules.monitoring.
      nixosModules.monitoring = import ./deploy/monitoring.nix;
      nixosModules.default = import ./deploy/monitoring.nix;
    };
}
