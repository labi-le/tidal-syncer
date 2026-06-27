package sync

import (
	"strings"

	"github.com/labi-le/tidal-syncer/internal/namer"
	"github.com/labi-le/tidal-syncer/internal/tag"
	"github.com/labi-le/tidal-syncer/pkg/tidal"
)

// Audio quality tier identifiers, highest to lowest, as advertised by TIDAL.
const (
	qualityHiResLossless = "HI_RES_LOSSLESS"
	qualityLossless      = "LOSSLESS"
	qualityHigh          = "HIGH"
	qualityLow           = "LOW"
)

// Comparable ranks for the quality tiers; a higher rank is a better tier. An
// unrecognized tier ranks lowest so it never satisfies a skip check.
const (
	rankUnknown  = 0
	rankLow      = 1
	rankHigh     = 2
	rankLossless = 3
	rankHiRes    = 4
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

// buildTagMeta projects a track and its album onto the Vorbis-comment fields.
func buildTagMeta(track tidal.Track, album tidal.Album) tag.Meta {
	return tag.Meta{
		Title:       track.Title,
		Artist:      primaryArtist(track.Artists),
		AlbumArtist: primaryArtist(album.Artists),
		Album:       album.Title,
		TrackNumber: track.TrackNumber,
		DiscNumber:  track.VolumeNumber,
		Date:        album.ReleaseDate,
		Genre:       "",
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

// qualityRank maps a quality tier identifier to its comparable rank.
func qualityRank(quality string) int {
	switch quality {
	case qualityHiResLossless:
		return rankHiRes
	case qualityLossless:
		return rankLossless
	case qualityHigh:
		return rankHigh
	case qualityLow:
		return rankLow
	default:
		return rankUnknown
	}
}

// yearFromDate extracts the leading year from an ISO-8601 release date.
func yearFromDate(date string) string {
	year, _, _ := strings.Cut(date, dateSeparator)

	return year
}
