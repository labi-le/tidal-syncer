package tidal_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labi-le/tidal-syncer/pkg/tidal"
)

// testRPM is a deliberately high rate limit so the token-bucket limiter never
// delays a test; the rate-limiting behavior itself is covered by the unit
// limiter and is not the subject of these HTTP round-trip tests.
const testRPM = 60_000

// wantServerCalls is the number of upstream hits expected for the 429 retry
// scenario: one rejected request plus one successful retry.
const wantServerCalls = 2

// staticTokens is a TokenSource test double. It is the ONLY mock seam the
// package exposes, per the client contract.
type staticTokens struct {
	access      string
	countryCode string
	userID      string
	err         error
}

func (s staticTokens) Token(_ context.Context) (string, string, string, error) {
	return s.access, s.countryCode, s.userID, s.err
}

func newResponse(status int, retryAfter string) *http.Response {
	header := http.Header{}
	if retryAfter != "" {
		header.Set("Retry-After", retryAfter)
	}
	return &http.Response{StatusCode: status, Header: header}
}

func TestClientDo_appendsCountryCodeAndAuthHeader(t *testing.T) {
	t.Parallel()

	var gotCountry, gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotCountry = r.URL.Query().Get("countryCode")
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
	}))
	defer srv.Close()

	client := tidal.New(
		staticTokens{access: "tok-123", countryCode: "US", userID: "u-9"},
		tidal.WithBaseURL(srv.URL),
		tidal.WithRequestsPerMinute(testRPM),
	)

	resp, err := client.Do(context.Background(), http.MethodGet, "/sessions", nil)
	if err != nil {
		t.Fatalf("Do: unexpected error: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	if gotCountry != "US" {
		t.Errorf("countryCode query: got %q want %q", gotCountry, "US")
	}
	if gotAuth != "Bearer tok-123" {
		t.Errorf("Authorization header: got %q want %q", gotAuth, "Bearer tok-123")
	}
	if gotPath != "/sessions" {
		t.Errorf("request path: got %q want %q", gotPath, "/sessions")
	}
}

func TestClientDo_mergesCallerQuery(t *testing.T) {
	t.Parallel()

	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
	}))
	defer srv.Close()

	client := tidal.New(
		staticTokens{access: "t", countryCode: "NO", userID: "u"},
		tidal.WithBaseURL(srv.URL),
		tidal.WithRequestsPerMinute(testRPM),
	)

	query := url.Values{"query": {"radiohead"}, "limit": {"50"}}
	resp, err := client.Do(context.Background(), http.MethodGet, "/search", query)
	if err != nil {
		t.Fatalf("Do: unexpected error: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })

	if got.Get("query") != "radiohead" {
		t.Errorf("query param: got %q want %q", got.Get("query"), "radiohead")
	}
	if got.Get("limit") != "50" {
		t.Errorf("limit param: got %q want %q", got.Get("limit"), "50")
	}
	if got.Get("countryCode") != "NO" {
		t.Errorf("countryCode param: got %q want %q", got.Get("countryCode"), "NO")
	}
}

func TestBackoff_clientRetriesAndWaitsOn429(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer srv.Close()

	client := tidal.New(
		staticTokens{access: "t", countryCode: "US", userID: "u"},
		tidal.WithBaseURL(srv.URL),
		tidal.WithRequestsPerMinute(testRPM),
	)

	start := time.Now()
	resp, err := client.Do(context.Background(), http.MethodGet, "/x", nil)
	if err != nil {
		t.Fatalf("Do: unexpected error: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	elapsed := time.Since(start)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want %d", resp.StatusCode, http.StatusOK)
	}
	if got := calls.Load(); got != wantServerCalls {
		t.Fatalf("server calls: got %d want %d", got, wantServerCalls)
	}
	if elapsed < 900*time.Millisecond {
		t.Fatalf("elapsed %v: client did not honor Retry-After: 1", elapsed)
	}
}

func TestBackoff_honorsRetryAfter429(t *testing.T) {
	t.Parallel()

	got := tidal.Backoff(time.Second, 30*time.Second, 0, newResponse(http.StatusTooManyRequests, "2"))
	if got != 2*time.Second {
		t.Fatalf("Backoff with Retry-After=2: got %v want %v", got, 2*time.Second)
	}
}

func TestBackoff_capsRetryAfterAtMax(t *testing.T) {
	t.Parallel()

	got := tidal.Backoff(time.Second, 5*time.Second, 0, newResponse(http.StatusTooManyRequests, "100"))
	if got != 5*time.Second {
		t.Fatalf("Backoff with Retry-After=100 capped at 5s: got %v want %v", got, 5*time.Second)
	}
}

func TestBackoff_delegatesToDefaultWhenNot429(t *testing.T) {
	t.Parallel()

	got0 := tidal.Backoff(time.Second, 30*time.Second, 0, nil)
	got1 := tidal.Backoff(time.Second, 30*time.Second, 1, nil)
	if got0 <= 0 {
		t.Fatalf("Backoff attempt 0: got %v want > 0", got0)
	}
	if got1 < got0 {
		t.Fatalf("Backoff should grow with attempts: got1=%v got0=%v", got1, got0)
	}

	got500 := tidal.Backoff(time.Second, 30*time.Second, 0, newResponse(http.StatusInternalServerError, ""))
	if got500 != time.Second {
		t.Fatalf("Backoff for 500 attempt 0: got %v want %v", got500, time.Second)
	}
}

func TestClientDo_cancelledContextAbortsRequest(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	client := tidal.New(
		staticTokens{access: "t", countryCode: "US", userID: "u"},
		tidal.WithBaseURL(srv.URL),
		tidal.WithRequestsPerMinute(testRPM),
	)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	resp, err := client.Do(ctx, http.MethodGet, "/hang", nil)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("Do: want error from cancelled context, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Do: want context.Canceled in chain, got %v", err)
	}
}

