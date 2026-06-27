package store_test

import (
	"context"
	"testing"

	"github.com/labi-le/tidal-syncer/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if cerr := st.Close(); cerr != nil {
			t.Errorf("close store: %v", cerr)
		}
	})
	if err = st.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	return st
}

func Test_Store_Migrate_is_idempotent_when_run_twice(t *testing.T) {
	// Given a freshly opened store
	ctx := context.Background()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		if cerr := st.Close(); cerr != nil {
			t.Errorf("close: %v", cerr)
		}
	})

	// When Migrate runs twice
	if err = st.Migrate(ctx); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if err = st.Migrate(ctx); err != nil {
		t.Fatalf("second migrate must be a no-op: %v", err)
	}

	// Then the schema is intact and usable
	want := store.Token{
		AccessToken: "a", RefreshToken: "r", ExpiresAt: 100,
		UserID: "u", CountryCode: "US", SessionID: "s",
	}
	if err = st.UpsertToken(ctx, want); err != nil {
		t.Fatalf("upsert token after double migrate: %v", err)
	}
	got, err := st.GetToken(ctx)
	if err != nil {
		t.Fatalf("get token: %v", err)
	}
	if got != want {
		t.Errorf("token mismatch: got %+v want %+v", got, want)
	}
}
