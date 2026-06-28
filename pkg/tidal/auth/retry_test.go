package auth_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labi-le/tidal-syncer/pkg/tidal/auth"
)

// retry_test.go exercises the retry-with-backoff transport wired into the auth
// [auth.Client] by default. It reuses the oauthMock harness, fakeStore, and
// shared fixtures declared in auth_test.go (same auth_test package). All new
// identifiers here are prefixed to stay collision-free with sibling test files.

const (
	retryRefreshSuccessBody = `{"access_token":"ACCESS-RETRY","expires_in":604800,` +
		`"user_id":12345,"user":{"countryCode":"US"},"sessionId":"SESSION-1"}`
	retryRefreshToken = "REFRESH-OLD"
	// retrySuccessCalls is the upstream call count for two transient 503s
	// followed by a 200: two retries plus the final success.
	retrySuccessCalls = 3
	// cancelGrace is how long the request is allowed to run before the test
	// cancels its context, comfortably inside the first ~1s backoff wait.
	cancelGrace = 50 * time.Millisecond
	// cancelDeadline bounds how long Refresh may take once cancelled; it must
	// abort well before the first backoff (1s) elapses.
	cancelDeadline = 500 * time.Millisecond
)

// newRetryStore returns a fakeStore primed with a refresh token so Refresh has
// something to exchange.
func newRetryStore() *fakeStore {
	return &fakeStore{tok: auth.Token{
		RefreshToken: retryRefreshToken,
		ExpiresAt:    fixedUnix + nearExpirySeconds,
	}}
}

// TestRetry_503ThenSuccess proves a transient 5xx is retried with backoff and
// then succeeds, hitting the upstream exactly retrySuccessCalls times.
func TestRetry_503ThenSuccess(t *testing.T) {
	t.Parallel()

	mock := &oauthMock{
		tokenReplies: []reply{
			{status: http.StatusServiceUnavailable, body: `{}`},
			{status: http.StatusServiceUnavailable, body: `{}`},
			{status: http.StatusOK, body: retryRefreshSuccessBody},
		},
	}
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	client := auth.New(testClientID, testClientSecret, newRetryStore(),
		auth.WithBaseURL(srv.URL),
		auth.WithClock(fixedClock),
	)

	tok, err := client.Refresh(t.Context())
	if err != nil {
		t.Fatalf("Refresh after transient 503s: got error %v, want success", err)
	}
	if tok.AccessToken != "ACCESS-RETRY" {
		t.Errorf("access token: got %q, want %q", tok.AccessToken, "ACCESS-RETRY")
	}
	if calls := mock.tokenCalls.Load(); calls != retrySuccessCalls {
		t.Fatalf("upstream token calls: got %d, want %d", calls, retrySuccessCalls)
	}
}

// TestRetry_NoRetryOn4xx proves a 4xx OAuth response (here 400 invalid_grant)
// is never retried: the upstream is hit exactly once and an error is returned.
// The concrete sentinel is owned by the error-classification task and is
// deliberately not asserted here to keep this test independent of it.
func TestRetry_NoRetryOn4xx(t *testing.T) {
	t.Parallel()

	mock := &oauthMock{
		tokenReplies: []reply{
			{status: http.StatusBadRequest, body: `{"error":"invalid_grant"}`},
			{status: http.StatusOK, body: retryRefreshSuccessBody},
		},
	}
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	client := auth.New(testClientID, testClientSecret, newRetryStore(),
		auth.WithBaseURL(srv.URL),
		auth.WithClock(fixedClock),
	)

	_, err := client.Refresh(t.Context())
	if err == nil {
		t.Fatal("Refresh on 400 invalid_grant: got nil error, want a non-retried failure")
	}
	if calls := mock.tokenCalls.Load(); calls != wantUpstreamCalls {
		t.Fatalf("upstream token calls on 4xx: got %d, want %d (must not retry)", calls, wantUpstreamCalls)
	}
}

// TestRetry_ContextCancelAborts proves an in-flight retry honors context
// cancellation: with the server stuck on 503, cancelling mid-backoff returns
// promptly with a context error and performs no further upstream calls.
func TestRetry_ContextCancelAborts(t *testing.T) {
	t.Parallel()

	mock := &oauthMock{
		tokenReplies: []reply{{status: http.StatusServiceUnavailable, body: `{}`}},
	}
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	client := auth.New(testClientID, testClientSecret, newRetryStore(),
		auth.WithBaseURL(srv.URL),
		auth.WithClock(fixedClock),
	)

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(cancelGrace)
		cancel()
	}()

	start := time.Now()
	_, err := client.Refresh(ctx)
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Refresh under cancellation: got %v, want context.Canceled", err)
	}
	if elapsed >= cancelDeadline {
		t.Fatalf("Refresh did not abort promptly: took %v, want < %v", elapsed, cancelDeadline)
	}
	if calls := mock.tokenCalls.Load(); calls != wantUpstreamCalls {
		t.Fatalf("upstream calls before cancel: got %d, want %d", calls, wantUpstreamCalls)
	}
}
