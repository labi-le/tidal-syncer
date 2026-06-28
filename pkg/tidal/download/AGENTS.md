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
- `Downloader` / `New(provider PlaybackProvider, httpClient, opts...)`; option `WithFFmpeg(path)`.
- `Download(ctx, trackID, destPath) (grantedQuality string, error)`.
- `PlaybackProvider` interface: `PlaybackInfo(ctx, trackID, quality) (mime, b64, granted, error)` — 4-return. Contract WARNS callers to enforce their own floor on `granted`: TIDAL answers HTTP 200 with a lossy stream when the account lacks HiFi.
- `SweepStale(dir) (removed int, error)` — startup janitor; missing dir is NOT an error.
- Sentinels: `ErrBelowFloor`, `ErrDiskFull`, `ErrEncryptedSkip` (wraps `manifest.ErrEncrypted`), `ErrUnsupportedManifest`, `ErrUnexpectedStatus`, `ErrFFmpeg`.

## FLOW
`fetchManifest` tries `HI_RES_LOSSLESS` then `LOSSLESS`. `meetsLosslessFloor` admits ONLY those two; any sub-floor grant (HIGH/AAC, LOW, unknown) → `ErrBelowFloor` and NOTHING is written. `manifest.Parse` → `producerFor`:
- KindBTS → `streamURLs` (direct GET, expect 200).
- KindDASH → `demuxDASH`.

`writeAtomic`: open `<dest>.part` (mode 0o644) → produce → `Sync` → `os.Rename`. On any error/cancel the part file is removed, dest untouched.

## INVARIANTS (download-specific)
- NEVER write lossy audio into a `.flac` — enforce the floor on the GRANTED tier before writing (`ErrBelowFloor`, nothing on disk).
- NEVER write directly to the destination — always `<dest>.part` → fsync → rename (atomic; cross-UID-readable 0o644).
- NEVER log here (silent lib). NEVER re-encode: DASH demux is `ffmpeg -map 0:a:0 -c:a copy -f flac pipe:1` (stream COPY). DASH needs `WithFFmpeg` else `ErrFFmpeg`. Scratch temp file is ALWAYS removed. Per-segment up to 3 retries; bytes appended only after a full read so a retry can't corrupt concatenation.
- The `//nolint:gosec` (G204) on the ffmpeg exec is justified: ffmpeg path is caller-injected, args are fixed literals + a self-created temp path (never user input).
- ENOSPC → `errors.Join(ErrDiskFull, …)` so callers distinguish "no space" from generic I/O.
