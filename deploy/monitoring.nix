# NixOS module: scrape the tidal-syncer daemon's loopback /metrics endpoint and
# provision its Grafana dashboard. It is co-located with the app so the dashboard
# stays in lockstep with the metric names it renders; the NixOS repo only pulls
# this in as a flake input.
#
# Consume from a host that runs Prometheus + Grafana (both on loopback):
#   inputs.tidal-syncer.url = "git+ssh://git@github.com/labi-le/tidal-syncer";
#   imports = [ inputs.tidal-syncer.nixosModules.monitoring ];
#
# Assumes docker-compose publishes the container's metrics port on
# 127.0.0.1:9101 (loopback-only; see ../docker-compose.yml) and that config.yaml
# has metrics.enabled = true with address ":9101". The dashboard selects the
# Prometheus datasource via a template variable, so it needs no fixed datasource
# uid and merges with any Prometheus provisioned on the host.
{ ... }:
{
  services.prometheus.scrapeConfigs = [
    {
      job_name = "tidal-syncer";
      static_configs = [ { targets = [ "127.0.0.1:9101" ]; } ];
    }
  ];

  services.grafana.provision.dashboards.settings = {
    apiVersion = 1;
    providers = [
      {
        name = "tidal-syncer";
        type = "file";
        disableDeletion = true;
        options = {
          path = ./grafana;
          foldersFromFilesStructure = false;
        };
      }
    ];
  };
}
