// Package namer renders filename templates into safe, relative paths.
//
// It performs simple, non-recursive token replacement (deliberately NOT
// text/template) and sanitizes every path component, so no field value can
// introduce a directory separator or traverse outside the destination tree.
// The package performs no I/O and never logs.
package namer

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrEmptyTemplate is returned by Render when the template, once its tokens are
// substituted, yields no usable path component.
var ErrEmptyTemplate = errors.New("namer: template produced an empty path")

const (
	separator  = "/"
	discPrefix = "Disc "
	singleDisc = 1
)

const (
	tokenAlbumArtist = "{albumartist}"
	tokenArtist      = "{artist}"
	tokenAlbum       = "{album}"
	tokenTitle       = "{title}"
	tokenTrack       = "{track}"
	tokenDisc        = "{disc}"
	tokenYear        = "{year}"
	tokenExt         = "{ext}"
	tokenISRC        = "{isrc}"
)

// TrackMeta carries the fields a template may reference. Every string field is
// a raw, untrusted value; Render sanitizes each one per path component.
type TrackMeta struct {
	AlbumArtist string // Album-level artist; the path roots here (handles Various Artists).
	Artist      string // Per-track artist.
	Album       string // Album title.
	Title       string // Track title.
	Track       int    // Track number within the disc; rendered padded to two digits.
	Disc        int    // Disc number; rendered padded to one digit.
	Year        string // Release year, e.g. "2021".
	Ext         string // File extension without a leading dot, e.g. "flac".
	ISRC        string // International Standard Recording Code.
	DiscCount   int    // Total discs in the album; greater than one inserts a "Disc N" directory.
}

// Render substitutes the template tokens with values from m and returns a
// slash-separated relative path. Each path component is independently
// sanitized, so a field value containing "/" or ".." can never escape the
// destination tree. When m.DiscCount is greater than one, a "Disc N"
// subdirectory is inserted ahead of the file name.
func Render(template string, m TrackMeta) (relPath string, err error) {
	replacer := newReplacer(m)
	segments := strings.Split(template, separator)
	components := make([]string, 0, len(segments)+1)
	for _, segment := range segments {
		substituted := replacer.Replace(segment)
		if substituted == "" {
			continue
		}
		components = append(components, Sanitize(substituted))
	}
	if len(components) == 0 {
		return "", ErrEmptyTemplate
	}
	if m.DiscCount > singleDisc {
		components = insertDiscDir(components, m.Disc)
	}
	return strings.Join(components, separator), nil
}

// newReplacer builds a single-pass token replacer for m. Track is padded to two
// digits and Disc to one; all other tokens substitute their raw field value.
func newReplacer(m TrackMeta) *strings.Replacer {
	return strings.NewReplacer(
		tokenAlbumArtist, m.AlbumArtist,
		tokenArtist, m.Artist,
		tokenAlbum, m.Album,
		tokenTitle, m.Title,
		tokenTrack, fmt.Sprintf("%02d", m.Track),
		tokenDisc, strconv.Itoa(m.Disc),
		tokenYear, m.Year,
		tokenExt, m.Ext,
		tokenISRC, m.ISRC,
	)
}

// insertDiscDir returns components with a sanitized "Disc N" directory inserted
// immediately before the final (file-name) component.
func insertDiscDir(components []string, disc int) []string {
	discDir := Sanitize(discPrefix + strconv.Itoa(disc))
	last := len(components) - 1
	result := make([]string, 0, len(components)+1)
	result = append(result, components[:last]...)
	result = append(result, discDir)
	result = append(result, components[last])
	return result
}
