package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// CollectionCache is a cached album/playlist expansion to its track ids.
type CollectionCache struct {
	Version  string
	NTracks  int
	TrackIDs []int
}

// GetCollection returns the cached expansion for the (kind, id) collection. The
// boolean is false when nothing is cached for that collection.
func (s *Store) GetCollection(ctx context.Context, kind, id string) (CollectionCache, bool, error) {
	const meta = `SELECT version, n_tracks FROM collection_cache WHERE kind = ? AND collection_id = ?`
	var cache CollectionCache
	err := s.db.QueryRowContext(ctx, meta, kind, id).Scan(&cache.Version, &cache.NTracks)
	if errors.Is(err, sql.ErrNoRows) {
		return CollectionCache{}, false, nil
	}
	if err != nil {
		return CollectionCache{}, false, fmt.Errorf("query collection %q/%q: %w", kind, id, err)
	}

	cache.TrackIDs, err = s.collectionTrackIDs(ctx, kind, id)
	if err != nil {
		return CollectionCache{}, false, err
	}

	return cache, true, nil
}

func (s *Store) collectionTrackIDs(ctx context.Context, kind, id string) ([]int, error) {
	const q = `SELECT track_id FROM collection_track WHERE kind = ? AND collection_id = ? ORDER BY track_id`
	rows, err := s.db.QueryContext(ctx, q, kind, id)
	if err != nil {
		return nil, fmt.Errorf("query collection tracks %q/%q: %w", kind, id, err)
	}
	defer func() { _ = rows.Close() }()

	var ids []int
	for rows.Next() {
		var trackID int
		if err = rows.Scan(&trackID); err != nil {
			return nil, fmt.Errorf("scan collection track: %w", err)
		}
		ids = append(ids, trackID)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate collection tracks %q/%q: %w", kind, id, err)
	}

	return ids, nil
}

// PutCollection replaces the cached expansion for the (kind, id) collection with
// version and trackIDs in a single transaction.
func (s *Store) PutCollection(ctx context.Context, kind, id, version string, trackIDs []int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin put collection %q/%q: %w", kind, id, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err = tx.ExecContext(ctx,
		`DELETE FROM collection_track WHERE kind = ? AND collection_id = ?`, kind, id); err != nil {
		return fmt.Errorf("clear collection tracks %q/%q: %w", kind, id, err)
	}

	const upsert = `INSERT INTO collection_cache (kind, collection_id, version, n_tracks, cached_at) ` +
		`VALUES (?, ?, ?, ?, unixepoch()) ` +
		`ON CONFLICT (kind, collection_id) DO UPDATE SET ` +
		`version = excluded.version, n_tracks = excluded.n_tracks, cached_at = excluded.cached_at`
	if _, err = tx.ExecContext(ctx, upsert, kind, id, version, len(trackIDs)); err != nil {
		return fmt.Errorf("upsert collection %q/%q: %w", kind, id, err)
	}

	const ins = `INSERT INTO collection_track (kind, collection_id, track_id) VALUES (?, ?, ?)`
	for _, trackID := range trackIDs {
		if _, err = tx.ExecContext(ctx, ins, kind, id, trackID); err != nil {
			return fmt.Errorf("insert collection track %q/%q id %d: %w", kind, id, trackID, err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit put collection %q/%q: %w", kind, id, err)
	}

	return nil
}

// PruneCollections deletes cached collections of kind whose id is not in keep,
// cascading to their track rows. It is housekeeping only: stale rows are already
// inert because enumeration consults the cache solely for currently favorited ids.
func (s *Store) PruneCollections(ctx context.Context, kind string, keep []string) error {
	existing, err := s.collectionIDs(ctx, kind)
	if err != nil {
		return err
	}

	keepSet := make(map[string]struct{}, len(keep))
	for _, id := range keep {
		keepSet[id] = struct{}{}
	}

	const del = `DELETE FROM collection_cache WHERE kind = ? AND collection_id = ?`
	for _, id := range existing {
		if _, ok := keepSet[id]; ok {
			continue
		}
		if _, err = s.db.ExecContext(ctx, del, kind, id); err != nil {
			return fmt.Errorf("prune collection %q/%q: %w", kind, id, err)
		}
	}

	return nil
}

func (s *Store) collectionIDs(ctx context.Context, kind string) ([]string, error) {
	const q = `SELECT collection_id FROM collection_cache WHERE kind = ?`
	rows, err := s.db.QueryContext(ctx, q, kind)
	if err != nil {
		return nil, fmt.Errorf("query collections %q: %w", kind, err)
	}
	defer func() { _ = rows.Close() }()

	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan collection id: %w", err)
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate collections %q: %w", kind, err)
	}

	return ids, nil
}
