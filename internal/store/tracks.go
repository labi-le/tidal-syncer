package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Status is the download state of a track.
type Status string

const (
	// StatusDone marks a successfully downloaded track.
	StatusDone Status = "done"
	// StatusFailed marks a track whose download failed.
	StatusFailed Status = "failed"
)

// Track is the cached download state for a single TIDAL track.
type Track struct {
	TidalID          string
	ISRC             string
	AlbumID          string
	Path             string
	ObtainedQuality  string
	RequestedQuality string
	Status           Status
	UpdatedAt        int64
}

// MarkTrack inserts or updates the cached state for a track, stamping
// updated_at with the current database time.
func (s *Store) MarkTrack(ctx context.Context, tr Track) error {
	const q = `INSERT INTO tracks
		(tidal_id, isrc, album_id, path, obtained_quality, requested_quality, status, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, unixepoch())
		ON CONFLICT(tidal_id) DO UPDATE SET
			isrc              = excluded.isrc,
			album_id          = excluded.album_id,
			path              = excluded.path,
			obtained_quality  = excluded.obtained_quality,
			requested_quality = excluded.requested_quality,
			status            = excluded.status,
			updated_at        = excluded.updated_at`
	if _, err := s.db.ExecContext(ctx, q,
		tr.TidalID, tr.ISRC, tr.AlbumID, tr.Path,
		tr.ObtainedQuality, tr.RequestedQuality, string(tr.Status),
	); err != nil {
		return fmt.Errorf("mark track %q: %w", tr.TidalID, err)
	}

	return nil
}

// GetTrack returns the cached state for the track with the given TIDAL id, or
// ErrNotFound if it is absent.
func (s *Store) GetTrack(ctx context.Context, tidalID string) (Track, error) {
	const q = `SELECT tidal_id, isrc, album_id, path, obtained_quality, requested_quality, status, updated_at
		FROM tracks WHERE tidal_id = ?`
	var (
		tr     Track
		status string
	)
	err := s.db.QueryRowContext(ctx, q, tidalID).Scan(
		&tr.TidalID, &tr.ISRC, &tr.AlbumID, &tr.Path,
		&tr.ObtainedQuality, &tr.RequestedQuality, &status, &tr.UpdatedAt,
	)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Track{}, fmt.Errorf("get track %q: %w", tidalID, ErrNotFound)
	case err != nil:
		return Track{}, fmt.Errorf("get track %q: %w", tidalID, err)
	}
	tr.Status = Status(status)

	return tr, nil
}
