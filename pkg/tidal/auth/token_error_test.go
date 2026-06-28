package auth_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labi-le/tidal-syncer/pkg/tidal/auth"
)

func TestClassifyTokenErrorByBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		status  int
		body    string
		wantErr error
		notErrs []error
	}{
		{
			name:    "invalid_grant is reauth required not dead credentials",
			status:  http.StatusBadRequest,
			body:    `{"status":400,"error":"invalid_grant","sub_status":1005}`,
			wantErr: auth.ErrReauthRequired,
			notErrs: []error{auth.ErrDeadCredentials},
		},
		{
			name:    "invalid_client on 401 is dead credentials",
			status:  http.StatusUnauthorized,
			body:    `{"error":"invalid_client"}`,
			wantErr: auth.ErrDeadCredentials,
			notErrs: []error{auth.ErrReauthRequired},
		},
		{
			name:    "invalid_client on 400 is dead credentials",
			status:  http.StatusBadRequest,
			body:    `{"error":"invalid_client"}`,
			wantErr: auth.ErrDeadCredentials,
			notErrs: []error{auth.ErrReauthRequired},
		},
		{
			name:    "expired_token is device code expired",
			status:  http.StatusBadRequest,
			body:    `{"error":"expired_token"}`,
			wantErr: auth.ErrDeviceCodeExpired,
			notErrs: []error{auth.ErrDeadCredentials, auth.ErrReauthRequired},
		},
		{
			name:    "bare 401 with empty body is transient not dead credentials",
			status:  http.StatusUnauthorized,
			body:    "",
			wantErr: nil,
			notErrs: []error{auth.ErrDeadCredentials, auth.ErrReauthRequired, auth.ErrDeviceCodeExpired},
		},
		{
			name:    "503 with empty body is transient not dead credentials",
			status:  http.StatusServiceUnavailable,
			body:    "",
			wantErr: nil,
			notErrs: []error{auth.ErrDeadCredentials, auth.ErrReauthRequired},
		},
		{
			name:    "non-oauth html body is transient not dead credentials",
			status:  http.StatusUnauthorized,
			body:    `<html>blocked by waf</html>`,
			wantErr: nil,
			notErrs: []error{auth.ErrDeadCredentials, auth.ErrReauthRequired},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mock := &oauthMock{
				tokenReplies: []reply{{status: tc.status, body: tc.body}},
			}
			srv := httptest.NewServer(mock.handler())
			t.Cleanup(srv.Close)

			store := &fakeStore{tok: auth.Token{
				RefreshToken: "REFRESH-OLD",
				ExpiresAt:    fixedUnix + nearExpirySeconds,
			}}
			client := auth.New(testClientID, testClientSecret, store,
				auth.WithBaseURL(srv.URL),
				auth.WithClock(fixedClock),
			)

			_, err := client.Refresh(t.Context())
			if err == nil {
				t.Fatalf("Refresh: got nil error, want a classified error")
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Errorf("Refresh error: got %v, want errors.Is(_, %v)", err, tc.wantErr)
			}
			for _, notErr := range tc.notErrs {
				if errors.Is(err, notErr) {
					t.Errorf("Refresh error: got %v, must NOT match %v", err, notErr)
				}
			}
		})
	}
}
