package tidal

import "time"

// config holds the resolved settings for a [Client]. It is populated from the
// package defaults and then mutated by the [Option] values passed to [New].
type config struct {
	baseURL           string
	requestsPerMinute int
	retryMax          int
	retryWaitMin      time.Duration
	retryWaitMax      time.Duration
}

// Option customizes a [Client] at construction time.
type Option func(*config)

// WithBaseURL overrides the API base URL. It is primarily useful in tests to
// point the client at an httptest server.
func WithBaseURL(baseURL string) Option {
	return func(c *config) { c.baseURL = baseURL }
}

// WithRequestsPerMinute sets the client-side rate limit. Values at or below
// zero are ignored in favor of [DefaultRequestsPerMinute].
func WithRequestsPerMinute(rpm int) Option {
	return func(c *config) { c.requestsPerMinute = rpm }
}

// WithRetryMax sets the maximum number of retries for transient failures.
// A value of zero disables retrying.
func WithRetryMax(retries int) Option {
	return func(c *config) { c.retryMax = retries }
}

// WithRetryWaitMin sets the minimum backoff between retries.
func WithRetryWaitMin(d time.Duration) Option {
	return func(c *config) { c.retryWaitMin = d }
}

// WithRetryWaitMax caps the backoff between retries, including any delay
// honored from a 429 Retry-After header.
func WithRetryWaitMax(d time.Duration) Option {
	return func(c *config) { c.retryWaitMax = d }
}
