package sync

import (
	"context"
	"iter"

	"github.com/labi-le/tidal-syncer/pkg/tidal"
)

// TidalClient is the narrow TIDAL surface the engine consumes. The concrete
// *tidal.Client satisfies the metadata and favorites methods directly; the
// per-collection track expanders (AlbumTracks, PlaylistTracks) are provided by
// an orchestrator-level adapter so the engine never depends on the wire client.
type TidalClient interface {
	// UserID returns the authenticated user's TIDAL id, refreshing the token as
	// a side effect so the engine can validate credentials before a run.
	UserID(ctx context.Context) (string, error)
	// FavoriteTracks streams the user's favorite tracks one page at a time, each
	// paired with the timestamp it was added to the user's favorites.
	FavoriteTracks(ctx context.Context) iter.Seq2[tidal.FavoriteTrack, error]
	// FavoriteAlbums streams the user's favorite albums one page at a time.
	FavoriteAlbums(ctx context.Context) iter.Seq2[tidal.Album, error]
	// FavoritePlaylists streams the user's favorite playlists one page at a time.
	FavoritePlaylists(ctx context.Context) iter.Seq2[tidal.Playlist, error]
	// AlbumTracks streams every track on the album with albumID.
	AlbumTracks(ctx context.Context, albumID string) iter.Seq2[tidal.Track, error]
	// PlaylistTracks streams every track in the playlist with playlistUUID.
	PlaylistTracks(ctx context.Context, playlistUUID string) iter.Seq2[tidal.Track, error]
	// Album fetches the full album record for id.
	Album(ctx context.Context, id string) (tidal.Album, error)
	// Lyrics fetches a track's plain and synced lyrics by id.
	Lyrics(ctx context.Context, id string) (tidal.Lyrics, error)
	// TrackGenres fetches the genres TIDAL associates with a track by id; the
	// result is empty when the track has no genre.
	TrackGenres(ctx context.Context, id string) ([]string, error)
}

// Downloader fetches a single track's audio to destPath and reports the audio
// quality actually obtained. The concrete *download.Downloader satisfies it.
type Downloader interface {
	// Download writes trackID to destPath and returns the obtained quality tier.
	Download(ctx context.Context, trackID, destPath string) (obtainedQuality tidal.Quality, err error)
}

// CoverFetcher retrieves album cover artwork as encoded JPEG bytes.
type CoverFetcher interface {
	// Cover fetches the cover image identified by the album cover uuid.
	Cover(ctx context.Context, uuid string) ([]byte, error)
}
