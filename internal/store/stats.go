package store

import (
	"context"
	"fmt"
)

// LibraryCounts is an aggregate snapshot of the cached library, computed with
// plain SQL for the metrics collector. It carries no business logic; genre and
// artist derivation live in the caller.
type LibraryCounts struct {
	// ByStatus counts tracks grouped by download status (done/failed/pending).
	ByStatus map[string]int
	// ByObtainedQuality counts downloaded tracks grouped by obtained tier.
	ByObtainedQuality map[string]int
	// Favorites is the number of rows in the favorites snapshot.
	Favorites int
	// PermanentFailures counts tracks failed with the permanent flag set.
	PermanentFailures int
	// DistinctAlbums counts distinct album ids among downloaded tracks.
	DistinctAlbums int
	// LastSyncUnix is the most recent tracks.updated_at, or 0 when empty.
	LastSyncUnix int64
}

// LibraryCounts returns aggregate library counts for metrics. Every value is a
// single SQL aggregate; the method opens no transaction and only reads.
func (s *Store) LibraryCounts(ctx context.Context) (LibraryCounts, error) {
	lc := LibraryCounts{
		ByStatus:          map[string]int{},
		ByObtainedQuality: map[string]int{},
		Favorites:         0,
		PermanentFailures: 0,
		DistinctAlbums:    0,
		LastSyncUnix:      0,
	}

	if err := s.groupCount(ctx, `SELECT status, count(*) FROM tracks GROUP BY status`, lc.ByStatus); err != nil {
		return LibraryCounts{}, err
	}
	if err := s.groupCount(ctx,
		`SELECT obtained_quality, count(*) FROM tracks WHERE status = 'done' GROUP BY obtained_quality`,
		lc.ByObtainedQuality,
	); err != nil {
		return LibraryCounts{}, err
	}

	scalars := []struct {
		query string
		dst   *int64
	}{
		{`SELECT count(*) FROM favorites_snapshot`, nil},
		{`SELECT count(*) FROM tracks WHERE status = 'failed' AND permanent = 1`, nil},
		{`SELECT count(DISTINCT album_id) FROM tracks WHERE status = 'done' AND album_id <> ''`, nil},
		{`SELECT COALESCE(MAX(updated_at), 0) FROM tracks`, &lc.LastSyncUnix},
	}
	// Bind the int destinations that are not int64.
	var favorites, permanent, albums int64
	scalars[0].dst = &favorites
	scalars[1].dst = &permanent
	scalars[2].dst = &albums
	for _, sc := range scalars {
		if err := s.db.QueryRowContext(ctx, sc.query).Scan(sc.dst); err != nil {
			return LibraryCounts{}, fmt.Errorf("library counts scalar: %w", err)
		}
	}
	lc.Favorites = int(favorites)
	lc.PermanentFailures = int(permanent)
	lc.DistinctAlbums = int(albums)

	return lc, nil
}

// groupCount runs a two-column "key, count(*)" query and fills dst.
func (s *Store) groupCount(ctx context.Context, query string, dst map[string]int) error {
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("group count: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var key string
		var n int
		if err = rows.Scan(&key, &n); err != nil {
			return fmt.Errorf("scan group count: %w", err)
		}
		dst[key] = n
	}
	if err = rows.Err(); err != nil {
		return fmt.Errorf("iterate group count: %w", err)
	}

	return nil
}

// TrackFacet carries the genre and path of a downloaded track so the metrics
// collector can derive genre counts and distinct artists without the store
// owning that presentation logic.
type TrackFacet struct {
	Genre string
	Path  string
}

// DoneTrackFacets returns the genre and path of every downloaded track.
func (s *Store) DoneTrackFacets(ctx context.Context) ([]TrackFacet, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT genre, path FROM tracks WHERE status = 'done'`)
	if err != nil {
		return nil, fmt.Errorf("done track facets: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var facets []TrackFacet
	for rows.Next() {
		var f TrackFacet
		if err = rows.Scan(&f.Genre, &f.Path); err != nil {
			return nil, fmt.Errorf("scan track facet: %w", err)
		}
		facets = append(facets, f)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate track facets: %w", err)
	}

	return facets, nil
}
