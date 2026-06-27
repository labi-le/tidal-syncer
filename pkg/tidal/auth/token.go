package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	oauthErrAuthorizationPending = "authorization_pending"
	oauthErrSlowDown             = "slow_down"
	oauthErrExpiredToken         = "expired_token"
	oauthErrInvalidClient        = "invalid_client"
)

// tokenResponse is the subset of the TIDAL token endpoint payload this package
// consumes. Fields absent from a given response decode to their zero value.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	UserID       int64  `json:"user_id"`
	SessionID    string `json:"sessionId"`
	User         struct {
		CountryCode string `json:"countryCode"`
	} `json:"user"`
}

// postToken performs a form POST to the token endpoint with HTTP Basic client
// authentication and classifies the response into a [tokenResponse] or a typed
// error.
func (c *Client) postToken(ctx context.Context, form url.Values) (tokenResponse, error) {
	endpoint := c.baseURL + tokenPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, fmt.Errorf("auth: build token request: %w", err)
	}
	req.Header.Set(contentTypeHeader, formContentType)
	req.Header.Set(acceptHeader, jsonMediaType)
	req.Header.Set(authHeader, "Basic "+basicAuth(c.clientID, c.clientSecret))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return tokenResponse{}, fmt.Errorf("auth: token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return decodeTokenResponse(resp)
}

// decodeTokenResponse maps an HTTP token response to a [tokenResponse] or a
// typed error based on its status code.
func decodeTokenResponse(resp *http.Response) (tokenResponse, error) {
	switch resp.StatusCode {
	case http.StatusOK:
		var tr tokenResponse
		if err := json.NewDecoder(io.LimitReader(resp.Body, responseBodyLimit)).Decode(&tr); err != nil {
			return tokenResponse{}, fmt.Errorf("auth: decode token response: %w", err)
		}
		return tr, nil
	case http.StatusUnauthorized:
		return tokenResponse{}, fmt.Errorf("auth: token endpoint status 401: %w", ErrDeadCredentials)
	case http.StatusBadRequest:
		return tokenResponse{}, classifyBadRequest(resp)
	default:
		return tokenResponse{}, fmt.Errorf("auth: token endpoint status %d: %w", resp.StatusCode, errUnexpectedStatus)
	}
}

// classifyBadRequest decodes the OAuth error field from a 400 response and maps
// it to the matching internal or exported sentinel.
func classifyBadRequest(resp *http.Response) error {
	var body struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, responseBodyLimit)).Decode(&body)

	switch body.Error {
	case oauthErrAuthorizationPending:
		return errAuthorizationPending
	case oauthErrSlowDown:
		return errSlowDown
	case oauthErrExpiredToken:
		return ErrDeviceCodeExpired
	case oauthErrInvalidClient:
		return ErrDeadCredentials
	default:
		return fmt.Errorf("auth: token endpoint error %q: %w", body.Error, errUnexpectedStatus)
	}
}

// basicAuth builds the value for an HTTP Basic Authorization header from the
// client credentials.
func basicAuth(clientID, clientSecret string) string {
	return base64.StdEncoding.EncodeToString([]byte(clientID + ":" + clientSecret))
}

// formatUserID renders a numeric TIDAL user ID as a string, returning the empty
// string for a zero (absent) ID.
func formatUserID(id int64) string {
	if id == 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}

// tokenFromResponse builds a persisted [Token] from a successful login token
// response, stamping the absolute expiry from the configured clock.
func (c *Client) tokenFromResponse(tr tokenResponse) Token {
	return Token{
		AccessToken:  tr.AccessToken,
		RefreshToken: tr.RefreshToken,
		ExpiresAt:    c.expiryFromNow(tr.ExpiresIn),
		UserID:       formatUserID(tr.UserID),
		CountryCode:  tr.User.CountryCode,
		SessionID:    tr.SessionID,
	}
}

// expiryFromNow converts a relative expires_in in seconds into an absolute Unix
// expiry using the client clock.
func (c *Client) expiryFromNow(expiresIn int64) int64 {
	return c.now().Add(time.Duration(expiresIn) * time.Second).Unix()
}
