package sync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rs/zerolog"

	"github.com/labi-le/tidal-syncer/internal/config"
	"github.com/labi-le/tidal-syncer/internal/ctxlog"
	"github.com/labi-le/tidal-syncer/internal/store"
)

const (
	// favoritesPlaylistName is the stem of the .m3u8 that mirrors the favorite-track
	// collection in favorite-add order. It is deliberately distinct from any user
	// playlist title so the two never overwrite each other under Playlists/.
	favoritesPlaylistName = "Favorite Tracks"
	// favoritesUnknownDuration marks an #EXTINF whose runtime is unknown; -1 is the
	// extended-M3U convention players accept for an unknown length.
	favoritesUnknownDuration = -1
	// opWriteFavorites scopes the logger for one favorites-export run.
	opWriteFavorites = "sync.WriteFavorites"
)

// favoritesStore reads the downloaded favorite tracks in favorite-add order.
type favoritesStore interface {
	OrderedFavoriteFiles(ctx context.Context, kind string) ([]store.FavoriteFile, error)
}

// FavoritesWriter exports the favorite-track collection as a single .m3u8 whose
// entries preserve TIDAL's favorite-add order, so offline playback follows the
// same sequence as the TIDAL app.
type FavoritesWriter struct {
	store  favoritesStore
	config config.Config
	logger zerolog.Logger
}

// NewFavoritesWriter builds a FavoritesWriter over st and cfg, tagging logger
// with the playlist component.
func NewFavoritesWriter(st favoritesStore, cfg config.Config, logger zerolog.Logger) *FavoritesWriter {
	return &FavoritesWriter{
		store:  st,
		config: cfg,
		logger: logger.With().Str("component", componentPlaylist).Logger(),
	}
}

// WriteFavorites writes <music>/Playlists/Favorite Tracks.m3u8 listing every
// downloaded favorite track newest-first. When no favorite has a downloaded file
// it writes nothing, leaving any previous export untouched.
func (w *FavoritesWriter) WriteFavorites(ctx context.Context) error {
	log := ctxlog.Op(w.logger, opWriteFavorites)
	files, err := w.store.OrderedFavoriteFiles(ctx, SnapshotKindTracks)
	if err != nil {
		return fmt.Errorf("read ordered favorite files: %w", err)
	}
	if len(files) == 0 {
		log.Debug().Msg("no downloaded favorites to export")

		return nil
	}

	dir := filepath.Join(w.config.Paths.Music, playlistsDirName)
	if err = os.MkdirAll(dir, musicDirMode); err != nil {
		return fmt.Errorf("create playlists directory: %w", err)
	}

	entries, err := favoritesEntries(dir, files)
	if err != nil {
		return err
	}

	dest := filepath.Join(dir, favoritesPlaylistName+playlistExt)
	if err = writeM3U8(dest, entries); err != nil {
		return err
	}
	log.Debug().Int("tracks", len(entries)).Msg("favorites playlist written")

	return nil
}

// favoritesEntries projects each favorite file into a playlistEntry carrying an
// unknown duration, no artist, and the audio path relative to dir, preserving the
// favorite-add order of files.
func favoritesEntries(dir string, files []store.FavoriteFile) ([]playlistEntry, error) {
	entries := make([]playlistEntry, 0, len(files))
	for _, file := range files {
		rel, err := filepath.Rel(dir, file.Path)
		if err != nil {
			return nil, fmt.Errorf("relativize favorite %q: %w", file.Path, err)
		}
		entries = append(entries, playlistEntry{
			durationSeconds: favoritesUnknownDuration,
			artist:          "",
			title:           file.Title,
			path:            filepath.ToSlash(rel),
		})
	}

	return entries, nil
}
