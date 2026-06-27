// Package tidal is a small, reusable HTTP client for the TIDAL API. It handles
// authentication, client-side rate limiting, 429-aware retry backoff, and
// secret redaction for logging, while leaving endpoint-specific request and
// response types to its callers.
package tidal

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"golang.org/x/time/rate"
)

const (
	// DefaultBaseURL is the production TIDAL API v1 base URL.
	DefaultBaseURL = "https://api.tidal.com/v1"
	// DefaultRequestsPerMinute is the default client-side request rate limit.
	DefaultRequestsPerMinute = 60
	// DefaultRetryMax is the default number of retries for transient failures.
	DefaultRetryMax = 4
	// DefaultRetryWaitMin is the default minimum backoff between retries.
	DefaultRetryWaitMin = time.Second
	// DefaultRetryWaitMax is the default maximum backoff between retries.
	DefaultRetryWaitMax = 30 * time.Second
)

const (
	rateLimitBurst      = 1
	authorizationHeader = "Authorization"
	acceptHeader        = "Accept"
	bearerPrefix        = "Bearer "
	jsonMediaType       = "application/json"
	countryCodeParam    = "countryCode"
)

// TokenSource supplies the credentials needed to authenticate TIDAL API
// requests. Token is called before every request, so implementations may
// transparently refresh expired credentials. Implementations must be safe for
// concurrent use by multiple goroutines.
type TokenSource interface {
	// Token returns a valid OAuth access token, the ISO 3166-1 alpha-2 country
	// code used to scope the request, and the authenticated user's ID.
	Token(ctx context.Context) (access, countryCode, userID string, err error)
}

// Client is a rate-limited, retrying HTTP client for the TIDAL API. It is safe
// for concurrent use by multiple goroutines. Construct one with [New].
type Client struct {
	httpClient *retryablehttp.Client
	limiter    *rate.Limiter
	tokens     TokenSource
	baseURL    string
}

// New constructs a Client that authenticates every request through tokens.
// Without options it targets [DefaultBaseURL] at [DefaultRequestsPerMinute].
func New(tokens TokenSource, opts ...Option) *Client {
	cfg := config{
		baseURL:           DefaultBaseURL,
		requestsPerMinute: DefaultRequestsPerMinute,
		retryMax:          DefaultRetryMax,
		retryWaitMin:      DefaultRetryWaitMin,
		retryWaitMax:      DefaultRetryWaitMax,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.requestsPerMinute <= 0 {
		cfg.requestsPerMinute = DefaultRequestsPerMinute
	}

	httpClient := retryablehttp.NewClient()
	httpClient.Logger = nil
	httpClient.RetryMax = cfg.retryMax
	httpClient.RetryWaitMin = cfg.retryWaitMin
	httpClient.RetryWaitMax = cfg.retryWaitMax
	httpClient.Backoff = Backoff

	interval := time.Minute / time.Duration(cfg.requestsPerMinute)
	return &Client{
		httpClient: httpClient,
		limiter:    rate.NewLimiter(rate.Every(interval), rateLimitBurst),
		tokens:     tokens,
		baseURL:    cfg.baseURL,
	}
}

// UserID returns the authenticated user's TIDAL user ID, obtained from the
// configured [TokenSource].
func (c *Client) UserID(ctx context.Context) (string, error) {
	_, _, userID, err := c.tokens.Token(ctx)
	if err != nil {
		return "", fmt.Errorf("tidal: token source: %w", err)
	}
	return userID, nil
}

// Do executes an authenticated, rate-limited request against the TIDAL API and
// returns the raw response. The path is resolved against the configured base
// URL, and the caller's query is merged with the mandatory countryCode
// parameter taken from the [TokenSource].
//
// The returned error is a [*APIError] for any non-2xx response and a wrapped
// transport error otherwise. On a nil error the caller owns resp.Body and MUST
// close it; on any error resp is nil and there is nothing to close.
func (c *Client) Do(
	ctx context.Context,
	method, path string,
	query url.Values,
) (*http.Response, error) {
	access, countryCode, _, err := c.tokens.Token(ctx)
	if err != nil {
		return nil, fmt.Errorf("tidal: token source: %w", err)
	}

	if waitErr := c.limiter.Wait(ctx); waitErr != nil {
		return nil, fmt.Errorf("tidal: rate limit: %w", waitErr)
	}

	endpoint, err := c.resolve(path, countryCode, query)
	if err != nil {
		return nil, err
	}

	req, err := retryablehttp.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("tidal: new request: %w", err)
	}
	req.Header.Set(authorizationHeader, bearerPrefix+access)
	req.Header.Set(acceptHeader, jsonMediaType)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tidal: %s %s: %w", method, path, err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		defer func() { _ = resp.Body.Close() }()
		return nil, newAPIError(resp)
	}
	return resp, nil
}

// resolve builds the absolute request URL by joining path onto the base URL and
// merging query with the mandatory countryCode parameter.
func (c *Client) resolve(path, countryCode string, query url.Values) (string, error) {
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return "", fmt.Errorf("tidal: parse base url: %w", err)
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/" + strings.TrimLeft(path, "/")

	merged := base.Query()
	for key, values := range query {
		for _, value := range values {
			merged.Add(key, value)
		}
	}
	merged.Set(countryCodeParam, countryCode)
	base.RawQuery = merged.Encode()
	return base.String(), nil
}
