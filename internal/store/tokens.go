package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Token is the persisted TIDAL OAuth session. The store keeps exactly one.
type Token struct {
	AccessToken  string
	RefreshToken string
	ExpiresAt    int64
	UserID       string
	CountryCode  string
	SessionID    string
}

// UpsertToken stores tok as the single cached token, overwriting any existing
// one.
func (s *Store) UpsertToken(ctx context.Context, tok Token) error {
	const q = `INSERT INTO tokens
		(id, access_token, refresh_token, expires_at, user_id, country_code, session_id)
		VALUES (1, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			access_token  = excluded.access_token,
			refresh_token = excluded.refresh_token,
			expires_at    = excluded.expires_at,
			user_id       = excluded.user_id,
			country_code  = excluded.country_code,
			session_id    = excluded.session_id`
	if _, err := s.db.ExecContext(ctx, q,
		tok.AccessToken, tok.RefreshToken, tok.ExpiresAt,
		tok.UserID, tok.CountryCode, tok.SessionID,
	); err != nil {
		return fmt.Errorf("upsert token: %w", err)
	}

	return nil
}

// GetToken returns the cached token, or ErrNotFound if none is stored.
func (s *Store) GetToken(ctx context.Context) (Token, error) {
	const q = `SELECT access_token, refresh_token, expires_at, user_id, country_code, session_id
		FROM tokens WHERE id = 1`
	var tok Token
	err := s.db.QueryRowContext(ctx, q).Scan(
		&tok.AccessToken, &tok.RefreshToken, &tok.ExpiresAt,
		&tok.UserID, &tok.CountryCode, &tok.SessionID,
	)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Token{}, fmt.Errorf("get token: %w", ErrNotFound)
	case err != nil:
		return Token{}, fmt.Errorf("get token: %w", err)
	}

	return tok, nil
}
