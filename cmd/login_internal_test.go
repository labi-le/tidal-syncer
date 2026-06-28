// White-box tests for the login command wiring. The file is named with the
// `internal_test.go` suffix so the testpackage linter permits package main
// (its skip-regexp covers export/internal test files), letting the tests drive
// the unexported runLogin/resolveCredentials directly while injecting a mock
// OAuth base URL through auth.Option.

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/labi-le/tidal-syncer/internal/authstore"
	"github.com/labi-le/tidal-syncer/internal/config"
	"github.com/labi-le/tidal-syncer/internal/store"
	"github.com/labi-le/tidal-syncer/pkg/tidal/auth"
)

const (
	fixedLoginUnix  int64 = 1_700_000_000
	tokenTTLSeconds int64 = 604_800
	wantExpiresAt   int64 = fixedLoginUnix + tokenTTLSeconds
)

const (
	wantVerificationURL = "link.tidal.com/ABCD"
	deviceAuthBody      = `{"deviceCode":"DEV-1","verificationUriComplete":"link.tidal.com/ABCD","expiresIn":300,"interval":0}`
	tokenSuccessBody    = `{"access_token":"ACCESS-1","refresh_token":"REFRESH-1","expires_in":604800,` +
		`"user_id":42,"sessionId":"SESS-1","user":{"countryCode":"DE"}}`
	invalidClientBody = `{"error":"invalid_client"}`
	expiredTokenBody  = `{"error":"expired_token"}`
)

const configFileMode os.FileMode = 0o600

func loginFixedClock() time.Time {
	return time.Unix(fixedLoginUnix, 0)
}

// startMockOAuth serves the TIDAL device + token endpoints, replying to the
// token poll with the supplied status and body.
func startMockOAuth(t *testing.T, tokenStatus int, tokenBody string) string {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/device_authorization", func(w http.ResponseWriter, _ *http.Request) {
		writeJSONReply(w, http.StatusOK, deviceAuthBody)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		writeJSONReply(w, tokenStatus, tokenBody)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv.URL
}

func writeJSONReply(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

// writeLoginConfig writes a minimal valid config whose data dir is dataDir and
// returns its path.
func writeLoginConfig(t *testing.T, dataDir string) string {
	t.Helper()

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	content := fmt.Sprintf("paths:\n  data: %q\n", dataDir)
	if err := os.WriteFile(cfgPath, []byte(content), configFileMode); err != nil {
		t.Fatalf("write config: %v", err)
	}

	return cfgPath
}

// assertStoredToken reopens the store at dataDir and asserts the persisted token
// matches the mock login response.
func assertStoredToken(t *testing.T, dataDir string) {
	t.Helper()

	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if err = st.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	got, err := st.GetToken(t.Context())
	if err != nil {
		t.Fatalf("get token: %v", err)
	}

	want := store.Token{
		AccessToken: "ACCESS-1", RefreshToken: "REFRESH-1",
		ExpiresAt: wantExpiresAt, UserID: "42", CountryCode: "DE", SessionID: "SESS-1",
	}
	if got != want {
		t.Errorf("stored token: got %+v, want %+v", got, want)
	}
}

func TestRunLoginPersistsTokenAndLogsLink(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	cfgPath := writeLoginConfig(t, dataDir)
	baseURL := startMockOAuth(t, http.StatusOK, tokenSuccessBody)

	var buf bytes.Buffer
	lg := zerolog.New(&buf)

	err := runLogin(t.Context(), cfgPath, false, lg,
		auth.WithBaseURL(baseURL),
		auth.WithClock(loginFixedClock),
	)
	if err != nil {
		t.Fatalf("runLogin: %v", err)
	}

	if logs := buf.String(); !strings.Contains(logs, wantVerificationURL) {
		t.Errorf("verification link not logged; logs=%s", logs)
	}

	assertStoredToken(t, dataDir)
}

func TestRunLoginDeadCredentialsReturnsGuidance(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	cfgPath := writeLoginConfig(t, dataDir)
	baseURL := startMockOAuth(t, http.StatusUnauthorized, invalidClientBody)

	var buf bytes.Buffer
	lg := zerolog.New(&buf)

	err := runLogin(t.Context(), cfgPath, false, lg,
		auth.WithBaseURL(baseURL),
		auth.WithClock(loginFixedClock),
	)
	if !errors.Is(err, auth.ErrDeadCredentials) {
		t.Fatalf("runLogin error: got %v, want ErrDeadCredentials", err)
	}
	if logs := buf.String(); !strings.Contains(logs, "client_id") {
		t.Errorf("dead-credential guidance not logged; logs=%s", logs)
	}
}

func TestRunLoginMissingConfigFails(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	lg := zerolog.New(&buf)

	err := runLogin(t.Context(), filepath.Join(t.TempDir(), "nonexistent.yaml"), false, lg)
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
}

func TestResolveCredentials(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      config.TidalAuth
		wantID  string
		wantSec string
	}{
		{"config overrides defaults", config.TidalAuth{ClientID: "cfg-id", ClientSecret: "cfg-secret"}, "cfg-id", "cfg-secret"},
		{"empty falls back to ldflag defaults", config.TidalAuth{}, defaultClientID, defaultClientSecret},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			id, secret := resolveCredentials(tc.in)
			if id != tc.wantID || secret != tc.wantSec {
				t.Errorf("got (%q, %q), want (%q, %q)", id, secret, tc.wantID, tc.wantSec)
			}
		})
	}
}

