package auth

import "errors"

// ErrDeadCredentials indicates the configured TIDAL OAuth client credentials
// were rejected by the authorization server with an invalid_client error
// (returned on either a 400 or a 401). Retrying with the same credentials
// cannot succeed; the caller must supply their own client_id and client_secret.
var ErrDeadCredentials = errors.New("auth: dead client credentials: supply your own client_id and client_secret")

// ErrDeviceCodeExpired indicates the device authorization code expired before
// the user completed the login in their browser.
var ErrDeviceCodeExpired = errors.New("auth: device code expired before authorization")

// ErrReauthRequired indicates the stored refresh token was revoked or is no
// longer accepted by the authorization server (an invalid_grant error). The
// client credentials remain valid, but interactive re-authentication is
// required to obtain a new refresh token.
var ErrReauthRequired = errors.New("auth: refresh token revoked: interactive re-authentication required")

// errAuthorizationPending is the internal signal that the user has not yet
// approved the device login, so polling must continue.
var errAuthorizationPending = errors.New("auth: authorization pending")

// errSlowDown is the internal signal that polling is too frequent and the
// interval must be increased before the next attempt.
var errSlowDown = errors.New("auth: slow down polling")

// errUnexpectedStatus is returned for an unclassified non-success response from
// an authorization endpoint.
var errUnexpectedStatus = errors.New("auth: unexpected authorization response")

// errTransient is returned for a token-endpoint failure with no recognizable
// OAuth error body, such as a bodiless 401 from a WAF or CDN, a 5xx response,
// or a proxy hiccup. The operation may succeed if retried.
var errTransient = errors.New("auth: transient token-endpoint failure")
