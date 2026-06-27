package auth_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labi-le/tidal-syncer/pkg/tidal/auth"
)

const (
	testClientID     = "test-client-id"
	testClientSecret = "test-client-secret"

	fixedUnix         int64 = 1_700_000_000
	tokenTTLSeconds   int64 = 604_800
	nearExpirySeconds int64 = 600

	pollInterval          = 10 * time.Millisecond
	serverDelay           = 100 * time.Millisecond
	slowDownTestIncrement = 1 * time.Millisecond
	deviceExpiry          = 5 * time.Minute

	concurrentCallers    = 20
	wantUpstreamCalls    = 1
	deviceFlowTokenCalls = 2
)

// tokenProvider mirrors the consumer interface the TIDAL API client expects, so
// the test can assert *auth.TokenSource satisfies it structurally without
// importing that client.
type tokenProvider interface {
	Token(ctx context.Context) (access, countryCode, userID string, err error)
}

var _ tokenProvider = (*auth.TokenSource)(nil)

func fixedClock() time.Time {
	return time.Unix(fixedUnix, 0)
}

// fakeStore is an in-memory auth.TokenStore for tests.
type fakeStore struct {
	mu      sync.Mutex
	tok     auth.Token
	loadErr error
}

func (f *fakeStore) Load(_ context.Context) (auth.Token, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.loadErr != nil {
		return auth.Token{}, f.loadErr
	}
	return f.tok, nil
}

func (f *fakeStore) Save(_ context.Context, tok auth.Token) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tok = tok
	return nil
}

type reply struct {
	status int
	body   string
}

// oauthMock is a scripted pair of TIDAL OAuth endpoints driven by httptest.
type oauthMock struct {
	deviceBody   string
	tokenReplies []reply
	tokenDelay   time.Duration
	tokenCalls   atomic.Int64
	tokenSeq     atomic.Int64
}

func (m *oauthMock) handler() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/device_authorization", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, m.deviceBody)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		m.tokenCalls.Add(1)
		if m.tokenDelay > 0 {
			time.Sleep(m.tokenDelay)
		}
		idx := int(m.tokenSeq.Add(1)) - 1
		if idx >= len(m.tokenReplies) {
			idx = len(m.tokenReplies) - 1
		}
		rep := m.tokenReplies[idx]
		writeJSON(w, rep.status, rep.body)
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func TestDeviceFlowDeadCredentialsReturnsErrDeadCredentials(t *testing.T) {
	t.Parallel()

	mock := &oauthMock{
		tokenReplies: []reply{{status: http.StatusUnauthorized, body: `{"error":"invalid_client"}`}},
	}
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	store := &fakeStore{}
	client := auth.New(testClientID, testClientSecret, store,
		auth.WithBaseURL(srv.URL),
		auth.WithClock(fixedClock),
	)

	err := client.PollToken(t.Context(), "DEV-1", pollInterval, fixedClock().Add(deviceExpiry))
	if !errors.Is(err, auth.ErrDeadCredentials) {
		t.Fatalf("PollToken error: got %v, want ErrDeadCredentials", err)
	}
}

func TestRefreshDeadCredentialsReturnsErrDeadCredentials(t *testing.T) {
	t.Parallel()

	mock := &oauthMock{
		tokenReplies: []reply{{status: http.StatusUnauthorized, body: `{"error":"invalid_client"}`}},
	}
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	store := &fakeStore{tok: auth.Token{
		RefreshToken: "REFRESH-OLD",
		ExpiresAt:    fixedUnix + nearExpirySeconds,
	}}
	client := auth.New(testClientID, testClientSecret, store,
		auth.WithBaseURL(srv.URL),
		auth.WithClock(fixedClock),
	)

	_, err := client.Refresh(t.Context())
	if !errors.Is(err, auth.ErrDeadCredentials) {
		t.Fatalf("Refresh error: got %v, want ErrDeadCredentials", err)
	}
}