// newDriveTestClient opens and migrates a store under dataDir, then builds an
// auth client pointed at a mock OAuth server whose token endpoint replies with
// tokenStatus/tokenBody. It returns the client and the open store so callers can
// assert on the persisted token through the same handle.
func newDriveTestClient(t *testing.T, dataDir string, tokenStatus int, tokenBody string) (*auth.Client, *store.Store) {
	t.Helper()

	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if err = st.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate store: %v", err)
	}

	baseURL := startMockOAuth(t, tokenStatus, tokenBody)
	client := auth.New("test-id", "test-secret", authstore.New(st),
		auth.WithBaseURL(baseURL),
		auth.WithClock(loginFixedClock),
	)

	return client, st
}

// assertTokenPersisted reads the token through st and asserts it matches the
// mock login response.
func assertTokenPersisted(t *testing.T, st *store.Store) {
	t.Helper()

	got, err := st.GetToken(t.Context())
	if err != nil {
		t.Fatalf("get token: %v", err)
	}

	want := store.Token{
		AccessToken: "ACCESS-1", RefreshToken: "REFRESH-1",
		ExpiresAt: wantExpiresAt, UserID: "42", CountryCode: "DE", SessionID: "SESS-1",
	}
	if got != want {
		t.Errorf("stored token: got %+v, want %+v", got, want)
	}
}

// TestDriveDeviceAuth_Success proves the shared helper starts the grant, hands
// the verification link to onLink, and drives PollToken to completion so the
// token is persisted through the store. driveDeviceAuth is the device-flow
// sequence the login command depends on.
func TestDriveDeviceAuth_Success(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	client, st := newDriveTestClient(t, dataDir, http.StatusOK, tokenSuccessBody)

	var gotURL string
	err := driveDeviceAuth(t.Context(), client, func(da auth.DeviceAuth) {
		gotURL = da.VerificationURIComplete
	})
	if err != nil {
		t.Fatalf("driveDeviceAuth: %v", err)
	}

	if gotURL != wantVerificationURL {
		t.Errorf("onLink URL: got %q, want %q", gotURL, wantVerificationURL)
	}
	assertTokenPersisted(t, st)
}

// TestDriveDeviceAuth_Expiry proves an expired device code surfaces from the
// shared helper as auth.ErrDeviceCodeExpired.
func TestDriveDeviceAuth_Expiry(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	client, _ := newDriveTestClient(t, dataDir, http.StatusBadRequest, expiredTokenBody)

	err := driveDeviceAuth(t.Context(), client, func(auth.DeviceAuth) {})
	if !errors.Is(err, auth.ErrDeviceCodeExpired) {
		t.Fatalf("driveDeviceAuth error: got %v, want ErrDeviceCodeExpired", err)
	}
}
