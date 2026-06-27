package auth

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// Refresh exchanges the stored refresh token for a new access token and
// persists the result. Concurrent calls are collapsed by single-flight so that
// N callers trigger exactly one upstream refresh and share its outcome.
func (c *Client) Refresh(ctx context.Context) (Token, error) {
	value, err, _ := c.refreshGroup.Do(refreshGroupKey, func() (any, error) {
		tok, refreshErr := c.doRefresh(ctx)
		return tok, refreshErr
	})
	if err != nil {
		return Token{}, fmt.Errorf("auth: refresh: %w", err)
	}

	tok, ok := value.(Token)
	if !ok {
		return Token{}, fmt.Errorf("auth: refresh: unexpected result type %T: %w", value, errUnexpectedStatus)
	}
	return tok, nil
}

// doRefresh performs one refresh round-trip: load the current token, exchange
// its refresh token, merge the response, and persist the result.
func (c *Client) doRefresh(ctx context.Context) (Token, error) {
	current, err := c.store.Load(ctx)
	if err != nil {
		return Token{}, fmt.Errorf("load token: %w", err)
	}

	form := url.Values{}
	form.Set(paramClientID, c.clientID)
	form.Set(paramScope, scope)
	form.Set(paramGrantType, refreshTokenGrant)
	form.Set(paramRefreshToken, current.RefreshToken)

	tr, err := c.postToken(ctx, form)
	if err != nil {
		return Token{}, fmt.Errorf("exchange refresh token: %w", err)
	}

	next := mergeRefreshed(current, tr, c.now())
	if err = c.store.Save(ctx, next); err != nil {
		return Token{}, fmt.Errorf("save token: %w", err)
	}
	return next, nil
}

// mergeRefreshed overlays a refresh response onto the current token, preserving
// the existing refresh token and identity fields when the response omits them.
func mergeRefreshed(current Token, tr tokenResponse, now time.Time) Token {
	next := current
	next.AccessToken = tr.AccessToken
	next.ExpiresAt = now.Add(time.Duration(tr.ExpiresIn) * time.Second).Unix()
	if tr.RefreshToken != "" {
		next.RefreshToken = tr.RefreshToken
	}
	if tr.UserID != 0 {
		next.UserID = formatUserID(tr.UserID)
	}
	if tr.User.CountryCode != "" {
		next.CountryCode = tr.User.CountryCode
	}
	if tr.SessionID != "" {
		next.SessionID = tr.SessionID
	}
	return next
}

// needsRefresh reports whether tok is within refreshThreshold of expiry (or
// already expired) and therefore should be refreshed before use.
func (c *Client) needsRefresh(tok Token) bool {
	expiry := time.Unix(tok.ExpiresAt, 0)
	return !c.now().Add(refreshThreshold).Before(expiry)
}
