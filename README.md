# tidal-syncer

Sync your TIDAL library to local storage.

## Quickstart (Docker)

Create your config file from the example and fill in your TIDAL credentials
**before** the first run:

```sh
cp config.example.yaml config.yaml
# edit config.yaml: set tidal_auth.client_id and tidal_auth.client_secret
docker compose run --rm tidal-syncer login
```

The `./config.yaml:/app/config.yaml` bind mount requires `config.yaml` to already
exist as a real file. If it is missing, Docker auto-creates an empty **directory**
at that path and startup fails. The image ships with **no** built-in credentials:
loading the config fails fast unless `tidal_auth.client_id` and
`tidal_auth.client_secret` are set in `config.yaml`.

## Dependencies

- Go 1.26+
- ffmpeg (audio processing)

## Install

```sh
make build
```

## Usage

```
Usage:
  tidal-syncer [command]

Available Commands:
  daemon      Run tidal-syncer as a background daemon
  health      Check health of the tidal-syncer service
  login       Authenticate with TIDAL
  sync        Sync TIDAL library to local storage
  version     Print build version information

Flags:
      --config string   path to config file (default "/app/config.yaml")
      --verbose         verbose mode (Trace level + caller)
```

## Examples

```sh
# Authenticate
tidal-syncer login

# One-off sync
tidal-syncer sync --config ./config.yaml

# Run as daemon
tidal-syncer daemon
```

## Metrics (Prometheus & Grafana)

The daemon can expose a Prometheus `/metrics` endpoint with live library and sync
statistics — track counts by status and quality, genre distribution, favorites,
distinct artists/albums, plus per-cycle counters (cycles, failures by class,
duration, last success) and the standard `go_*` / `process_*` runtime metrics.
Every app metric is namespaced `tidal_syncer_*`.

Enable it in `config.yaml` (off by default):

```yaml
metrics:
  enabled: true
  address: ":9101"
```

`docker-compose.yml` publishes the endpoint on `127.0.0.1:9101` (loopback), so a
Prometheus already running on the host can scrape it:

```yaml
# prometheus.yml
scrape_configs:
  - job_name: tidal-syncer
    static_configs:
      - targets: ["127.0.0.1:9101"]
```

### NixOS

For a host managed with NixOS that already runs Prometheus + Grafana, this repo
is a flake exposing a monitoring module that adds the scrape job and provisions
the Grafana dashboard (`deploy/grafana/tidal-syncer.json`, which picks its
Prometheus datasource via a template variable):

```nix
# flake.nix
inputs.tidal-syncer.url = "github:labi-le/tidal-syncer";

# configuration.nix
imports = [ inputs.tidal-syncer.nixosModules.monitoring ];
```

The dashboard JSON under `deploy/grafana/` can also be imported into any Grafana
instance by hand.

## Querying the library (SQLite)

Sync state lives in `data/tidal-syncer.db`. To list your most recently
favorited tracks with artist, album and genre:

```sql
WITH joined AS (
  SELECT f.name AS title, f.added_at, t.genre,
         substr(t.path, length('/app/Music/') + 1) AS rel
  FROM favorites_snapshot f
  JOIN tracks t ON t.tidal_id = f.tidal_id
  WHERE f.kind = 'tracks' AND f.added_at <> '' AND t.path <> ''
),
split AS (
  SELECT title, added_at, genre,
         substr(rel, 1, instr(rel, '/') - 1) AS artist,
         substr(rel, instr(rel, '/') + 1) AS after_artist
  FROM joined
)
SELECT artist,
       substr(after_artist, 1, instr(after_artist, '/') - 1) AS album,
       title, genre, added_at
FROM split
ORDER BY added_at DESC
LIMIT 300;
```

Run it with:

```sh
nix-shell --run "sqlite3 -header -column data/tidal-syncer.db"
# then paste the query, or pass it inline with -cmd
```

- `artist` and `album` are parsed from `tracks.path`
  (`/app/Music/{albumartist}/{album}/{NN} - {title}.flac`).
- `genre` is a `;`-joined list — filter with e.g. `WHERE genre LIKE '%Metal%'`.
- `added_at` is the favorite-add date; use `ORDER BY t.updated_at DESC` for
  download order instead. The join skips failed tracks (they have no file).
