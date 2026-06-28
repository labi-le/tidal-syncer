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
	oauthErrInvalidGrant         = "invalid_grant"
)

// transientErrorBodyLimit bounds how many bytes of an untrusted token-endpoint body are echoed into a transient error.
const transientErrorBodyLimit = 256

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

// decodeTokenResponse maps a 200 response to a [tokenResponse]; any non-200
// status is classified into a typed error from the OAuth error field in the
// response body by [classifyTokenError].
func decodeTokenResponse(resp *http.Response) (tokenResponse, error) {
	if resp.StatusCode == http.StatusOK {
		var tr tokenResponse
		if err := json.NewDecoder(io.LimitReader(resp.Body, responseBodyLimit)).Decode(&tr); err != nil {
			return tokenResponse{}, fmt.Errorf("auth: decode token response: %w", err)
		}
		return tr, nil
	}

	body, _ := io.ReadAll(io.LimitReader(resp.Body, responseBodyLimit))
	return tokenResponse{}, classifyTokenError(resp.StatusCode, body)
}

// classifyTokenError maps a non-200 token-endpoint response to a typed error
// from the OAuth 2.0 error field in its body (RFC 6749 §5.2), independent of the
// HTTP status code. A response without a recognizable OAuth error body (a
// bodiless 401 from a WAF or CDN, a 5xx, or a proxy hiccup) is treated as a
// transient, retryable failure rather than a fatal credential error.
func classifyTokenError(status int, body []byte) error {
	var oauthErr struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(body, &oauthErr)

	switch oauthErr.Error {
	case oauthErrAuthorizationPending:
		return errAuthorizationPending
	case oauthErrSlowDown:
		return errSlowDown
	case oauthErrExpiredToken:
		return ErrDeviceCodeExpired
	case oauthErrInvalidGrant:
		return ErrReauthRequired
	case oauthErrInvalidClient:
		return ErrDeadCredentials
	default:
		snippet := body
		if len(snippet) > transientErrorBodyLimit {
			snippet = snippet[:transientErrorBodyLimit]
		}
		return fmt.Errorf("auth: token endpoint status %d body %q: %w", status, snippet, errTransient)
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
