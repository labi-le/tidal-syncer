# pkg/tidal/ — TIDAL API client

Reusable, config-agnostic TIDAL HTTP client: rate-limited, retrying, secret-redacting. Credentials/store/http all injected. This package NEVER logs — typed errors only.

Root `AGENTS.md` has global conventions/commands. Sibling subpkgs `auth/` and `download/` have their own AGENTS.md — see those, not here.

## WHERE TO LOOK
| Task | File |
|------|------|
| Client / Do / construction | client.go |
| functional options | options.go |
| response DTOs | dtos.go |
| Track + PlaybackInfo | tracks.go |
| Album + AlbumCredits | albums.go |
| Lyrics | lyrics.go |
| lazy favorites/album/playlist iterators | favorites.go + items.go |
| cover URL builder | cover.go |
| retry/backoff | backoff.go |
| secret redaction | redact.go |
| API error type | errors.go |
| manifest parsing | manifest/ |

## KEY API
- `Client` / `New(tokens TokenSource, opts...)`.
- `Do(ctx, method, path, query) (*http.Response, error)`; `UserID(ctx)`.
- Endpoint methods: `Track`, `PlaybackInfo`, `Album`, `AlbumCredits`, `Lyrics`.
- Favorites are LAZY `iter.Seq2[T, error]` (page size 100): `FavoriteTracks/Albums/Artists/Playlists`, `AlbumTracks`, `PlaylistTracks`.
- `TokenSource` (`Token(ctx) (access, countryCode, userID, err)`) — the seam to `auth/`; called before EVERY request; impls MUST be concurrency-safe.
- `Backoff` (exported, Retry-After-aware, clamped to waitMax) — reused by `auth/` to dodge an import cycle.
- `Redact` masks `Bearer <jwt>` + signed query params (sig/x-amz-signature/token/policy…); one-way.
- `APIError{Status, Code}`; `ErrTrackUnavailable` (401/404 on track endpoints → caller skips). `errorBodyLimit` 16 KiB.
- Defaults: 60 rpm, 4 retries.
- Options: `WithBaseURL`, `WithRequestsPerMinute`, `WithRetryMax`, `WithRetryWaitMin`, `WithRetryWaitMax`.

## manifest/ (sub-note)
Parse-don't-validate. `Parse(mime, b64) (Manifest, error)`.
MIME = `application/vnd.tidal.bts` (JSON, direct URLs) OR `application/dash+xml` (MPD XML, `$Number$` segments).
`encryptionType != "NONE"` → `ErrEncrypted` at parse time (no DRM).
Errors: `ErrInvalidManifest`, `UnknownMimeTypeError`.
Discriminator: `Manifest.Kind()` + `BTS()/DASH()` returning `(value, ok)`; zero value is invalid.

## INVARIANTS (pkg/tidal-specific)
- NEVER log here (silent reusable lib) — return typed errors; callers use `Redact` to log safely.
- NEVER materialize the whole library — use the lazy `iter.Seq2` iterators.
- On non-nil error `Do` returns a nil response; on nil error the CALLER owns and MUST close `resp.Body`.
- Reuse `tidal.Backoff` for any new retrying caller — don't fork backoff logic.
