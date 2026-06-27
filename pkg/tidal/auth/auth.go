// Package auth implements the TIDAL OAuth 2.0 device authorization grant and
// token refresh, with no dependency on any application package or logger. It
// returns data and typed errors and leaves all logging and credential storage
// to the caller: client credentials are injected into [New] and tokens are
// persisted through the caller-supplied [TokenStore].
package auth

import (
	"context"
	"net/http"
	"time"

	"golang.org/x/sync/singleflight"
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
		httpClient:       &http.Client{Timeout: defaultHTTPTimeout},
		baseURL:          DefaultBaseURL,
		now:              time.Now,
		slowDownIncrease: defaultSlowDownIncrease,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}
