# cmd/ — CLI COMPOSITION ROOT

## OVERVIEW
Flat `package main` — cobra CLI. Every subcommand is one file at `cmd/` root (no per-command subdirs). This is the composition root that wires every `internal/` + `pkg/tidal` package together.

## WHERE TO LOOK
| Task | File |
|------|------|
| root cmd assembly / flags / logger | main.go (`newRootCmd`, `PersistentPreRun`) |
| add a subcommand | new cmd/<name>.go + register in `newRootCmd` |
| OAuth login flow | login.go (`driveDeviceAuth`) |
| one sync cycle wiring | sync.go (`runSync` → `executeSync`) |
| daemon poll loop | daemon.go (`runDaemon`, `runDaemonCycle`) |
| health + selfcheck + ffmpeg resolution | health.go |

## KEY WIRING
- **main.go**: `newRootCmd` builds the cobra tree. `initLogger(verbose)` builds the bootstrap console logger used ONLY by `version` (which reads no config), injected as a `*zerolog.Logger` pointer. Every config-loading subcommand instead builds its FINAL logger via `buildLogger(out, cfg.Log.Format, cfg.Log.Level, verbose)` AFTER `config.Load`, selecting the writer (console vs JSON per `log.format`) and the level (`--verbose` forces Trace+Caller, else `log.level`); `parseLogLevel` maps the level string. Flags: `--config`, `--verbose`. (The `--log-level` flag was removed.)
- **login.go**: device-authorization grant. Credentials come straight from config (`cfg.TidalAuth.ClientID`/`ClientSecret`), shared with sync; `config.Load` rejects empty creds up front (`tidal_auth.client_id is required`), while present-but-invalid creds → TIDAL 400/401 → `auth.ErrDeadCredentials` with actionable guidance.
- **sync.go**: `runSync` = `config.Load` → `store.Open`+`Migrate` → `lock.FileLock.TryAcquire` → `download.SweepStale` → `executeSync`. `--once` is the only mode (else `errOnceOnly`). `newSyncHTTPClient` tunes dial/TLS/response-header timeouts but sets NO overall client timeout, so streaming downloads aren't truncated. `playbackProvider` adapts `*tidal.Client` to `download.PlaybackProvider` (4-return).
- **daemon.go**: poll loop; `runDaemonCycle` classifies per-cycle errors.
- **health.go**: hosts BOTH `health` and `selfcheck`. `resolveFFmpegPath` = `TIDAL_FFMPEG` env else `/usr/local/bin/ffmpeg`; `checkFFmpeg` execs `ffmpeg -version`.

## INVARIANTS (cmd-specific)
- `version` MUST work WITHOUT a config file — it uses the bootstrap logger and never calls `config.Load`. Don't move config loading into `PersistentPreRun`.
- Daemon NEVER self-reauths: on `auth.ErrReauthRequired` it logs "run 'tidal-syncer login'"; on `auth.ErrDeadCredentials` it logs an operator alert to fix client_id/secret. Re-auth is an out-of-band `tidal-syncer login` (writes the token via `internal/authstore`; the next tick picks it up).
- Per-cycle daemon errors are logged, never fatal (the loop continues except on `context.Canceled`).
- Stdlib `log` IS allowed here (cmd/main.go) but nowhere else (depguard).