func TestClientDo_returnsAPIErrorOnNon2xx(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"code":"track_not_found"}`)
	}))
	defer srv.Close()

	client := tidal.New(
		staticTokens{access: "t", countryCode: "US", userID: "u"},
		tidal.WithBaseURL(srv.URL),
		tidal.WithRequestsPerMinute(testRPM),
	)

	resp, err := client.Do(context.Background(), http.MethodGet, "/tracks/0", nil)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("Do: want APIError, got nil")
	}
	if resp != nil {
		t.Fatalf("Do: response must be nil on error, got %v", resp)
	}

	var apiErr *tidal.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("Do: want *tidal.APIError, got %v", err)
	}
	if apiErr.Status != http.StatusNotFound {
		t.Errorf("APIError.Status: got %d want %d", apiErr.Status, http.StatusNotFound)
	}
	if apiErr.Code != "track_not_found" {
		t.Errorf("APIError.Code: got %q want %q", apiErr.Code, "track_not_found")
	}
}

func TestClientDo_propagatesTokenSourceError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("token boom")
	client := tidal.New(
		staticTokens{err: wantErr},
		tidal.WithRequestsPerMinute(testRPM),
	)

	resp, err := client.Do(context.Background(), http.MethodGet, "/x", nil)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("Do: want token source error, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("Do: want wrapped token error, got %v", err)
	}
}

func TestClientDo_withRetryMaxZeroDoesNotRetry(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "100")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	client := tidal.New(
		staticTokens{access: "t", countryCode: "US", userID: "u"},
		tidal.WithBaseURL(srv.URL),
		tidal.WithRequestsPerMinute(testRPM),
		tidal.WithRetryMax(0),
		tidal.WithRetryWaitMin(time.Millisecond),
		tidal.WithRetryWaitMax(time.Second),
	)

	start := time.Now()
	resp, err := client.Do(context.Background(), http.MethodGet, "/x", nil)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("Do: want error after exhausting retries, got nil")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("server calls: got %d want 1 (no retry)", got)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("elapsed %v: RetryMax=0 must not wait for Retry-After", elapsed)
	}
}

func TestClientUserID_returnsTokenUserID(t *testing.T) {
	t.Parallel()

	client := tidal.New(
		staticTokens{userID: "user-42"},
		tidal.WithRequestsPerMinute(testRPM),
	)

	got, err := client.UserID(context.Background())
	if err != nil {
		t.Fatalf("UserID: unexpected error: %v", err)
	}
	if got != "user-42" {
		t.Errorf("UserID: got %q want %q", got, "user-42")
	}
}

func TestRedact_masksBearerToken(t *testing.T) {
	t.Parallel()

	in := "Authorization: Bearer eyJhbGciOi.JIUzI1NiI.sInR5cCISECRET"
	out := tidal.Redact(in)

	if strings.Contains(out, "eyJhbGciOi") || strings.Contains(out, "SECRET") {
		t.Fatalf("Redact leaked the bearer token: %q", out)
	}
	if !strings.Contains(out, "Bearer ") {
		t.Errorf("Redact dropped the Bearer label: %q", out)
	}
}

func TestRedact_masksSignedQueryParams(t *testing.T) {
	t.Parallel()

	in := "https://resources.tidal.com/f.flac?Policy=ABC123&Signature=SIGSECRET&Key-Pair-Id=KPID&foo=bar"
	out := tidal.Redact(in)

	if strings.Contains(out, "SIGSECRET") || strings.Contains(out, "ABC123") || strings.Contains(out, "KPID") {
		t.Fatalf("Redact leaked a signed query value: %q", out)
	}
	if !strings.Contains(out, "foo=bar") {
		t.Errorf("Redact stripped a non-secret param: %q", out)
	}
}

func TestRedact_preservesNonSecretText(t *testing.T) {
	t.Parallel()

	in := "GET /tracks/123?countryCode=US&limit=50 200 OK"
	if out := tidal.Redact(in); out != in {
		t.Errorf("Redact altered non-secret text: got %q want %q", out, in)
	}
}
