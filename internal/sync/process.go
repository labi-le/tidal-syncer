package sync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rs/zerolog"

	"github.com/labi-le/tidal-syncer/internal/ctxlog"
	"github.com/labi-le/tidal-syncer/internal/namer"
	"github.com/labi-le/tidal-syncer/internal/store"
	"github.com/labi-le/tidal-syncer/internal/tag"
	"github.com/labi-le/tidal-syncer/pkg/tidal"
)

const (
	// musicDirMode is the permission mode applied to every directory created under
	// the music root: destination track directories and the Playlists export
	// directory alike. 0o755 keeps the music library world-traversable so the host
	// user and media servers (Jellyfin, Navidrome, ...) can read it regardless of
	// the container UID.
	musicDirMode os.FileMode = 0o755
	// opProcessTrack scopes the logger for the per-track pipeline.
	opProcessTrack = "sync.processTrack"
	// genreSeparator joins a track's genres into the single store.Track.Genre
	// column for SQL querying; the FLAC file keeps each genre as its own comment.
	genreSeparator = ";"
)

// processTrack runs the full per-track pipeline, recording the outcome in the
// counters. A per-track failure is recorded and swallowed so the run continues.
func (e *Engine) processTrack(ctx context.Context, track tidal.Track, c *counters) {
	log := ctxlog.Op(e.logger, opProcessTrack)

	skip, err := e.shouldSkip(ctx, track)
	if err != nil {
		e.markFailed(ctx, log, track, err, c)

		return
	}
	if skip {
		log.Debug().Int("track", track.ID).Str("title", track.Title).Msg("skipped: already present")
		c.skipped.Add(1)

		return
	}

	if err = e.downloadOne(ctx, log, track); err != nil {
		e.markFailed(ctx, log, track, err, c)

		return
	}
	c.downloaded.Add(1)
}

// shouldSkip reports whether track is already stored as done having been
// requested at a tier at least as high as the one currently configured. It
// compares the requested tier, not the obtained one, so a track whose best
// available master is below the requested tier is downloaded once and then
// skipped rather than re-downloaded every cycle; raising the requested tier
// re-attempts it. A missing record is not a skip.
func (e *Engine) shouldSkip(ctx context.Context, track tidal.Track) (bool, error) {
	record, err := e.store.GetTrack(ctx, strconv.Itoa(track.ID))
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("look up track %d: %w", track.ID, err)
	}
	if record.Status != store.StatusDone {
		return false, nil
	}

	return tidal.Quality(record.RequestedQuality).Rank() >= e.config.Quality.Request.Rank(), nil
}

// downloadOne resolves metadata, downloads, integrity-checks, tags and records a
// single track. Any step's failure aborts only this track.
func (e *Engine) downloadOne(ctx context.Context, log zerolog.Logger, track tidal.Track) error {
	album, cover, err := e.resolveAlbum(ctx, log, track)
	if err != nil {
		return err
	}
	lyrics := e.fetchLyrics(ctx, log, track)
	genres := e.fetchGenres(ctx, log, track)

	rel, err := namer.Render(e.config.PathTemplate, buildTrackMeta(track, album))
	if err != nil {
		return fmt.Errorf("render path for track %d: %w", track.ID, err)
	}
	dest := filepath.Join(e.config.Paths.Music, filepath.FromSlash(rel))
	if err = os.MkdirAll(filepath.Dir(dest), musicDirMode); err != nil {
		return fmt.Errorf("create directory for track %d: %w", track.ID, err)
	}

	artist := ""
	if len(track.Artists) > 0 {
		artist = track.Artists[0].Name
	}
	log.Debug().
		Int("track", track.ID).
		Str("title", track.Title).
		Str("artist", artist).
		Str("album", album.Title).
		Str("requested", string(e.config.Quality.Request)).
		Msg("downloading")

	quality, err := e.downloader.Download(ctx, strconv.Itoa(track.ID), dest)
	if err != nil {
		return fmt.Errorf("download track %d: %w", track.ID, err)
	}
	if err = tag.IntegrityCheck(dest); err != nil {
		return fmt.Errorf("verify track %d: %w", track.ID, err)
	}
	if err = e.writeTags(dest, track, album, trackTags{cover: cover, lyrics: lyrics, genres: genres}); err != nil {
		return err
	}

	if err = e.markDone(ctx, track, dest, quality, genres); err != nil {
		return err
	}
	log.Debug().
		Int("track", track.ID).
		Str("title", track.Title).
		Str("quality", string(quality)).
		Msg("downloaded")

	return nil
}

