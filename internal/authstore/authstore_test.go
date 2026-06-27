package authstore_test

import (
	"errors"
	"testing"

	"github.com/labi-le/tidal-syncer/internal/authstore"
	"github.com/labi-le/tidal-syncer/internal/store"
	"github.com/labi-le/tidal-syncer/pkg/tidal/auth"
)

const wantExpiresAt int64 = 1_700_000_000

// newAdapter builds an Adapter over a freshly migrated temp-dir store.
func newAdapter(t *testing.T) *authstore.Adapter {
	t.Helper()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if err = st.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	return authstore.New(st)
}

func TestAdapterSaveLoadRoundTrip(t *testing.T) {
	t.Parallel()

	adapter := newAdapter(t)
	ctx := t.Context()
	want := auth.Token{
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		ExpiresAt:    wantExpiresAt,
		UserID:       "42",
		CountryCode:  "DE",
		SessionID:    "sess-1",
	}

	if err := adapter.Save(ctx, want); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := adapter.Load(ctx)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got != want {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, want)
	}
}

func TestAdapterLoadMissingReturnsNotFound(t *testing.T) {
	t.Parallel()

	adapter := newAdapter(t)

	_, err := adapter.Load(t.Context())
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("load empty: got %v, want store.ErrNotFound", err)
	}
}
