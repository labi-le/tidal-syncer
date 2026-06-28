# internal/store

Thin single-writer SQLite cache (`modernc.org/sqlite`, pure-Go, no CGO). Persistence only — NO business logic.

## WHERE TO LOOK
| Task | File |
|------|------|
| open / migrate / pragmas | store.go |
| schema (DDL) | migrations/*.sql |
| OAuth token (single row) | tokens.go |
| per-track download state | tracks.go |
| favorites snapshot + diff | snapshot.go |

## SCHEMA (migrations/*.sql)
- `tokens` — PK `id` CHECK(id=1): exactly ONE row. Cols: access/refresh/expires_at/user_id/country_code/session_id.
- `tracks` — PK `tidal_id`; cols include `obtained_quality` + `requested_quality` (tier requested at download time — drives the no-re-download skip in `internal/sync`); `status` CHECK in ('pending','done','failed'); index `idx_tracks_status`. **0002** adds `requested_quality`, backfilled from `obtained_quality` for done rows.
- `favorites_snapshot` — PK (kind, tidal_id); diffs added/removed between runs.
- Migration runner: embedded FS, records applied versions in `schema_migrations`, parses leading numeric filename prefix.

## KEY API
- `Open(dataDir) (*Store, error)`, `Close()`, `Migrate(ctx)` — idempotent; doubles as a cheap reachability ping for `health`.
- Tokens: `UpsertToken`, `GetToken`.
- Tracks: `MarkTrack`, `GetTrack`, `Status`/`StatusDone`/`StatusFailed`.
- Snapshot: `ReplaceSnapshot(ctx, kind, items)` — atomic DELETE+INSERT tx; `DiffSnapshot(ctx, kind, items) (added, removed, err)`.
- Sentinels: `ErrNotFound`, `ErrBadMigrationName`.

## INVARIANTS (store-specific)
- `busy_timeout` MUST be the FIRST pragma — `modernc` silently ignores it otherwise.
- Pragmas: WAL + `foreign_keys=ON` + `busy_timeout=5000`.
- `SetMaxOpenConns(1)` = single writer. Don't raise it.
- DB file forced to `0o600` via idempotent `tightenDBFilePerms`, re-applied AFTER migrations.
- Keep this package free of business logic — ranking/policy/orchestration live in `internal/sync`.
- `SELECT *` forbidden (unqueryvet) — enumerate columns.
