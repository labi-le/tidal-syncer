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

	const ins = `INSERT INTO favorites_snapshot (kind, tidal_id, name) VALUES (?, ?, ?)`
	for _, item := range items {
		if _, err = tx.ExecContext(ctx, ins, kind, item.TidalID, item.Name); err != nil {
			return fmt.Errorf("insert snapshot %q row %q: %w", kind, item.TidalID, err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit snapshot %q: %w", kind, err)
	}

	return nil
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
