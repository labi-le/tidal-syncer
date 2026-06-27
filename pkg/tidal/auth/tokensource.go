package auth

import (
	"context"
	"fmt"
)

// TokenSource adapts a [Client] to the access-token read path expected by the
// TIDAL API client. It loads the stored token, refreshing it when it is within
// one hour of expiry, and is safe for concurrent use by multiple goroutines.
type TokenSource struct {
	client *Client
}

// NewTokenSource returns a [TokenSource] backed by client.
func NewTokenSource(client *Client) *TokenSource {
	return &TokenSource{client: client}
}

// Token returns a valid access token, the ISO 3166-1 alpha-2 country code, and
// the authenticated user's ID, refreshing the stored token when it is close to
// expiry. It satisfies the token-source contract expected by the API client.
func (s *TokenSource) Token(ctx context.Context) (access, countryCode, userID string, err error) {
	tok, err := s.client.store.Load(ctx)
	if err != nil {
		return "", "", "", fmt.Errorf("auth: load token: %w", err)
	}

	if s.client.needsRefresh(tok) {
		tok, err = s.client.Refresh(ctx)
		if err != nil {
			return "", "", "", fmt.Errorf("auth: token source refresh: %w", err)
		}
	}

	return tok.AccessToken, tok.CountryCode, tok.UserID, nil
}
