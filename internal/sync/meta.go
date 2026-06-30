package sync

import (
	"strings"

	"github.com/labi-le/tidal-syncer/internal/namer"
	"github.com/labi-le/tidal-syncer/internal/tag"
	"github.com/labi-le/tidal-syncer/pkg/tidal"
)

// artistTypeMain marks the primary (non-featured) artist credit.
const artistTypeMain = "MAIN"

// fileExtension is the container extension every downloaded track is written as.
const fileExtension = "flac"

// dateSeparator splits an ISO-8601 release date into its components.
const dateSeparator = "-"

// buildTrackMeta projects a track and its album onto the path-template fields.
func buildTrackMeta(track tidal.Track, album tidal.Album) namer.TrackMeta {
	return namer.TrackMeta{
		AlbumArtist: primaryArtist(album.Artists),
		Artist:      primaryArtist(track.Artists),
		Album:       album.Title,
		Title:       track.Title,
		Track:       track.TrackNumber,
		Disc:        track.VolumeNumber,
		Year:        yearFromDate(album.ReleaseDate),
		Ext:         fileExtension,
		ISRC:        track.ISRC,
		DiscCount:   album.NumberOfVolumes,
	}
}

// buildTagMeta projects a track, its album and its genres onto the
// Vorbis-comment fields.
func buildTagMeta(track tidal.Track, album tidal.Album, genres []string) tag.Meta {
	return tag.Meta{
		Title:       track.Title,
		Artist:      primaryArtist(track.Artists),
		AlbumArtist: primaryArtist(album.Artists),
		Album:       album.Title,
		TrackNumber: track.TrackNumber,
		DiscNumber:  track.VolumeNumber,
		Date:        album.ReleaseDate,
		Genre:       genres,
		ISRC:        track.ISRC,
		Copyright:   track.Copyright,
	}
}

// primaryArtist returns the name of the MAIN artist, falling back to the first
// credited artist and finally to the empty string when none are present.
func primaryArtist(artists []tidal.Artist) string {
	for _, artist := range artists {
		if artist.Type == artistTypeMain {
			return artist.Name
		}
	}
	if len(artists) > 0 {
		return artists[0].Name
	}

	return ""
}

// yearFromDate extracts the leading year from an ISO-8601 release date.
func yearFromDate(date string) string {
	year, _, _ := strings.Cut(date, dateSeparator)

	return year
}
