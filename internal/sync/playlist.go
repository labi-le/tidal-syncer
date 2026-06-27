package sync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rs/zerolog"

	"github.com/labi-le/tidal-syncer/internal/config"
	"github.com/labi-le/tidal-syncer/internal/ctxlog"
	"github.com/labi-le/tidal-syncer/internal/namer"
	"github.com/labi-le/tidal-syncer/pkg/tidal"
)

const (
	// playlistsDirName is the subdirectory under the music root that holds exports.
	playlistsDirName = "Playlists"
	// playlistExt is the file extension of an exported playlist.
	playlistExt = ".m3u8"
	// partSuffix names the temporary file written before its atomic rename.
	partSuffix = ".part"
	// m3uHeader is the mandatory first line of an extended M3U playlist.
	m3uHeader = "#EXTM3U\n"
	// playlistDirMode is the permission mode for the created Playlists directory.
	playlistDirMode os.FileMode = 0o750
	// playlistFileMode is the permission mode for a written .m3u8 file.
	playlistFileMode os.FileMode = 0o600
	// opWritePlaylists scopes the logger for one playlist-export run.
	opWritePlaylists = "sync.WritePlaylists"
	// componentPlaylist labels every log line emitted by the writer.
	componentPlaylist = "playlist"
)

// PlaylistWriter exports each favorite TIDAL playlist as an .m3u8 file whose
// entries are paths, relative to the Playlists directory, of the exact audio
// files the engine writes for the playlist's tracks.
type PlaylistWriter struct {
	client TidalClient
	config config.Config
	logger zerolog.Logger
	albums map[string]tidal.Album
}

// NewPlaylistWriter builds a PlaylistWriter over client and cfg, tagging logger
// with the playlist component. The returned writer is single-run and not safe
// for concurrent use: its album cache is an unsynchronized map.
func NewPlaylistWriter(client TidalClient, cfg config.Config, logger zerolog.Logger) *PlaylistWriter {
	return &PlaylistWriter{
		client: client,
		config: cfg,
		logger: logger.With().Str("component", componentPlaylist).Logger(),
		albums: make(map[string]tidal.Album),
	}
}

// WritePlaylists writes one .m3u8 file per favorite playlist under
// <music>/Playlists, creating that directory first. It returns the first
// enumeration, metadata or filesystem error encountered.
func (w *PlaylistWriter) WritePlaylists(ctx context.Context) error {
	log := ctxlog.Op(w.logger, opWritePlaylists)
	dir := filepath.Join(w.config.Paths.Music, playlistsDirName)
	if err := os.MkdirAll(dir, playlistDirMode); err != nil {
		return fmt.Errorf("create playlists directory: %w", err)
	}

	for playlist, iterErr := range w.client.FavoritePlaylists(ctx) {
		if iterErr != nil {
			return fmt.Errorf("enumerate favorite playlists: %w", iterErr)
		}
		if err := w.writeOne(ctx, log, dir, playlist); err != nil {
			return err
		}
	}

	return nil
}

// writeOne renders playlist into a single .m3u8 file inside dir.
func (w *PlaylistWriter) writeOne(
	ctx context.Context, log zerolog.Logger, dir string, playlist tidal.Playlist,
) error {
	entries, err := w.entries(ctx, dir, playlist)
	if err != nil {
		return err
	}

	dest := filepath.Join(dir, namer.Sanitize(playlist.Title)+playlistExt)
	if err = writeM3U8(dest, entries); err != nil {
		return err
	}
	log.Debug().Str("playlist", playlist.Title).Int("tracks", len(entries)).Msg("playlist written")

	return nil
}

// playlistEntry is one resolved track in an exported playlist: the metadata for
// its #EXTINF line and the path to its audio file relative to the Playlists
// directory.
type playlistEntry struct {
	durationSeconds int
	artist          string
	title           string
	path            string
}

// entries resolves every track in playlist into a playlistEntry, preserving the
// playlist's track order. Each entry carries the track's #EXTINF metadata and
// the audio path relative to dir.
func (w *PlaylistWriter) entries(
	ctx context.Context, dir string, playlist tidal.Playlist,
) ([]playlistEntry, error) {
	items := make([]playlistEntry, 0, playlist.NumberOfTracks)
	for track, iterErr := range w.client.PlaylistTracks(ctx, playlist.UUID) {
		if iterErr != nil {
			return nil, fmt.Errorf("enumerate playlist %q tracks: %w", playlist.UUID, iterErr)
		}
		rel, err := w.trackPath(ctx, dir, track)
		if err != nil {
			return nil, err
		}
		items = append(items, playlistEntry{
			durationSeconds: track.Duration,
			artist:          primaryArtist(track.Artists),
			title:           track.Title,
			path:            rel,
		})
	}

	return items, nil
}

// trackPath returns the path of track's audio file relative to dir, rendered
// through the same template and album metadata the engine downloads it under.
func (w *PlaylistWriter) trackPath(ctx context.Context, dir string, track tidal.Track) (string, error) {
	album, err := w.albumByID(ctx, track.Album.ID)
	if err != nil {
		return "", err
	}

	rel, err := namer.Render(w.config.PathTemplate, buildTrackMeta(track, album))
	if err != nil {
		return "", fmt.Errorf("render path for track %d: %w", track.ID, err)
	}
	dest := filepath.Join(w.config.Paths.Music, filepath.FromSlash(rel))
	relative, err := filepath.Rel(dir, dest)
	if err != nil {
		return "", fmt.Errorf("relativize track %d path: %w", track.ID, err)
	}

	return filepath.ToSlash(relative), nil
}

// albumByID returns the full album for albumID, fetching it from the client at
// most once per run and memoizing the result.
func (w *PlaylistWriter) albumByID(ctx context.Context, albumID int) (tidal.Album, error) {
	id := strconv.Itoa(albumID)
	if album, ok := w.albums[id]; ok {
		return album, nil
	}

	album, err := w.client.Album(ctx, id)
	if err != nil {
		return tidal.Album{}, fmt.Errorf("fetch album %s: %w", id, err)
	}
	w.albums[id] = album

	return album, nil
}

// writeM3U8 writes the EXTM3U header followed by an #EXTINF metadata line and
// its path line for each entry to dest, staging through a .part file and
// renaming it into place atomically.
func writeM3U8(dest string, entries []playlistEntry) error {
	var buf strings.Builder
	buf.WriteString(m3uHeader)
	for _, entry := range entries {
		buf.WriteString(extinfLine(entry))
		buf.WriteByte('\n')
		buf.WriteString(entry.path)
		buf.WriteByte('\n')
	}

	tmp := dest + partSuffix
	if err := os.WriteFile(tmp, []byte(buf.String()), playlistFileMode); err != nil {
		return fmt.Errorf("write playlist %q: %w", dest, err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		return fmt.Errorf("finalize playlist %q: %w", dest, err)
	}

	return nil
}

// extinfLine renders entry's #EXTINF directive in the extended-M3U form
// "#EXTINF:<seconds>,<artist> - <title>".
func extinfLine(entry playlistEntry) string {
	return fmt.Sprintf("#EXTINF:%d,%s - %s", entry.durationSeconds, entry.artist, entry.title)
}
