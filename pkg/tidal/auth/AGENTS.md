# pkg/tidal/auth

## OVERVIEW
TIDAL OAuth 2.0 device-authorization grant (RFC 8628) + token refresh. Zero dependency on any app package or logger; `TokenStore` and client credentials are injected.

## WHERE TO LOOK
| Task | File |
|------|------|
| client + retry policy (`checkRetry`) | auth.go |
| device-flow start + poll | device.go |
| token POST + OAuth error classification | token.go |
| single-flight refresh | refresh.go |
| `tidal.TokenSource` adapter | tokensource.go |
| sentinels | errors.go |
| functional options | options.go |

## KEY API
- `Client` / `New(clientID, clientSecret, store TokenStore, opts...)`.
- `StartDeviceAuth(ctx) (DeviceAuth, error)` → `{VerificationURIComplete, DeviceCode, Interval, Expiry}`.
- `PollToken(ctx, deviceCode, interval, expiry) error` — saves token on success.
- `Refresh(ctx) (Token, error)` — single-flighted.
- `TokenSource` / `NewTokenSource(c)` — implements `tidal.TokenSource` (loads from store, refreshes if needed).
- `TokenStore` interface (`Load`/`Save`) — persistence seam; satisfied by `internal/authstore`.

### Sentinels (errors.go)
- `ErrDeadCredentials` — OAuth `invalid_client` (400 or 401); supply your own client_id/secret.
- `ErrReauthRequired` — `invalid_grant`, refresh token revoked; interactive re-auth needed.
- `ErrDeviceCodeExpired` — device code expired before user approval.

## INVARIANTS (auth-specific)
- NEVER import `internal/store` (or any app package) and NEVER log. This is a reusable silent library; `internal/authstore` bridges to the SQLite store.
- NEVER retry 4xx on the token endpoint. OAuth soft-errors arrive as 400s, classified by the JSON `error` field (RFC 6749 §5.2), NOT by HTTP status. `checkRetry` retries only 5xx / 429 / transport errors.
- The single `//nolint:nilerr` in auth.go is REQUIRED: a bodiless transient (`errTransient`) must stay retryable.
- Refresh is single-flight via `refreshGroup.Do` — N concurrent callers → exactly ONE upstream round-trip. `needsRefresh` triggers within 1h of expiry (`refreshThreshold`).
- `mergeRefreshed` preserves refresh_token / user_id / country / sessionId when the response omits them.
- `PollToken` honors `authorization_pending` (keep interval) and `slow_down` (grow by `slowDownIncrement`, default 5s).
- `scope = "r_usr+w_usr+w_sub"` is a literal `+`-joined string (NOT URL-encoded). HTTP Basic on `/token`; form `client_id` on `/device_authorization`. Reuses `tidal.Backoff` + `tidal.DefaultRetry*`.
