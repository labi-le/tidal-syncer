package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/labi-le/tidal-syncer/internal/store"
)

func Test_Store_Token_round_trips(t *testing.T) {
	// Given
	ctx := context.Background()
	st := newStore(t)
	want := store.Token{
		AccessToken: "access-123", RefreshToken: "refresh-456",
		ExpiresAt: 1700000000, UserID: "42", CountryCode: "DE", SessionID: "sess-789",
	}

	// When
	if err := st.UpsertToken(ctx, want); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := st.GetToken(ctx)

	// Then
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func Test_Store_UpsertToken_overwrites_existing_single_row(t *testing.T) {
	// Given an existing token
	ctx := context.Background()
	st := newStore(t)
	if err := st.UpsertToken(ctx, store.Token{AccessToken: "old", RefreshToken: "r1"}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// When a second token is upserted
	if err := st.UpsertToken(ctx, store.Token{AccessToken: "new", RefreshToken: "r2"}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	// Then only the latest value is stored
	got, err := st.GetToken(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.AccessToken != "new" || got.RefreshToken != "r2" {
		t.Errorf("expected overwritten token, got %+v", got)
	}
}

func Test_Store_GetToken_returns_ErrNotFound_when_empty(t *testing.T) {
	// Given an empty store
	st := newStore(t)

	// When
	_, err := st.GetToken(context.Background())

	// Then
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
