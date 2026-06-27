package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labi-le/tidal-syncer/pkg/tidal/auth"
)

const (
	deviceAuthBody = `{"deviceCode":"DEV-1","userCode":"WXYZ",` +
		`"verificationUriComplete":"link.tidal.com/WXYZ","expiresIn":300,"interval":1}`
	deviceSuccessBody = `{"access_token":"ACCESS-1","refresh_token":"REFRESH-1",` +
		`"expires_in":604800,"user_id":12345,"sessionId":"SESSION-1","user":{"countryCode":"US"}}`
	wantVerificationURI = "link.tidal.com/WXYZ"
)

func TestDeviceFlowReturnsURIAndPersistsToken(t *testing.T) {
	t.Parallel()

	mock := &oauthMock{
		deviceBody: deviceAuthBody,
		tokenReplies: []reply{
			{status: http.StatusBadRequest, body: `{"status":400,"error":"authorization_pending","sub_status":1002}`},
			{status: http.StatusOK, body: deviceSuccessBody},
		},
	}
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	store := &fakeStore{}
	client := auth.New(testClientID, testClientSecret, store,
		auth.WithBaseURL(srv.URL),
		auth.WithClock(fixedClock),
	)

	ctx := t.Context()
	da, err := client.StartDeviceAuth(ctx)
	if err != nil {
		t.Fatalf("StartDeviceAuth: %v", err)
	}
	if da.VerificationURIComplete != wantVerificationURI {
		t.Errorf("verification uri: got %q, want %q", da.VerificationURIComplete, wantVerificationURI)
	}

	if err = client.PollToken(ctx, da.DeviceCode, pollInterval, da.Expiry); err != nil {
		t.Fatalf("PollToken: %v", err)
	}

	want := auth.Token{
		AccessToken:  "ACCESS-1",
		RefreshToken: "REFRESH-1",
		ExpiresAt:    fixedUnix + tokenTTLSeconds,
		UserID:       "12345",
		CountryCode:  "US",
		SessionID:    "SESSION-1",
	}
	got, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	if got != want {
		t.Errorf("persisted token:\n got %+v\nwant %+v", got, want)
	}
	if calls := mock.tokenCalls.Load(); calls != deviceFlowTokenCalls {
		t.Errorf("token endpoint calls: got %d, want %d", calls, deviceFlowTokenCalls)
	}
}

func TestDeviceFlowHonorsSlowDown(t *testing.T) {
	t.Parallel()

	mock := &oauthMock{
		deviceBody: deviceAuthBody,
		tokenReplies: []reply{
			{status: http.StatusBadRequest, body: `{"status":400,"error":"slow_down"}`},
			{status: http.StatusOK, body: deviceSuccessBody},
		},
	}
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	store := &fakeStore{}
	client := auth.New(testClientID, testClientSecret, store,
		auth.WithBaseURL(srv.URL),
		auth.WithClock(fixedClock),
		auth.WithSlowDownIncrement(slowDownTestIncrement),
	)

	ctx := t.Context()
	da, err := client.StartDeviceAuth(ctx)
	if err != nil {
		t.Fatalf("StartDeviceAuth: %v", err)
	}
	if err = client.PollToken(ctx, da.DeviceCode, pollInterval, da.Expiry); err != nil {
		t.Fatalf("PollToken: %v", err)
	}

	got, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	if got.AccessToken != "ACCESS-1" {
		t.Errorf("access token: got %q, want %q", got.AccessToken, "ACCESS-1")
	}
}
