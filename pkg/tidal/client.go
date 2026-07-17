// Package tidal is a small, reusable HTTP client for the TIDAL API. It handles
// authentication, client-side rate limiting, 429-aware retry backoff, and
// secret redaction for logging, while leaving endpoint-specific request and
// response types to its callers.
package tidal

import (
	"context"
	"errors"
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
	// DefaultV2BaseURL is the production TIDAL openapi v2 base URL, used only for
	// the genre lookups the v1 API does not expose.
	DefaultV2BaseURL = "https://openapi.tidal.com/v2"
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
	v2BaseURL  string
}

// New constructs a Client that authenticates every request through tokens.
// Without options it targets [DefaultBaseURL] at [DefaultRequestsPerMinute].
func New(tokens TokenSource, opts ...Option) *Client {
	cfg := config{
		baseURL:           DefaultBaseURL,
		v2BaseURL:         DefaultV2BaseURL,
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
	httpClient.ErrorHandler = errorHandler

	interval := time.Minute / time.Duration(cfg.requestsPerMinute)
	return &Client{
		httpClient: httpClient,
		limiter:    rate.NewLimiter(rate.Every(interval), rateLimitBurst),
		tokens:     tokens,
		baseURL:    cfg.baseURL,
		v2BaseURL:  cfg.v2BaseURL,
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

// apiRequest describes a single API call. method, path and query are the usual
// HTTP inputs; baseURL and accept select the API version, since the v1 API
// speaks plain JSON while the v2 catalog speaks JSON:API at a different host.
type apiRequest struct {
	method  string
	baseURL string
	accept  string
	path    string
	query   url.Values
}

// Do executes an authenticated, rate-limited request against the TIDAL v1 API
// and returns the raw response. The path is resolved against the configured base
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
	return c.do(ctx, apiRequest{
		method:  method,
		baseURL: c.baseURL,
		accept:  jsonMediaType,
		path:    path,
		query:   query,
	})
}

// do is the shared transport for every API version: it authenticates, waits on
// the rate limiter, resolves r against its base URL, sets its Accept type, and
// classifies the result. The error and body-ownership contract is the one
// documented on [Client.Do].
func (c *Client) do(ctx context.Context, r apiRequest) (*http.Response, error) {
	access, countryCode, _, err := c.tokens.Token(ctx)
	if err != nil {
		return nil, fmt.Errorf("tidal: token source: %w", err)
	}

	if waitErr := c.limiter.Wait(ctx); waitErr != nil {
		return nil, fmt.Errorf("tidal: rate limit: %w", waitErr)
	}

	endpoint, err := resolve(r, countryCode)
	if err != nil {
		return nil, err
	}

	req, err := retryablehttp.NewRequestWithContext(ctx, r.method, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("tidal: new request: %w", err)
	}
	req.Header.Set(authorizationHeader, bearerPrefix+access)
	req.Header.Set(acceptHeader, r.accept)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// A retried non-2xx response reaches us as the *APIError built by
		// errorHandler; return it unwrapped so it is byte-for-byte identical
		// to the non-retried non-2xx path below.
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			return nil, apiErr
		}
		return nil, fmt.Errorf("tidal: %s %s: %w", r.method, r.path, err)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		defer func() { _ = resp.Body.Close() }()
		return nil, newAPIError(resp)
	}
	return resp, nil
}

// resolve builds the absolute request URL by joining r.path onto r.baseURL and
// merging r.query with the mandatory countryCode parameter.
func resolve(r apiRequest, countryCode string) (string, error) {
	base, err := url.Parse(r.baseURL)
	if err != nil {
		return "", fmt.Errorf("tidal: parse base url: %w", err)
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/" + strings.TrimLeft(r.path, "/")

	merged := base.Query()
	for key, values := range r.query {
		for _, value := range values {
			merged.Add(key, value)
		}
	}
	merged.Set(countryCodeParam, countryCode)
	base.RawQuery = merged.Encode()
	return base.String(), nil
}
