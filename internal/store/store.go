// Package store is the SQLite-backed local cache for tidal-syncer: TIDAL token
// storage, per-track download state, and favorites-snapshot diffing. It is a
// thin persistence layer over database/sql with a pure-Go driver and contains
// no business logic.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"modernc.org/sqlite"             // registers the "sqlite" driver + exposes *sqlite.Error
	sqlite3 "modernc.org/sqlite/lib" // SQLITE_BUSY result code
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

const (
	driverName    = "sqlite"
	dbFileName    = "tidal-syncer.db"
	migrationsDir = "migrations"
	busyTimeoutMs = 5000
	maxOpenConns  = 1
)

// dbFileMode is the permission bits enforced on the SQLite database file.
// 0o600 keeps it readable only by the owner because the DB holds the long-lived
// TIDAL refresh token; mirrors internal/lock.lockFileMode.
const dbFileMode os.FileMode = 0o600

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("store: not found")

// ErrBadMigrationName is returned when an embedded migration file name lacks a
// numeric version prefix (for example "0001_init.sql").
var ErrBadMigrationName = errors.New("store: bad migration name")

// Store is a single-writer SQLite cache backed by database/sql.
type Store struct {
	db     *sql.DB
	dbPath string
}

// Open opens (creating it if needed) the cache database under dataDir and
// configures it for single-writer WAL operation. It does not run migrations;
// callers must invoke Migrate before using the store.
func Open(dataDir string) (*Store, error) {
	dbPath := filepath.Join(dataDir, dbFileName)
	// busy_timeout must be the first pragma or modernc ignores it.
	dsn := fmt.Sprintf(
		"file:%s?_pragma=busy_timeout(%d)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)",
		dbPath, busyTimeoutMs,
	)

	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", dbPath, err)
	}
	db.SetMaxOpenConns(maxOpenConns)

	// Establish the first connection eagerly so the rollback->WAL conversion
	// happens here rather than lazily under the migration write lock.
	if err = establishWAL(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	if err = tightenDBFilePerms(dbPath); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &Store{db: db, dbPath: dbPath}, nil
}

// establishWAL forces the first physical connection, applying the DSN pragmas
// (notably the initial rollback->WAL conversion). That conversion needs a brief
// exclusive lock and, per SQLite, does NOT invoke the busy handler on a
// read->write lock upgrade, so busy_timeout cannot cover it: concurrent fresh
// opens must retry SQLITE_BUSY here until one connection wins the conversion.
func establishWAL(db *sql.DB) error {
	const retryDelay = 20 * time.Millisecond
	deadline := time.Now().Add(busyTimeoutMs * time.Millisecond)

	ctx := context.Background()
	for {
		err := db.PingContext(ctx)
		if err == nil {
			return nil
		}
		if !isBusy(err) || time.Now().After(deadline) {
			return fmt.Errorf("establish wal connection: %w", err)
		}
		time.Sleep(retryDelay)
	}
}

// isBusy reports whether err is a SQLITE_BUSY result from the sqlite driver.
func isBusy(err error) bool {
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_BUSY
}

// tightenDBFilePerms forces the SQLite file at dbPath to dbFileMode. Idempotent:
// also tightens a pre-existing loose file. No-op if the file was not (yet)
// materialized on disk by the driver.
func tightenDBFilePerms(dbPath string) error {
	if err := os.Chmod(dbPath, dbFileMode); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("chmod sqlite %q: %w", dbPath, err)
	}
	return nil
}

// Close releases the underlying database handle.
func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("close store: %w", err)
	}

	return nil
}

// Migrate applies every embedded migration not yet recorded in
// schema_migrations. It is idempotent: a second run is a no-op.
func (s *Store) Migrate(ctx context.Context) error {
	if err := s.ensureMigrationsTable(ctx); err != nil {
		return err
	}

	entries, err := fs.ReadDir(migrationsFS, migrationsDir)
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	for _, entry := range entries {
		if err = s.applyMigration(ctx, entry.Name()); err != nil {
			return err
		}
	}

	if err = tightenDBFilePerms(s.dbPath); err != nil {
		return err
	}

	return nil
}

