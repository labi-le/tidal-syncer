# PROJECT KNOWLEDGE BASE

**Generated:** 2026-06-28 · **Commit:** 5eceea4 · **Branch:** main

## OVERVIEW
- `tidal-syncer` (module `github.com/labi-le/tidal-syncer`): a single-binary Go CLI that syncs a TIDAL library to local **lossless FLAC** (one-shot `sync` or long-running `daemon`).
- Go **1.26.3**. Stack: cobra (CLI), zerolog (logging), goccy/go-yaml (config), **modernc.org/sqlite** (pure-Go, no CGO), go.senan.xyz/taglib (tags), mewkiz/flac (integrity), hashicorp/go-retryablehttp, golang.org/x/{sync,time,text,sys}. ffmpeg is a runtime dep (DASH demux only).

## STRUCTURE
```
cmd/                  package main: flat — every cobra subcommand is one file (login/sync/daemon/health+selfcheck/version), no per-cmd subdirs
internal/             app-private packages
  version.go          ⚠ stray file, package `internal`, ldflag vars (Version/CommitHash/BuildTime)
  sync/               ★ largest (~1295 LOC) — sync orchestrator (engine, enumerate, process, removal, playlist, cache, meta, ports)
  store/              SQLite cache (tokens/tracks/favorites_snapshot) + migrations/*.sql
  tag/  namer/  config/  ctxlog/  lock/  authstore/   (small focused pkgs)
pkg/tidal/            ★ reusable, config-agnostic TIDAL client lib (credentials/store/http/ffmpeg all injected)
  auth/  download/  manifest/
build/package/        ⚠ holds a committed 14.7MB tidal-syncer BINARY (non-standard)
.github/workflows/    CI: automerge-dependabot, releaser (goreleaser), stale, update-flake — ⚠ NO go test/lint workflow
docs/MANUAL_SMOKE.md  only doc file; manual smoke playbook
```

## WHERE TO LOOK
| Task | Location | Notes |
|------|----------|-------|
| CLI wiring/flags/logger | cmd/main.go | newRootCmd, PersistentPreRun |
| add/modify a command | cmd/<name>.go | one file per subcommand |
| sync cycle logic | internal/sync/engine.go (SyncOnce) + process.go | enumerate → process → snapshot |
| quality/floor | pkg/tidal/download/download.go (fetchManifest, ErrBelowFloor) + internal/sync/meta.go (qualityRank) | lossless floor enforced |
| OAuth/login/refresh | pkg/tidal/auth/ | device flow + refresh |
| SQLite schema | internal/store/migrations/0001_init.sql | |
| path templating | internal/namer/ | hand-rolled, no text/template |
| FLAC tagging/integrity | internal/tag/ | taglib + mewkiz/flac |
| config schema/validation | internal/config/ | |

## CODE MAP
Note: centrality unmeasured — gopls & codegraph unavailable in this workspace.

| Symbol | Type | Location | Role |
|--------|------|----------|------|
| `newRootCmd` | func | cmd/main.go | assembles cobra tree, builds logger |
| `Engine.SyncOnce` | method | internal/sync/engine.go | orchestrates one sync run (enumerate → concurrent process → snapshot) |
| `Downloader.Download` | method | pkg/tidal/download/download.go | manifest→BTS/DASH→atomic .part write, lossless-floor enforced |
| `auth.Client` (StartDeviceAuth/PollToken/Refresh) | type | pkg/tidal/auth | RFC 8628 device flow + single-flight refresh |
| `tidal.Client` | type | pkg/tidal/client.go | rate-limited, retrying, redacting HTTP client |
| `store.Store` | type | internal/store | single-writer SQLite (tokens/tracks/snapshot) |

