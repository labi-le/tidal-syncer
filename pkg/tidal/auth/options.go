package auth

import (
	"net/http"
	"time"
)

// Option customizes a [Client] constructed by [New].
type Option func(*Client)

// WithBaseURL overrides the OAuth base URL. It is primarily useful for testing
// against a mock authorization server.
func WithBaseURL(baseURL string) Option {
	return func(c *Client) {
		c.baseURL = baseURL
	}
}

// WithHTTPClient sets the HTTP client used for authorization requests.
func WithHTTPClient(httpClient *http.Client) Option {
	return func(c *Client) {
		c.httpClient = httpClient
	}
}

// WithClock overrides the time source used to compute token expiry, enabling
// deterministic tests.
func WithClock(now func() time.Time) Option {
	return func(c *Client) {
		c.now = now
	}
}

// WithSlowDownIncrement sets how much the poll interval grows after the server
// answers a poll with slow_down. It defaults to five seconds.
func WithSlowDownIncrement(increment time.Duration) Option {
	return func(c *Client) {
		c.slowDownIncrease = increment
	}
}
