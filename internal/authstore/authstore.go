// Package authstore adapts the SQLite-backed token store to the device-auth
// package's TokenStore interface. It converts between the two identically
// shaped token types so pkg/tidal/auth never has to import internal/store and
// stays a reusable, application-agnostic package.
package authstore

import (
	"context"
	"fmt"

	"github.com/labi-le/tidal-syncer/internal/store"
	"github.com/labi-le/tidal-syncer/pkg/tidal/auth"
)

// Adapter bridges a [store.Store] to the [auth.TokenStore] interface, mapping
// each field between [auth.Token] and [store.Token].
type Adapter struct {
	store *store.Store
}

var _ auth.TokenStore = (*Adapter)(nil)

// New returns an Adapter that persists tokens through st.
func New(st *store.Store) *Adapter {
	return &Adapter{store: st}
}

// Load returns the stored token as an [auth.Token]. It propagates the store's
// error unchanged in spirit (wrapped), so callers can match [store.ErrNotFound]
// when no token has been persisted yet.
func (a *Adapter) Load(ctx context.Context) (auth.Token, error) {
	tok, err := a.store.GetToken(ctx)
	if err != nil {
		return auth.Token{}, fmt.Errorf("authstore: load token: %w", err)
	}

	return auth.Token{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		ExpiresAt:    tok.ExpiresAt,
		UserID:       tok.UserID,
		CountryCode:  tok.CountryCode,
		SessionID:    tok.SessionID,
	}, nil
}

// Save persists tok, converting it to the store's token type.
func (a *Adapter) Save(ctx context.Context, tok auth.Token) error {
	if err := a.store.UpsertToken(ctx, store.Token{
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		ExpiresAt:    tok.ExpiresAt,
		UserID:       tok.UserID,
		CountryCode:  tok.CountryCode,
		SessionID:    tok.SessionID,
	}); err != nil {
		return fmt.Errorf("authstore: save token: %w", err)
	}

	return nil
}