func (s *Store) ensureMigrationsTable(ctx context.Context) error {
	const ddl = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`
	return s.withImmediateTx(ctx, func(conn *sql.Conn) error {
		if _, err := conn.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("ensure schema_migrations: %w", err)
		}

		return nil
	})
}

func (s *Store) applyMigration(ctx context.Context, name string) error {
	version, err := migrationVersion(name)
	if err != nil {
		return err
	}

	// Fast path: an autocommit read avoids taking the write lock for the common
	// already-applied no-op. The authoritative re-check happens inside the write
	// transaction below, so a stale "false" here is harmless.
	applied, err := migrationApplied(ctx, s.db, version)
	if err != nil {
		return err
	}
	if applied {
		return nil
	}

	ddl, err := migrationsFS.ReadFile(path.Join(migrationsDir, name))
	if err != nil {
		return fmt.Errorf("read migration %q: %w", name, err)
	}

	return s.runMigration(ctx, version, string(ddl))
}

// rowQuerier is the subset of *sql.DB / *sql.Conn used to probe
// schema_migrations; letting migrationApplied run either as an autocommit read
// or inside a held write transaction.
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func migrationApplied(ctx context.Context, q rowQuerier, version int) (bool, error) {
	const query = `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = ?)`
	var exists bool
	if err := q.QueryRowContext(ctx, query, version).Scan(&exists); err != nil {
		return false, fmt.Errorf("check migration %d: %w", version, err)
	}

	return exists, nil
}

// runMigration applies a single migration atomically: it re-checks the version
// and, if still unapplied, runs the DDL and records it inside one write
// transaction (see withImmediateTx). The re-check inside the lock turns a racer
// that committed first into a no-op rather than a duplicate-apply error.
func (s *Store) runMigration(ctx context.Context, version int, ddl string) error {
	return s.withImmediateTx(ctx, func(conn *sql.Conn) error {
		applied, err := migrationApplied(ctx, conn, version)
		if err != nil {
			return err
		}
		if applied {
			return nil
		}

		if _, err = conn.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("exec migration %d: %w", version, err)
		}
		const record = `INSERT INTO schema_migrations (version, applied_at) VALUES (?, datetime('now'))`
		if _, err = conn.ExecContext(ctx, record, version); err != nil {
			return fmt.Errorf("record migration %d: %w", version, err)
		}

		return nil
	})
}

// withImmediateTx runs fn inside a BEGIN IMMEDIATE transaction on one pinned
// connection, then COMMITs. The write lock is taken up front so a concurrent
// process (single-writer WAL) blocks on busy_timeout instead of failing with
// SQLITE_BUSY on a read->write lock upgrade — the busy handler is skipped for
// upgrades, so a plain autocommit write or a deferred BeginTx would race. fn
// MUST NOT commit or roll back; returning an error aborts and rolls back.
func (s *Store) withImmediateTx(ctx context.Context, fn func(conn *sql.Conn) error) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration conn: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin immediate: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			// Detached context so cleanup runs even if ctx was cancelled; a bare
			// conn.Close() with an open tx would leak the write lock.
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	if err = fn(conn); err != nil {
		return err
	}

	if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit migration tx: %w", err)
	}
	committed = true

	return nil
}

func migrationVersion(name string) (int, error) {
	idx := strings.IndexByte(name, '_')
	if idx <= 0 {
		return 0, fmt.Errorf("%w: %q", ErrBadMigrationName, name)
	}

	version, err := strconv.Atoi(name[:idx])
	if err != nil {
		return 0, fmt.Errorf("parse migration version %q: %w", name, err)
	}

	return version, nil
}
