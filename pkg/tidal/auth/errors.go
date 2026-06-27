package auth

import "errors"

// ErrDeadCredentials indicates the configured TIDAL OAuth client credentials
// were rejected by the authorization server (an HTTP 401 response or an
// invalid_client error). Retrying with the same credentials cannot succeed; the
// caller must supply their own client_id and client_secret.
var ErrDeadCredentials = errors.New("auth: dead client credentials: supply your own client_id and client_secret")

// ErrDeviceCodeExpired indicates the device authorization code expired before
// the user completed the login in their browser.
var ErrDeviceCodeExpired = errors.New("auth: device code expired before authorization")

// errAuthorizationPending is the internal signal that the user has not yet
// approved the device login, so polling must continue.
var errAuthorizationPending = errors.New("auth: authorization pending")

// errSlowDown is the internal signal that polling is too frequent and the
// interval must be increased before the next attempt.
var errSlowDown = errors.New("auth: slow down polling")

// errUnexpectedStatus is returned for an unclassified non-success response from
// an authorization endpoint.
var errUnexpectedStatus = errors.New("auth: unexpected authorization response")
