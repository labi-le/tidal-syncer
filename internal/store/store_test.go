package store_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/labi-le/tidal-syncer/internal/store"

	_ "modernc.org/sqlite" // registers the "sqlite" driver for the direct schema probe
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

func Test_Store_Migrate_is_safe_under_concurrent_invocation(t *testing.T) {
	// Given several independent Store handles on the SAME data dir. This mirrors
	// the sync/daemon/login/health/selfcheck entrypoints, each of which opens the
	// DB and calls Migrate; on a fresh DB they can run at the same time.
	const handles = 4
	dir := t.TempDir()
	ctx := context.Background()

	stores := make([]*store.Store, handles)
	for i := range stores {
		st, err := store.Open(dir)
		if err != nil {
			t.Fatalf("open handle %d: %v", i, err)
		}
		stores[i] = st
		t.Cleanup(func() {
			if cerr := st.Close(); cerr != nil {
				t.Errorf("close handle: %v", cerr)
			}
		})
	}

	// When every handle runs Migrate simultaneously
	var wg sync.WaitGroup
	errs := make([]error, handles)
	start := make(chan struct{})
	for i, st := range stores {
		wg.Go(func() {
			<-start
			errs[i] = st.Migrate(ctx)
		})
	}
	close(start)
	wg.Wait()

	// Then none of them races into a duplicate-apply error ("duplicate column
	// name" from ALTER, or a schema_migrations PRIMARY KEY conflict).
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent migrate handle %d: %v", i, err)
		}
	}

	// And every migration was recorded exactly once.
	assertMigrationsAppliedOnce(t, dir)
}

// assertMigrationsAppliedOnce opens the DB directly and verifies that no version
// appears more than once in schema_migrations and that at least one is present.
func assertMigrationsAppliedOnce(t *testing.T, dir string) {
	t.Helper()

	db, err := sql.Open("sqlite", filepath.Join(dir, dbFileName))
	if err != nil {
		t.Fatalf("open db for probe: %v", err)
	}
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("close probe db: %v", cerr)
		}
	})

	rows, err := db.QueryContext(context.Background(),
		`SELECT version, COUNT(*) FROM schema_migrations GROUP BY version`)
	if err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	defer func() { _ = rows.Close() }()

	count := 0
	for rows.Next() {
		var version, applied int
		if serr := rows.Scan(&version, &applied); serr != nil {
			t.Fatalf("scan schema_migrations: %v", serr)
		}
		if applied != 1 {
			t.Errorf("migration %d recorded %d times, want exactly 1", version, applied)
		}
		count++
	}
	if rerr := rows.Err(); rerr != nil {
		t.Fatalf("iterate schema_migrations: %v", rerr)
	}
	if count == 0 {
		t.Fatal("no migrations recorded in schema_migrations")
	}
}
