// Package namer renders filename templates into safe, relative paths.
//
// It performs simple, non-recursive token replacement (deliberately NOT
// text/template) and sanitizes every path component, so no field value can
// introduce a directory separator or traverse outside the destination tree.
// The package performs no I/O and never logs.
package namer

import (
	"errors"
	"strconv"
)

// ErrEmptyTemplate is returned by Render when the template, once its tokens are
// substituted, yields no usable path component.
var ErrEmptyTemplate = errors.New("namer: template produced an empty path")

const (
	separator  = "/"
	discPrefix = "Disc "
	singleDisc = 1
	// trackPadWidth is the minimum digit count for a rendered track number,
	// matching the previous "%02d" formatting (single digits gain a leading 0).
	trackPadWidth      = 2
	tokenOpen     byte = '{'
	tokenClose    byte = '}'
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

// Render compiles template and renders it against m in a single call. Callers
// in a hot loop that reuse one template should Compile it once and call
// Template.Render per track, which parses the template only once instead of on
// every render.
func Render(template string, m TrackMeta) (string, error) {
	return Compile(template).Render(m)
}

// padTrack renders n zero-padded to trackPadWidth digits, matching the previous
// fmt "%02d" output without its reflection and formatting cost.
func padTrack(n int) string {
	s := strconv.Itoa(n)
	if len(s) < trackPadWidth {
		return "0" + s
	}
	return s
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
