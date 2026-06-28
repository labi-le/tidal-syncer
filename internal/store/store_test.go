package store_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/labi-le/tidal-syncer/internal/store"
)

const (
	wantDBPerm os.FileMode = 0o600
	loosePerm  os.FileMode = 0o644
	dbFileName             = "tidal-syncer.db"
)

func Test_Store_Open_creates_db_file_with_0600_perms(t *testing.T) {
	// Given a fresh data dir
	dir := t.TempDir()

	// When Open + Migrate run (which is the point at which the SQLite file
	// is created on disk by modernc/sqlite)
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		if cerr := st.Close(); cerr != nil {
			t.Errorf("close: %v", cerr)
		}
	})
	if err = st.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Then the on-disk DB file is 0600 (owner read/write only)
	info, err := os.Stat(filepath.Join(dir, dbFileName))
	if err != nil {
		t.Fatalf("stat db file: %v", err)
	}
	if got := info.Mode().Perm(); got != wantDBPerm {
		t.Errorf("db file perm: got %#o, want %#o", got, wantDBPerm)
	}
}

func Test_Store_Open_tightens_pre_existing_loose_db_file(t *testing.T) {
	// Given a pre-existing DB file with loose 0644 perms
	dir := t.TempDir()
	dbPath := filepath.Join(dir, dbFileName)
	if err := os.WriteFile(dbPath, nil, loosePerm); err != nil {
		t.Fatalf("seed loose db file: %v", err)
	}

	// When Open runs over the existing file
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		if cerr := st.Close(); cerr != nil {
			t.Errorf("close: %v", cerr)
		}
	})

	// Then the file is tightened to 0600
	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("stat db file: %v", err)
	}
	if got := info.Mode().Perm(); got != wantDBPerm {
		t.Errorf("db file perm: got %#o, want %#o", got, wantDBPerm)
	}
}

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
