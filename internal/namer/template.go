package namer

import (
	"strconv"
	"strings"
)

// Template is a compiled path template. Compile parses the template's
// slash-separated segments and their "{token}" placeholders once; Render then
// substitutes per-track metadata against that plan without re-parsing, so an
// entire library renders under a single parse of the template.
type Template struct {
	segments []segment
}

// segment is one slash-delimited component of a template, decomposed into the
// ordered literal and token parts Render concatenates for each track. hint is a
// rendered-size estimate used to preallocate the builder for multi-part segments.
type segment struct {
	parts []part
	hint  int
}

// part is either a literal run of text (token == tokenNone) or a single metadata
// token substituted at render time.
type part struct {
	literal string
	token   tokenID
}

// tokenID identifies a known "{token}". tokenNone is the zero value and marks a
// literal part, so a bare part defaults to literal text.
type tokenID uint8

const (
	tokenNone tokenID = iota
	tokenIDAlbumArtist
	tokenIDArtist
	tokenIDAlbum
	tokenIDTitle
	tokenIDTrack
	tokenIDDisc
	tokenIDYear
	tokenIDExt
	tokenIDISRC
)

// tokenSizeHint is the assumed rendered width of one token, used only to
// preallocate a segment's builder; a low estimate costs at most one extra grow.
const tokenSizeHint = 8

// Compile parses template into a reusable plan. It splits the template on "/"
// once and decomposes each segment into its literal and token parts. Unknown
// "{tokens}" and unmatched braces are folded into literal text, exactly as the
// free Render function substitutes them, so Compile never fails; malformed
// templates are rejected earlier, at configuration load.
func Compile(template string) *Template {
	raw := strings.Split(template, separator)
	segments := make([]segment, len(raw))
	for i, seg := range raw {
		segments[i] = compileSegment(seg)
	}

	return &Template{segments: segments}
}

// Render substitutes the compiled template's tokens with values from m and
// returns a slash-separated relative path. Each component is independently
// sanitized, so a field value containing "/" or ".." can never escape the
// destination tree. When m.DiscCount is greater than one, a "Disc N"
// subdirectory is inserted ahead of the file name.
func (t *Template) Render(m TrackMeta) (string, error) {
	components := make([]string, 0, len(t.segments)+1)
	for i := range t.segments {
		expanded := t.segments[i].expand(m)
		if expanded == "" {
			continue
		}
		components = append(components, Sanitize(expanded))
	}
	if len(components) == 0 {
		return "", ErrEmptyTemplate
	}
	if m.DiscCount > singleDisc {
		components = insertDiscDir(components, m.Disc)
	}

	return strings.Join(components, separator), nil
}

// compileSegment decomposes one raw template segment into ordered literal and
// token parts. It scans left to right like the previous substitute pass: a "{"
// that opens a known token becomes a token part, while an unknown token or a
// stray "{" is copied into the literal run verbatim.
func compileSegment(raw string) segment {
	var (
		parts []part
		lit   strings.Builder
	)
	for i := 0; i < len(raw); {
		if raw[i] != tokenOpen {
			lit.WriteByte(raw[i])
			i++

			continue
		}
		id, width := tokenIDAt(raw[i:])
		if id == tokenNone {
			lit.WriteByte(tokenOpen)
			i++

			continue
		}
		if lit.Len() > 0 {
			parts = append(parts, part{literal: lit.String()})
			lit.Reset()
		}
		parts = append(parts, part{token: id})
		i += width
	}
	if lit.Len() > 0 {
		parts = append(parts, part{literal: lit.String()})
	}

	return segment{parts: parts, hint: segmentHint(parts)}
}

// segmentHint estimates a segment's rendered byte length to size its builder:
// literal parts contribute their exact length and each token a fixed guess.
func segmentHint(parts []part) int {
	hint := 0
	for _, p := range parts {
		if p.token == tokenNone {
			hint += len(p.literal)

			continue
		}
		hint += tokenSizeHint
	}

	return hint
}

// tokenIDAt reports the token opening s and its full byte width (braces
// included). It returns tokenNone with a zero width when s opens no closing
// brace or names no known token, matching the previous tokenAt lookup.
func tokenIDAt(s string) (tokenID, int) {
	end := strings.IndexByte(s, tokenClose)
	if end < 0 {
		return tokenNone, 0
	}
	id := tokenIDOf(s[:end+1])
	if id == tokenNone {
		return tokenNone, 0
	}

	return id, end + 1
}

// tokenIDOf maps a full "{token}" string to its tokenID, or tokenNone when the
// token is not recognized.
func tokenIDOf(token string) tokenID {
	switch token {
	case tokenAlbumArtist:
		return tokenIDAlbumArtist
	case tokenArtist:
		return tokenIDArtist
	case tokenAlbum:
		return tokenIDAlbum
	case tokenTitle:
		return tokenIDTitle
	case tokenTrack:
		return tokenIDTrack
	case tokenDisc:
		return tokenIDDisc
	case tokenYear:
		return tokenIDYear
	case tokenExt:
		return tokenIDExt
	case tokenISRC:
		return tokenIDISRC
	default:
		return tokenNone
	}
}

// expand concatenates the segment's parts against m. A single-part segment
// returns its literal or token value directly with no allocation; a multi-part
// segment builds the result in one preallocated pass.
func (s segment) expand(m TrackMeta) string {
	switch len(s.parts) {
	case 0:
		return ""
	case 1:
		return s.parts[0].value(m)
	default:
		var b strings.Builder
		b.Grow(s.hint)
		for _, p := range s.parts {
			b.WriteString(p.value(m))
		}

		return b.String()
	}
}

// value returns the part's literal text, or the substituted metadata value when
// the part is a token.
func (p part) value(m TrackMeta) string {
	if p.token == tokenNone {
		return p.literal
	}

	return tokenValueByID(p.token, m)
}

// tokenValueByID returns the metadata value for a token, padding Track to two
// digits and formatting Disc. tokenNone and any unknown id yield the empty
// string; value never calls it with tokenNone.
func tokenValueByID(id tokenID, m TrackMeta) string {
	switch id {
	case tokenIDAlbumArtist:
		return m.AlbumArtist
	case tokenIDArtist:
		return m.Artist
	case tokenIDAlbum:
		return m.Album
	case tokenIDTitle:
		return m.Title
	case tokenIDTrack:
		return padTrack(m.Track)
	case tokenIDDisc:
		return strconv.Itoa(m.Disc)
	case tokenIDYear:
		return m.Year
	case tokenIDExt:
		return m.Ext
	case tokenIDISRC:
		return m.ISRC
	case tokenNone:
		return ""
	}

	return ""
}
