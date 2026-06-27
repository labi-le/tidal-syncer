package tag

import (
	"strconv"

	"go.senan.xyz/taglib"
)

// coverMIMEType is the MIME type of the front-cover artwork TagFile embeds;
// callers supply the bytes already encoded as JPEG.
const coverMIMEType = "image/jpeg"

// Meta holds the textual metadata TagFile writes to a FLAC file's Vorbis
// comments. Every field maps to a standard Vorbis comment key. Lyrics are not a
// field here because TagFile accepts them separately: they may be embedded, or
// written as a sidecar, independently of these tags.
type Meta struct {
	Title       string // Track title (TITLE).
	Artist      string // Track artist (ARTIST).
	AlbumArtist string // Album-level artist (ALBUMARTIST); handles compilations.
	Album       string // Album title (ALBUM).
	TrackNumber int    // Track number within its disc (TRACKNUMBER).
	DiscNumber  int    // Disc number within the album (DISCNUMBER).
	Date        string // Release date or year, e.g. "2026" (DATE).
	Genre       string // Musical genre (GENRE).
	ISRC        string // International Standard Recording Code (ISRC).
	Copyright   string // Copyright / rights statement (COPYRIGHT).
}

// TagFile writes m as Vorbis comments to the FLAC at path and, when supplied,
// embeds coverJPEG as a PICTURE type-3 (front cover) and writes plainLyrics to
// the LYRICS comment. A nil or empty coverJPEG skips the picture, and an empty
// plainLyrics skips the lyrics comment; neither omission is fatal. Pre-existing
// comments are cleared so the file reflects exactly m (plus optional lyrics).
func TagFile(path string, m Meta, coverJPEG []byte, plainLyrics string) error { //nolint:revive // TagFile is the task-mandated exported API; the tag.TagFile stutter is intentional
	tags := map[string][]string{
		taglib.Title:       {m.Title},
		taglib.Artist:      {m.Artist},
		taglib.AlbumArtist: {m.AlbumArtist},
		taglib.Album:       {m.Album},
		taglib.TrackNumber: {strconv.Itoa(m.TrackNumber)},
		taglib.DiscNumber:  {strconv.Itoa(m.DiscNumber)},
		taglib.Date:        {m.Date},
		taglib.Genre:       {m.Genre},
		taglib.ISRC:        {m.ISRC},
		taglib.Copyright:   {m.Copyright},
	}
	if plainLyrics != "" {
		tags[taglib.Lyrics] = []string{plainLyrics}
	}

	if err := WriteTags(path, tags); err != nil {
		return err
	}

	if len(coverJPEG) == 0 {
		return nil
	}

	return WriteImage(path, coverJPEG, coverMIMEType)
}