// resolveAlbum returns track's album and cover from the per-run cache, fetching
// them at most once per album id.
func (e *Engine) resolveAlbum(
	ctx context.Context, log zerolog.Logger, track tidal.Track,
) (tidal.Album, []byte, error) {
	id := strconv.Itoa(track.Album.ID)

	return e.albums.load(id, func() (tidal.Album, []byte, error) {
		album, err := e.client.Album(ctx, id)
		if err != nil {
			return tidal.Album{}, nil, fmt.Errorf("fetch album %s: %w", id, err)
		}

		return album, e.fetchCover(ctx, log, album), nil
	})
}

// fetchCover retrieves album artwork best-effort: a missing or failed cover
// yields nil rather than failing the track.
func (e *Engine) fetchCover(ctx context.Context, log zerolog.Logger, album tidal.Album) []byte {
	if album.Cover == "" {
		return nil
	}
	data, err := e.covers.Cover(ctx, album.Cover)
	if err != nil {
		log.Warn().Err(err).Str("cover", album.Cover).Msg("cover fetch failed")

		return nil
	}

	return data
}

// fetchLyrics retrieves lyrics best-effort when either lyrics output is enabled;
// a fetch error yields empty lyrics rather than failing the track.
func (e *Engine) fetchLyrics(ctx context.Context, log zerolog.Logger, track tidal.Track) tidal.Lyrics {
	if !e.config.Lyrics.Embed && !e.config.Lyrics.Sidecar {
		return tidal.Lyrics{}
	}
	lyrics, err := e.client.Lyrics(ctx, strconv.Itoa(track.ID))
	if err != nil {
		log.Debug().Err(err).Int("track", track.ID).Msg("lyrics fetch failed")

		return tidal.Lyrics{}
	}

	return lyrics
}

// fetchGenres retrieves a track's genres best-effort. The v1 API does not expose
// genre, so this hits the v2 catalog; a fetch error yields no genres rather than
// failing the track.
func (e *Engine) fetchGenres(ctx context.Context, log zerolog.Logger, track tidal.Track) []string {
	genres, err := e.client.TrackGenres(ctx, strconv.Itoa(track.ID))
	if err != nil {
		log.Debug().Err(err).Int("track", track.ID).Msg("genre fetch failed")

		return nil
	}

	return genres
}

// trackTags bundles the optional, best-effort metadata written alongside a
// downloaded track: cover artwork, lyrics, and genres.
type trackTags struct {
	cover  []byte
	lyrics tidal.Lyrics
	genres []string
}

// writeTags writes Vorbis comments and, per configuration, embeds plain lyrics
// and writes an LRC sidecar.
func (e *Engine) writeTags(dest string, track tidal.Track, album tidal.Album, tags trackTags) error {
	plain := ""
	if e.config.Lyrics.Embed {
		plain = tags.lyrics.Plain
	}
	if err := tag.TagFile(dest, buildTagMeta(track, album, tags.genres), tags.cover, plain); err != nil {
		return fmt.Errorf("tag track %d: %w", track.ID, err)
	}
	if e.config.Lyrics.Sidecar && tags.lyrics.LRC != "" {
		if err := tag.WriteLRC(dest, tags.lyrics.LRC); err != nil {
			return fmt.Errorf("write lyrics for track %d: %w", track.ID, err)
		}
	}

	return nil
}

// markDone records a successful download in the store.
func (e *Engine) markDone(
	ctx context.Context, track tidal.Track, dest string, quality tidal.Quality, genres []string,
) error {
	record := store.Track{
		TidalID:          strconv.Itoa(track.ID),
		ISRC:             track.ISRC,
		AlbumID:          strconv.Itoa(track.Album.ID),
		Path:             dest,
		ObtainedQuality:  string(quality),
		RequestedQuality: string(e.config.Quality.Request),
		Genre:            strings.Join(genres, genreSeparator),
		Status:           store.StatusDone,
		UpdatedAt:        0,
	}
	if err := e.store.MarkTrack(ctx, record); err != nil {
		return fmt.Errorf("record track %d: %w", track.ID, err)
	}

	return nil
}

// markFailed counts and records a failed track, logging but never propagating a
// secondary store error so the run is never aborted by one track.
func (e *Engine) markFailed(
	ctx context.Context, log zerolog.Logger, track tidal.Track, cause error, c *counters,
) {
	c.failed.Add(1)
	log.Error().Err(cause).Int("track", track.ID).Msg("track failed")

	record := store.Track{
		TidalID:         strconv.Itoa(track.ID),
		ISRC:            track.ISRC,
		AlbumID:         strconv.Itoa(track.Album.ID),
		Path:            "",
		ObtainedQuality: "",
		Status:          store.StatusFailed,
		UpdatedAt:       0,
	}
	if err := e.store.MarkTrack(ctx, record); err != nil {
		log.Error().Err(err).Int("track", track.ID).Msg("record failed state")
	}
}
