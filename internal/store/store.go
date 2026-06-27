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
	"path"
	"path/filepath"
	"strconv"
	"strings"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
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

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("store: not found")

// ErrBadMigrationName is returned when an embedded migration file name lacks a
// numeric version prefix (for example "0001_init.sql").
var ErrBadMigrationName = errors.New("store: bad migration name")

// Store is a single-writer SQLite cache backed by database/sql.
type Store struct {
	db *sql.DB
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

	return &Store{db: db}, nil
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

	return nil
}

func (s *Store) ensureMigrationsTable(ctx context.Context) error {
	const ddl = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`
	if _, err := s.db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}

	return nil
}

func (s *Store) applyMigration(ctx context.Context, name string) error {
	version, err := migrationVersion(name)
	if err != nil {
		return err
	}

	applied, err := s.migrationApplied(ctx, version)
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

func (s *Store) migrationApplied(ctx context.Context, version int) (bool, error) {
	const q = `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = ?)`
	var exists bool
	if err := s.db.QueryRowContext(ctx, q, version).Scan(&exists); err != nil {
		return false, fmt.Errorf("check migration %d: %w", version, err)
	}

	return exists, nil
}

func (s *Store) runMigration(ctx context.Context, version int, ddl string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %d: %w", version, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err = tx.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("exec migration %d: %w", version, err)
	}
	const record = `INSERT INTO schema_migrations (version, applied_at) VALUES (?, datetime('now'))`
	if _, err = tx.ExecContext(ctx, record, version); err != nil {
		return fmt.Errorf("record migration %d: %w", version, err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %d: %w", version, err)
	}

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
