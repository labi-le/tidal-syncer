# pkg/tidal/download

Atomic, lossless-floor-enforced track downloader: resolves a manifest via an injected `PlaybackProvider`, streams BTS or ffmpeg-demuxes DASH, commits each file atomically. NEVER logs (typed errors only). See root `AGENTS.md` for global conventions/commands.

## WHERE TO LOOK
| Task | File |
|------|------|
| Download/floor logic/atomic write | download.go |
| DASH segment assembly + ffmpeg demux | dash.go |
| typed errors | errors.go |
| startup orphan-`.part` cleanup | sweep.go |

## KEY API
- `Downloader` / `New(provider PlaybackProvider, httpClient, opts...)`; options `WithFFmpeg(path)`, `WithQuality(request)` (ceiling tier, default HI_RES_LOSSLESS), `WithFloor(floor)` (minimum tier, default LOSSLESS).
- `Download(ctx, trackID, destPath) (grantedQuality string, error)`.
- `PlaybackProvider` interface: `PlaybackInfo(ctx, trackID, quality) (mime, b64, granted, error)` — 4-return. Contract WARNS callers to enforce their own floor on `granted`: TIDAL answers HTTP 200 with a lossy stream when the account lacks HiFi.
- `SweepStale(dir) (removed int, error)` — startup janitor; missing dir is NOT an error.
- Sentinels: `ErrBelowFloor`, `ErrDiskFull`, `ErrEncryptedSkip` (wraps `manifest.ErrEncrypted`), `ErrUnsupportedManifest`, `ErrUnexpectedStatus`, `ErrFFmpeg`.

## FLOW
`fetchManifest` tries the configured band highest-first — every tier from `WithQuality` (request) down to `WithFloor` (floor), ranked by `Quality.Rank()`. A granted tier below the floor (HIGH/AAC, LOW, unknown, or empty) → `ErrBelowFloor` and NOTHING is written. `manifest.Parse` → `producerFor`:
- KindBTS → `streamURLs` (direct GET, expect 200; up to `streamAttempts` retries on a transient transport/non-200 error before the body is copied once).
- KindDASH → `demuxDASH`.

`writeAtomic`: create a UNIQUE `os.CreateTemp` part file (a `.<base>.<rand>.part` sibling on dest's volume, chmod 0o644, basename bounded to NAME_MAX — so concurrent same-dest writers never share a part file) → produce → `Sync` → `os.Rename` → fsync the dest dir (`syncDir`, crash-durable). On any error/cancel the part file is removed, dest untouched.

## INVARIANTS (download-specific)
- NEVER write lossy audio into a `.flac` — enforce the floor on the GRANTED tier before writing (`ErrBelowFloor`, nothing on disk).
- NEVER write directly to the destination — always a unique `.part` sibling → fsync → rename → dest-dir fsync (atomic; cross-UID-readable 0o644).
- NEVER log here (silent lib). NEVER re-encode: DASH demux is `ffmpeg -map 0:a:0 -c:a copy -f flac pipe:1` (stream COPY). DASH needs `WithFFmpeg` else `ErrFFmpeg`. Scratch temp (on the dest volume, `.part`-suffixed so `SweepStale` reaps a hard-crash orphan) is ALWAYS removed on normal paths. Per-segment up to 3 retries; bytes appended only after a full read so a retry can't corrupt concatenation.
- The `//nolint:gosec` (G204) on the ffmpeg exec is justified: ffmpeg path is caller-injected, args are fixed literals + a self-created temp path (never user input).
- ENOSPC → `errors.Join(ErrDiskFull, …)` so callers distinguish "no space" from generic I/O.
