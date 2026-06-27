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
at that path and startup fails. The distroless image ships with **empty** default
credentials, so a credential-less `login` returns HTTP 400 — you must supply your
own `tidal_auth.client_id` / `tidal_auth.client_secret` in `config.yaml`.

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
