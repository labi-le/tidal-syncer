package store

import (
	"context"
	"fmt"
	"slices"
)

// SnapshotItem is one entry in a favorites snapshot.
type SnapshotItem struct {
	TidalID string
	Name    string
	AddedAt string
}

// ReplaceSnapshot atomically replaces the stored snapshot for kind with items.
func (s *Store) ReplaceSnapshot(ctx context.Context, kind string, items []SnapshotItem) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin replace snapshot %q: %w", kind, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err = tx.ExecContext(ctx, `DELETE FROM favorites_snapshot WHERE kind = ?`, kind); err != nil {
		return fmt.Errorf("clear snapshot %q: %w", kind, err)
	}

	const ins = `INSERT INTO favorites_snapshot (kind, tidal_id, name, added_at) VALUES (?, ?, ?, ?)`
	for _, item := range items {
		if _, err = tx.ExecContext(ctx, ins, kind, item.TidalID, item.Name, item.AddedAt); err != nil {
			return fmt.Errorf("insert snapshot %q row %q: %w", kind, item.TidalID, err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit snapshot %q: %w", kind, err)
	}

	return nil
}

// FavoritesByRecency returns the snapshot items for kind that carry an add date,
// newest first, capped at limit. Items without a favorite-add date (those pulled
// in only by a favorited album or playlist) are excluded.
func (s *Store) FavoritesByRecency(ctx context.Context, kind string, limit int) ([]SnapshotItem, error) {
	const q = `SELECT tidal_id, name, added_at FROM favorites_snapshot ` +
		`WHERE kind = ? AND added_at != '' ORDER BY added_at DESC LIMIT ?`
	rows, err := s.db.QueryContext(ctx, q, kind, limit)
	if err != nil {
		return nil, fmt.Errorf("query favorites by recency %q: %w", kind, err)
	}
	defer func() { _ = rows.Close() }()

	var items []SnapshotItem
	for rows.Next() {
		var item SnapshotItem
		if err = rows.Scan(&item.TidalID, &item.Name, &item.AddedAt); err != nil {
			return nil, fmt.Errorf("scan favorite row: %w", err)
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate favorites by recency %q: %w", kind, err)
	}

	return items, nil
}

// FavoriteFile is a favorited track resolved to its downloaded file, carrying the
// favorite-add date so callers can order the local mirror by when each track was
// favorited on TIDAL.
type FavoriteFile struct {
	Title   string
	Path    string
	AddedAt string
}

// OrderedFavoriteFiles returns every favorite-kind track that carries a
// favorite-add date and a completed download on disk, most recently favorited
// first. Tracks reached only via a favorited album or playlist (no add date) and
// tracks without a finished download (no file) are excluded, so every returned
// path points at a real file.
func (s *Store) OrderedFavoriteFiles(ctx context.Context, kind string) ([]FavoriteFile, error) {
	const q = `SELECT f.name, t.path, f.added_at ` +
		`FROM favorites_snapshot f JOIN tracks t ON t.tidal_id = f.tidal_id ` +
		`WHERE f.kind = ? AND f.added_at <> '' AND t.status = 'done' AND t.path <> '' ` +
		`ORDER BY f.added_at DESC`
	rows, err := s.db.QueryContext(ctx, q, kind)
	if err != nil {
		return nil, fmt.Errorf("query ordered favorite files %q: %w", kind, err)
	}
	defer func() { _ = rows.Close() }()

	var files []FavoriteFile
	for rows.Next() {
		var file FavoriteFile
		if err = rows.Scan(&file.Title, &file.Path, &file.AddedAt); err != nil {
			return nil, fmt.Errorf("scan favorite file: %w", err)
		}
		files = append(files, file)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate ordered favorite files %q: %w", kind, err)
	}

	return files, nil
}

// DiffSnapshot compares items against the stored snapshot for kind and returns
// the tidal ids newly added (present in items, absent from the store) and those
// removed (present in the store, absent from items), each sorted ascending.
func (s *Store) DiffSnapshot(
	ctx context.Context, kind string, items []SnapshotItem,
) (added, removed []string, err error) {
	stored, err := s.snapshotIDs(ctx, kind)
	if err != nil {
		return nil, nil, err
	}

	incoming := make(map[string]struct{}, len(items))
	for _, item := range items {
		incoming[item.TidalID] = struct{}{}
	}

	added = make([]string, 0, len(incoming))
	for id := range incoming {
		if _, ok := stored[id]; !ok {
			added = append(added, id)
		}
	}

	removed = make([]string, 0, len(stored))
	for id := range stored {
		if _, ok := incoming[id]; !ok {
			removed = append(removed, id)
		}
	}

	slices.Sort(added)
	slices.Sort(removed)

	return added, removed, nil
}

func (s *Store) snapshotIDs(ctx context.Context, kind string) (map[string]struct{}, error) {
	const q = `SELECT tidal_id FROM favorites_snapshot WHERE kind = ?`
	rows, err := s.db.QueryContext(ctx, q, kind)
	if err != nil {
		return nil, fmt.Errorf("query snapshot %q: %w", kind, err)
	}
	defer func() { _ = rows.Close() }()

	ids := make(map[string]struct{})
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan snapshot id: %w", err)
		}
		ids[id] = struct{}{}
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate snapshot %q: %w", kind, err)
	}

	return ids, nil
}
