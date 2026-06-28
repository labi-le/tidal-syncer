// Package auth implements the TIDAL OAuth 2.0 device authorization grant and
// token refresh, with no dependency on any application package or logger. It
// returns data and typed errors and leaves all logging and credential storage
// to the caller: client credentials are injected into [New] and tokens are
// persisted through the caller-supplied [TokenStore].
package auth

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"golang.org/x/sync/singleflight"

	"github.com/labi-le/tidal-syncer/pkg/tidal"
)

const (
	// DefaultBaseURL is the production TIDAL OAuth 2.0 base URL, beneath which
	// the device authorization and token endpoints are resolved.
	DefaultBaseURL = "https://auth.tidal.com/v1/oauth2"

	// scope is the OAuth scope requested for library read/write access. TIDAL
	// expects the literal "+"-separated form.
	scope = "r_usr+w_usr+w_sub"

	deviceCodeGrant   = "urn:ietf:params:oauth:grant-type:device_code"
	refreshTokenGrant = "refresh_token"

	devicePath = "/device_authorization"
	tokenPath  = "/token"

	contentTypeHeader = "Content-Type"
	acceptHeader      = "Accept"
	authHeader        = "Authorization"
	formContentType   = "application/x-www-form-urlencoded"
	jsonMediaType     = "application/json"

	paramClientID     = "client_id"
	paramScope        = "scope"
	paramGrantType    = "grant_type"
	paramDeviceCode   = "device_code"
	paramRefreshToken = "refresh_token"

	refreshGroupKey = "refresh"
)

const (
	defaultHTTPTimeout            = 30 * time.Second
	defaultSlowDownIncrease       = 5 * time.Second
	refreshThreshold              = time.Hour
	responseBodyLimit       int64 = 1 << 20
)

// Token is the persisted TIDAL OAuth session. A [TokenStore] holds exactly one.
type Token struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    int64
	UserID       string
	CountryCode  string
	SessionID    string
}

// TokenStore persists and retrieves the single cached [Token]. It is the seam
// implemented by callers (for example a database-backed store) so this package
// never depends on a concrete storage backend. Implementations must be safe for
// concurrent use by multiple goroutines.
type TokenStore interface {
	// Load returns the currently stored token.
	Load(ctx context.Context) (Token, error)
	// Save persists tok as the single stored token, replacing any existing one.
	Save(ctx context.Context, tok Token) error
}

// Client performs the TIDAL device authorization grant and token refresh
// against the configured endpoints. It is safe for concurrent use by multiple
// goroutines. Construct one with [New].
type Client struct {
	clientID         string
	clientSecret     string
	store            TokenStore
	httpClient       *http.Client
	baseURL          string
	now              func() time.Time
	slowDownIncrease time.Duration
	refreshGroup     singleflight.Group
}

// New constructs a [Client] that authenticates as clientID/clientSecret and
// persists tokens through store. Credentials are injected by the caller so this
// package stays independent of any configuration source.
func New(clientID, clientSecret string, store TokenStore, opts ...Option) *Client {
	c := &Client{
		clientID:         clientID,
		clientSecret:     clientSecret,
		store:            store,
		httpClient:       newRetryingHTTPClient(),
		baseURL:          DefaultBaseURL,
		now:              time.Now,
		slowDownIncrease: defaultSlowDownIncrease,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// newRetryingHTTPClient builds the default transport for the token endpoints: a
// retrying client that mirrors the TIDAL API client's policy, reusing the
// shared 429-aware [tidal.Backoff] and the same retry budget. It retries only
// transient failures, as decided by [checkRetry], and preserves the historical
// per-attempt 30s timeout. Callers may override it with [WithHTTPClient].
func newRetryingHTTPClient() *http.Client {
	rc := retryablehttp.NewClient()
	rc.Logger = nil
	rc.HTTPClient.Timeout = defaultHTTPTimeout
	rc.RetryMax = tidal.DefaultRetryMax
	rc.RetryWaitMin = tidal.DefaultRetryWaitMin
	rc.RetryWaitMax = tidal.DefaultRetryWaitMax
	rc.Backoff = tidal.Backoff
	rc.CheckRetry = checkRetry
	return rc.StandardClient()
}

// checkRetry retries only transient token-endpoint failures: HTTP 5xx, HTTP 429,
// and network/transport errors. It never retries 4xx responses (400/401 carry
// OAuth semantics classified by the caller, and the device-poll soft errors
// authorization_pending/slow_down arrive as 400s), and it stops immediately on
// context cancellation or an exceeded deadline, surfacing the context error.
func checkRetry(ctx context.Context, resp *http.Response, err error) (bool, error) {
	if cerr := ctx.Err(); cerr != nil {
		return false, fmt.Errorf("auth: retry aborted: %w", cerr)
	}
	transientStatus := resp != nil &&
		(resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= http.StatusInternalServerError)
	//nolint:nilerr // CheckRetry signals "retry this request" as (true, nil); a transient transport error must stay unsurfaced here or no retry happens.
	return err != nil || transientStatus, nil
}
