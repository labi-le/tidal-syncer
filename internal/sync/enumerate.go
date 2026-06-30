package sync

import (
	"cmp"
	"context"
	"fmt"
	"iter"
	"slices"
	"strconv"

	"github.com/labi-le/tidal-syncer/pkg/tidal"
)

// enumerate resolves the desired track set for the configured scope, deduplicated
// by TIDAL track id and returned in a deterministic id order. It also returns the
// favorite-add timestamp per track id for the favorites it enumerated; tracks
// reached only via favorited albums or playlists have no entry.
func (e *Engine) enumerate(ctx context.Context) ([]tidal.Track, map[int]string, error) {
	desired := make(map[int]tidal.Track)
	dates := make(map[int]string)
	if err := e.enumerateInto(ctx, desired, dates); err != nil {
		return nil, nil, err
	}

	return sortedTracks(desired), dates, nil
}

// enumerateInto fills dst from either the whole library or the enabled favorite
// collections, according to the scope, recording favorite-add dates into dates.
func (e *Engine) enumerateInto(ctx context.Context, dst map[int]tidal.Track, dates map[int]string) error {
	if e.config.Scope.All {
		return e.enumerateAll(ctx, dst, dates)
	}

	return e.enumerateFavorites(ctx, dst, dates)
}

// enumerateAll unions favorite tracks with the tracks of every favorited album
// and playlist.
func (e *Engine) enumerateAll(ctx context.Context, dst map[int]tidal.Track, dates map[int]string) error {
	if err := collectFavoriteTracks(e.client.FavoriteTracks(ctx), dst, dates); err != nil {
		return err
	}
	if err := e.expandAlbums(ctx, dst); err != nil {
		return err
	}

	return e.expandPlaylists(ctx, dst)
}

// enumerateFavorites honors each favorites toggle independently.
func (e *Engine) enumerateFavorites(ctx context.Context, dst map[int]tidal.Track, dates map[int]string) error {
	if e.config.Scope.Favorites.Tracks {
		if err := collectFavoriteTracks(e.client.FavoriteTracks(ctx), dst, dates); err != nil {
			return err
		}
	}
	if e.config.Scope.Favorites.Albums {
		if err := e.expandAlbums(ctx, dst); err != nil {
			return err
		}
	}
	if e.config.Scope.Favorites.Playlists {
		if err := e.expandPlaylists(ctx, dst); err != nil {
			return err
		}
	}

	return nil
}

// expandAlbums adds every track of every favorited album into dst.
func (e *Engine) expandAlbums(ctx context.Context, dst map[int]tidal.Track) error {
	for album, iterErr := range e.client.FavoriteAlbums(ctx) {
		if iterErr != nil {
			return fmt.Errorf("enumerate favorite albums: %w", iterErr)
		}
		if err := collectTracks(e.client.AlbumTracks(ctx, strconv.Itoa(album.ID)), dst); err != nil {
			return err
		}
	}

	return nil
}

// expandPlaylists adds every track of every favorited playlist into dst.
func (e *Engine) expandPlaylists(ctx context.Context, dst map[int]tidal.Track) error {
	for playlist, iterErr := range e.client.FavoritePlaylists(ctx) {
		if iterErr != nil {
			return fmt.Errorf("enumerate favorite playlists: %w", iterErr)
		}
		if err := collectTracks(e.client.PlaylistTracks(ctx, playlist.UUID), dst); err != nil {
			return err
		}
	}

	return nil
}

// collectTracks drains a track stream into dst keyed by track id, stopping at
// the first stream error.
func collectTracks(seq iter.Seq2[tidal.Track, error], dst map[int]tidal.Track) error {
	for track, err := range seq {
		if err != nil {
			return fmt.Errorf("enumerate tracks: %w", err)
		}
		dst[track.ID] = track
	}

	return nil
}

// collectFavoriteTracks drains a favorite-track stream into dst keyed by track
// id, recording each track's favorite-add date into dates, stopping at the first
// stream error.
func collectFavoriteTracks(
	seq iter.Seq2[tidal.FavoriteTrack, error], dst map[int]tidal.Track, dates map[int]string,
) error {
	for fav, err := range seq {
		if err != nil {
			return fmt.Errorf("enumerate favorite tracks: %w", err)
		}
		dst[fav.Track.ID] = fav.Track
		dates[fav.Track.ID] = fav.AddedAt
	}

	return nil
}

// sortedTracks returns the map values ordered by ascending track id.
func sortedTracks(m map[int]tidal.Track) []tidal.Track {
	tracks := make([]tidal.Track, 0, len(m))
	for _, track := range m {
		tracks = append(tracks, track)
	}
	slices.SortFunc(tracks, func(a, b tidal.Track) int {
		return cmp.Compare(a.ID, b.ID)
	})

	return tracks
}