## CONVENTIONS
Deviations from vanilla Go ONLY (from .golangci.yml v2.7.2 strict):
- **No global logger** (sloglint no-global:all). Pull op-scoped logger via `internal/ctxlog.Op`; loggers passed by value (pointer indirection in cmd).
- **No stdlib `log`** outside `cmd/main.go` (depguard) → use zerolog/`log/slog`.
- Log LEVEL comes from `config.log.level` (the `--log-level` flag was REMOVED); `--verbose` forces Trace+caller. `version` must work without a config file.
- CGO_ENABLED=0 everywhere except `make test-race` (CGO=1).
- Errors: wrap external errors `%w` (wrapcheck); sentinels `ErrX`, error types `XError` (errname); accept interfaces / return concrete (ireturn).
- No naked returns; funlen ≤100 lines/50 stmts; cyclop ≤30; exhaustive **switch AND map**; `math/rand/v2`; goimports local-prefix `github.com/labi-le/tidal-syncer`; golines max 120; `SELECT *` forbidden (unqueryvet); every `//nolint` needs specific linter + explanation.
- White-box tests use `*_internal_test.go` suffix (testpackage skip); tests use `t.TempDir()` (usetesting).

## ANTI-PATTERNS (THIS PROJECT)
- NEVER write lossy audio to `.flac` — LOSSLESS floor; sub-floor grant → `ErrBelowFloor`, nothing written (pkg/tidal/download/download.go).
- NEVER write directly to the destination — always `<dest>.part` → fsync → rename.
- `pkg/tidal/{download,auth}` and `internal/namer` NEVER log (silent libraries, typed errors only).
- `pkg/tidal/auth` MUST NOT import `internal/store` — bridge via `internal/authstore`.
- `internal/sync` engine depends only on `ports.go` — don't import the concrete wire client; extend ports instead.
- Use the lazy `iter.Seq2` iterators (favorites.go/items.go) — never materialize the whole library.
- Don't copy `internal/sync.counters` (atomic fields) — pass by pointer.
- SQLite `busy_timeout` MUST be the first pragma (modernc ignores it otherwise).
- Don't retry 4xx on the auth token endpoint (OAuth soft-errors are 400s — classify, don't retry).
- A single track failure must NEVER abort the run; a daemon per-cycle error is NEVER fatal; the daemon NEVER self-reauths (operator runs `tidal-syncer login`).
- Don't use `text/template` for filenames (deliberately hand-rolled in namer).

## COMMANDS

**MANDATORY WORKFLOW:** ALL Go tooling (`go`, `golangci-lint`, `goreleaser`) MUST run inside `nix-shell` — the pinned toolchain (Go 1.26 + linters + ffmpeg) lives only there; NEVER invoke bare `go`/`golangci-lint`. The application itself is built and run ONLY through **Docker** (`make docker-build`, `docker compose ...`); **`docker` runs on the HOST, NOT wrapped in `nix-shell`.**

```bash
# Go tooling ONLY via nix-shell; docker runs on the HOST (not nix-wrapped)
nix-shell --run 'go build ./cmd/... ./internal/... ./pkg/...'   # NOT ./...  (Music/ perms break the walker)
nix-shell --run 'go test ./cmd/... ./internal/... ./pkg/...'
nix-shell --run 'golangci-lint run ./cmd/... ./internal/... ./pkg/...'
make build | run | tests | test-race | lint | docker-build | docker-run | compose-up | logs
docker compose run --rm tidal-syncer <login|sync|daemon|health>   # one-off
```

## NOTES (gotchas)
- `go ./...` BREAKS: `Music/` is owned by container UID 65532 (0750) → use the explicit `./cmd/... ./internal/... ./pkg/...` roots.
- `config.yaml` is gitignored and MUST pre-exist as a real file before `docker compose` (else the bind-mount auto-creates a directory and startup fails). Contains TIDAL client_id/secret; token persists in `data/` (also gitignored).
- `Music/` and `data/` must be writable by the nonroot container (UID 65532) → `chmod 777` (host user can't chown to another uid).
- Image is **distroless** (no shell); `ffmpeg` at `/usr/local/bin/ffmpeg` but there is **no ffprobe** — inspect files with `ffmpeg -i` or a throwaway `alpine` container.
- `docker compose run` is a one-off container NOT shown by `docker compose logs` — follow it with `docker logs -f <…-run-…>`; `make logs`/`make compose-up` are for the daemon service.
- TIDAL account must be HiFi for `LOSSLESS`; **24-bit HI_RES_LOSSLESS needs a PKCE mobile-client auth flow that is NOT implemented** (device-flow tops out at 16-bit). Working device client: `cgiF7TQuB97BUIu3`.
- `internal/version.go` makes `internal` itself an importable package; version injected via `-ldflags -X github.com/labi-le/tidal-syncer/internal.{Version,CommitHash,BuildTime}`.
