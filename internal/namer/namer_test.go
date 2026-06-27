package namer_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/labi-le/tidal-syncer/internal/namer"
)

// TestSanitize verifies the per-component sanitization rules: illegal and
// control characters are replaced, Windows reserved device names are suffixed,
// trailing dots/spaces are stripped, decomposed Unicode is folded to NFC, and
// an otherwise-empty result collapses to a single placeholder.
func TestSanitize(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"replaces all illegal path characters", `a/b\c:d*e?f"g<h>i|j`, "a_b_c_d_e_f_g_h_i_j"},
		{"replaces control characters", "a\x00b\x1fc\x7fd", "a_b_c_d"},
		{"suffixes reserved CON uppercase", "CON", "CON_"},
		{"suffixes reserved con lowercase", "con", "con_"},
		{"suffixes reserved mixed-case Aux", "Aux", "Aux_"},
		{"suffixes reserved COM1", "COM1", "COM1_"},
		{"suffixes reserved LPT9", "LPT9", "LPT9_"},
		{"leaves COM0 untouched", "COM0", "COM0"},
		{"leaves CONSOLE untouched", "CONSOLE", "CONSOLE"},
		{"suffixes reserved NUL with extension", "NUL.txt", "NUL_.txt"},
		{"treats trailing-dot reserved name as reserved", "CON.", "CON_"},
		{"keeps reserved word inside longer name", "01 CON.flac", "01 CON.flac"},
		{"strips trailing dots", "name...", "name"},
		{"strips trailing spaces", "name   ", "name"},
		{"normalizes decomposed form to NFC", "e\u0301", "\u00e9"},
		{"maps dot-dot to placeholder", "..", "_"},
		{"maps blank input to placeholder", "   ", "_"},
		{"maps empty input to placeholder", "", "_"},
		{"neutralizes an embedded separator value", "../etc", ".._etc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := namer.Sanitize(tt.input); got != tt.want {
				t.Errorf("Sanitize(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestSanitizeTruncatesToByteLimitOnRuneBoundary proves the 255-byte cap is
// enforced on a rune boundary, not a rune count: a 300-glyph multibyte title
// must shrink to a valid UTF-8 string of at most 255 bytes.
func TestSanitizeTruncatesToByteLimitOnRuneBoundary(t *testing.T) {
	const (
		maxBytes      = 255
		syllableBytes = 3
		runeCount     = 300
	)
	// Given: 300 Hangul syllables, each 3 bytes and unchanged by NFC.
	syllable := "\uac00"
	input := strings.Repeat(syllable, runeCount)
	if len(input) != runeCount*syllableBytes {
		t.Fatalf("setup: len(input) = %d bytes, want %d", len(input), runeCount*syllableBytes)
	}

	// When
	got := namer.Sanitize(input)

	// Then: byte-bounded, valid UTF-8, never split mid-rune.
	if len(got) > maxBytes {
		t.Errorf("len(got) = %d bytes, want <= %d", len(got), maxBytes)
	}
	if !utf8.ValidString(got) {
		t.Errorf("got is not valid UTF-8: %q", got)
	}
	wantRunes := maxBytes / syllableBytes
	if n := utf8.RuneCountInString(got); n != wantRunes {
		t.Errorf("rune count = %d, want %d (byte-bounded, not rune-bounded)", n, wantRunes)
	}
	if len(got) != wantRunes*syllableBytes {
		t.Errorf("len(got) = %d bytes, want %d", len(got), wantRunes*syllableBytes)
	}
}

// TestRender checks template token replacement, track/disc padding, the
// album-artist root (Various Artists compilations land under the album artist,
// not the per-track artist), and the multi-disc directory prefix.
func TestRender(t *testing.T) {
	const template = "{albumartist}/{album}/{track} {title}.{ext}"
	tests := []struct {
		name string
		meta namer.TrackMeta
		want string
	}{
		{
			name: "renders a basic single-disc track",
			meta: namer.TrackMeta{
				AlbumArtist: "Pink Floyd", Artist: "Pink Floyd",
				Album: "Animals", Title: "Dogs",
				Track: 2, Disc: 1, Year: "1977",
				Ext: "flac", ISRC: "GBN9Y1100001", DiscCount: 1,
			},
			want: "Pink Floyd/Animals/02 Dogs.flac",
		},
		{
			name: "files a various-artists track under the album artist",
			meta: namer.TrackMeta{
				AlbumArtist: "Various Artists", Artist: "Aphex Twin",
				Album: "Warp10", Title: "Polynomial-C",
				Track: 3, Disc: 1, Year: "1999",
				Ext: "flac", ISRC: "GBABC9900003", DiscCount: 1,
			},
			want: "Various Artists/Warp10/03 Polynomial-C.flac",
		},
		{
			name: "inserts a disc directory for multi-disc albums",
			meta: namer.TrackMeta{
				AlbumArtist: "The Beatles", Artist: "The Beatles",
				Album: "The Beatles", Title: "Helter Skelter",
				Track: 8, Disc: 2, Year: "1968",
				Ext: "flac", ISRC: "GBAYE6800008", DiscCount: 2,
			},
			want: "The Beatles/The Beatles/Disc 2/08 Helter Skelter.flac",
		},
		{
			name: "pads the track and leaves a single disc unpadded",
			meta: namer.TrackMeta{
				AlbumArtist: "X", Artist: "X", Album: "Y", Title: "Z",
				Track: 1, Disc: 1, Year: "2000",
				Ext: "mp3", ISRC: "", DiscCount: 1,
			},
			want: "X/Y/01 Z.mp3",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := namer.Render(template, tt.meta)
			if err != nil {
				t.Fatalf("Render() error = %v, want nil", err)
			}
			if got != tt.want {
				t.Errorf("Render() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestRenderPreventsPathTraversal proves a malicious field value can never
// introduce a directory separator or a ".." component that escapes the
// destination tree: separators are neutralized and a bare ".." collapses.
func TestRenderPreventsPathTraversal(t *testing.T) {
	const (
		template = "{albumartist}/{album}/{title}.{ext}"
		base     = "/music/library"
	)
	tests := []struct {
		name string
		meta namer.TrackMeta
		want string
	}{
		{
			name: "neutralizes separators embedded in a title",
			meta: namer.TrackMeta{
				AlbumArtist: "Artist", Artist: "Artist",
				Album: "Album", Title: "../../../etc/passwd",
				Track: 1, Disc: 1, Year: "2020",
				Ext: "flac", ISRC: "", DiscCount: 1,
			},
			want: "Artist/Album/.._.._.._etc_passwd.flac",
		},
		{
			name: "collapses a dot-dot field value to a placeholder",
			meta: namer.TrackMeta{
				AlbumArtist: "Artist", Artist: "Artist",
				Album: "..", Title: "Song",
				Track: 1, Disc: 1, Year: "2020",
				Ext: "flac", ISRC: "", DiscCount: 1,
			},
			want: "Artist/_/Song.flac",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := namer.Render(template, tt.meta)
			if err != nil {
				t.Fatalf("Render() error = %v, want nil", err)
			}
			if got != tt.want {
				t.Errorf("Render() = %q, want %q", got, tt.want)
			}
			for comp := range strings.SplitSeq(got, "/") {
				if comp == ".." || comp == "." {
					t.Errorf("Render() produced traversal component %q in %q", comp, got)
				}
			}
			joined := filepath.Clean(filepath.Join(base, got))
			if !strings.HasPrefix(joined, base+"/") {
				t.Errorf("rendered path escaped base: Join(%q, %q) = %q", base, got, joined)
			}
		})
	}
}

// TestRenderReturnsErrorForEmptyTemplate confirms a template that yields no
// usable component reports ErrEmptyTemplate rather than an empty path.
func TestRenderReturnsErrorForEmptyTemplate(t *testing.T) {
	tests := []struct {
		name     string
		template string
	}{
		{"empty string", ""},
		{"only separators", "///"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := namer.Render(tt.template, namer.TrackMeta{})
			if !errors.Is(err, namer.ErrEmptyTemplate) {
				t.Errorf("Render(%q) error = %v, want ErrEmptyTemplate", tt.template, err)
			}
		})
	}
}
