package sync

import (
	"context"
	"fmt"
	"iter"

	"github.com/labi-le/tidal-syncer/internal/store"
	"github.com/labi-le/tidal-syncer/pkg/tidal"
)

const (
	collectionKindAlbum    = "album"
	collectionKindPlaylist = "playlist"
)

// cachedCollection describes one favorited collection (album or playlist) to
// expand into tracks, plus the metadata needed to decide whether the
// store-backed expansion cache is still valid.
type cachedCollection struct {
	kind      string
	id        string
	version   string
	immutable bool
	fetch     func() iter.Seq2[tidal.Track, error]
}

// valid reports whether a cached expansion may be reused. Albums are immutable, so
// a present entry is always valid — an entry is only ever written after a complete,
// error-free fetch; playlists are versioned by lastUpdated and require a non-empty
// matching version.
func (c cachedCollection) valid(cached store.CollectionCache, ok bool) bool {
	if !ok {
		return false
	}
	if c.immutable {
		return true
	}

	return c.version != "" && c.version == cached.Version
}

// expandCollection adds a collection's tracks to dst, serving them from the
// store cache when the cached expansion is still valid and every cached track is
// already settled on disk; otherwise it fetches the live track list and refreshes
// the cache.
func (e *Engine) expandCollection(ctx context.Context, dst map[int]tidal.Track, c cachedCollection) error {
	cached, ok, err := e.store.GetCollection(ctx, c.kind, c.id)
	if err != nil {
		return fmt.Errorf("read collection cache %s %q: %w", c.kind, c.id, err)
	}
	if c.valid(cached, ok) && e.allSettled(ctx, cached.TrackIDs) {
		emitStubs(dst, cached.TrackIDs)

		return nil
	}
	got, err := fetchCollectionTracks(c.fetch(), dst)
	if err != nil {
		return err
	}
	if err = e.store.PutCollection(ctx, c.kind, c.id, c.version, got); err != nil {
		return fmt.Errorf("cache collection %s %q: %w", c.kind, c.id, err)
	}

	return nil
}

// allSettled reports whether every track id is already downloaded at (or above)
// the requested quality, reusing the same decision the downloader applies so a
// raised quality floor or a --retry-failed run correctly re-expands the collection.
func (e *Engine) allSettled(ctx context.Context, ids []int) bool {
	for _, id := range ids {
		skip, err := e.shouldSkip(ctx, tidal.Track{ID: id})
		if err != nil || !skip {
			return false
		}
	}

	return true
}

// emitStubs adds id-only track stubs for a cached collection, never overwriting a
// track already present with full metadata (e.g. one also reached as a favorite).
func emitStubs(dst map[int]tidal.Track, ids []int) {
	for _, id := range ids {
		if _, present := dst[id]; !present {
			dst[id] = tidal.Track{ID: id}
		}
	}
}

// fetchCollectionTracks drains the live track iterator into dst and returns the
// track ids in listing order for caching. Full metadata always overwrites any
// stub already present.
func fetchCollectionTracks(seq iter.Seq2[tidal.Track, error], dst map[int]tidal.Track) ([]int, error) {
	var got []int
	for track, err := range seq {
		if err != nil {
			return nil, fmt.Errorf("enumerate collection tracks: %w", err)
		}
		dst[track.ID] = track
		got = append(got, track.ID)
	}

	return got, nil
}
