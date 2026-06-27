package auth_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/labi-le/tidal-syncer/pkg/tidal/auth"
)

const refreshSuccessBody = `{"access_token":"ACCESS-2","expires_in":604800,` +
	`"user_id":12345,"user":{"countryCode":"US"},"sessionId":"SESSION-1"}`

func TestRefreshSingleflightCollapsesConcurrentCalls(t *testing.T) {
	t.Parallel()

	mock := &oauthMock{
		tokenReplies: []reply{{status: http.StatusOK, body: refreshSuccessBody}},
		tokenDelay:   serverDelay,
	}
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	store := &fakeStore{tok: auth.Token{
		AccessToken:  "ACCESS-1",
		RefreshToken: "REFRESH-OLD",
		ExpiresAt:    fixedUnix + nearExpirySeconds,
		UserID:       "12345",
		CountryCode:  "US",
		SessionID:    "SESSION-1",
	}}
	client := auth.New(testClientID, testClientSecret, store,
		auth.WithBaseURL(srv.URL),
		auth.WithClock(fixedClock),
	)
	source := auth.NewTokenSource(client)

	ctx := t.Context()
	start := make(chan struct{})
	var wg sync.WaitGroup
	access := make([]string, concurrentCallers)
	errs := make([]error, concurrentCallers)
	for i := range concurrentCallers {
		wg.Go(func() {
			<-start
			tok, _, _, err := source.Token(ctx)
			access[i] = tok
			errs[i] = err
		})
	}
	close(start)
	wg.Wait()

	if calls := mock.tokenCalls.Load(); calls != wantUpstreamCalls {
		t.Fatalf("upstream refresh calls: got %d, want %d", calls, wantUpstreamCalls)
	}
	for i := range concurrentCallers {
		if errs[i] != nil {
			t.Errorf("caller %d: unexpected error: %v", i, errs[i])
		}
		if access[i] != "ACCESS-2" {
			t.Errorf("caller %d: access token: got %q, want %q", i, access[i], "ACCESS-2")
		}
	}

	persisted, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	if persisted.RefreshToken != "REFRESH-OLD" {
		t.Errorf("refresh token: got %q, want kept %q", persisted.RefreshToken, "REFRESH-OLD")
	}
	if persisted.AccessToken != "ACCESS-2" {
		t.Errorf("access token: got %q, want %q", persisted.AccessToken, "ACCESS-2")
	}
}
