# internal/sync — sync orchestrator (~1295 LOC, largest package)

Root `AGENTS.md` owns global conventions/commands/anti-patterns. This file is `internal/sync/` ONLY.

## OVERVIEW
Hexagonal orchestrator: `Engine` depends ONLY on the narrow ports in `ports.go` (TidalClient, Downloader, CoverFetcher), all injected — it never imports the concrete wire client.

## WHERE TO LOOK
| Task | File |
|------|------|
| orchestration / concurrency | engine.go (`NewEngine`, `SyncOnce`) |
| which tracks get synced (scope) | enumerate.go |
| per-track pipeline | process.go |
| quality ranking/skip + meta projection | meta.go |
| album/cover memoization | cache.go |
| run counters | summary.go |
| deletion policy | removal.go |
| .m3u8 export | playlist.go |
| HTTP cover fetcher + downloader factory | wiring.go |
| port interfaces | ports.go |

## KEY FLOW
- **engine.go**: `NewEngine(Params{Client,Downloader,Covers,Store,Config,Logger,Limiter})`; `SyncOnce(ctx)` → validate token via `client.UserID` → `enumerate` → `errgroup` with `SetLimit(max(1, cfg.Concurrency))`, wait on `*rate.Limiter` per track → returns `Summary` + snapshot items (caller persists).
- **enumerate.go**: scope-driven (`cfg.Scope.All` unions favorites+albums+playlists; else honors the 3 toggles) → dedup `map[int]tidal.Track` → sorted by id (determinism).
- **process.go**: `processTrack` → `shouldSkip` (skip iff stored StatusDone AND `qualityRank(requested) >= qualityRank(cfg.Quality.Request)` — compares the tier REQUESTED at download time, not the obtained tier, so a sub-request master downloads once then skips instead of re-downloading every cycle) → `downloadOne`: resolveAlbum (cached) + best-effort cover/lyrics → `namer.Render` → MkdirAll → `Downloader.Download` → `tag.IntegrityCheck` → `tag.TagFile` (+`WriteLRC`) → `store.MarkTrack(StatusDone)`. `markFailed` logs + records StatusFailed, NEVER propagates. Debug logs: "downloading"/"downloaded"/"skipped: already present".
- **meta.go**: `qualityRank` HI_RES_LOSSLESS>LOSSLESS>HIGH>LOW>unknown; `buildTrackMeta`→namer.TrackMeta, `buildTagMeta`→tag.Meta; `primaryArtist` = artist with `Type=="MAIN"`; const `fileExtension="flac"`.
- **removal.go**: `Reconcile` diffs the `tracks` snapshot → keep | mirror (delete file+.lrc+prune empty dirs) | trash (rename under `<music>/.trash`). `withinMusic` sandboxes every path. First run (empty snapshot) → nothing removed.

## INVARIANTS (sync-specific)
- Extend `ports.go` to add a TIDAL capability — NEVER import the concrete `*tidal.Client` into the engine.
- `counters` (summary.go) holds atomic fields — pass by pointer, NEVER copy.
- cache.go: release the cache mutex BEFORE running the album loader (so independent albums resolve in parallel) — never hold it across a download/fetch.
- A single track failure must never abort the run.
- playlist.go reuses the SAME `namer.Render`/`buildTrackMeta` projection as the engine so .m3u8 paths match files on disk; writer is single-run, not concurrency-safe.
- First sync run can never delete (empty stored snapshot ⇒ empty `removed`).
- `internal/lock` is held by the cmd layer around the whole cycle — the engine itself is lock-agnostic.
