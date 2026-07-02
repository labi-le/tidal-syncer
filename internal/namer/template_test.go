package namer_test

import (
	"errors"
	"testing"

	"github.com/labi-le/tidal-syncer/internal/namer"
)

// compileTemplate is the canonical library template used across the compiled
// template tests: an album-artist root, an album directory, and a
// track-numbered file name.
const compileTemplate = "{albumartist}/{album}/{track} {title}.{ext}"

// TestCompileRenderMatchesConcretePaths locks the compiled path exactly against
// the same outputs the free Render function produces, including the multi-disc
// directory insertion, path-traversal neutralization, and the lenient handling
// of unknown tokens and unmatched braces (which are copied verbatim).
func TestCompileRenderMatchesConcretePaths(t *testing.T) {
	cases := []struct {
		name string
		tmpl string
		meta namer.TrackMeta
		want string
	}{
		{
			name: "single disc",
			tmpl: compileTemplate,
			meta: namer.TrackMeta{
				AlbumArtist: "Pink Floyd", Artist: "Pink Floyd",
				Album: "Animals", Title: "Dogs",
				Track: 2, Disc: 1, Year: "1977",
				Ext: "flac", ISRC: "GBN9Y1100001", DiscCount: 1,
			},
			want: "Pink Floyd/Animals/02 Dogs.flac",
		},
		{
			name: "multi disc inserts disc directory",
			tmpl: compileTemplate,
			meta: namer.TrackMeta{
				AlbumArtist: "The Beatles", Artist: "The Beatles",
				Album: "The Beatles", Title: "Helter Skelter",
				Track: 8, Disc: 2, Year: "1968",
				Ext: "flac", ISRC: "GBAYE6800008", DiscCount: 2,
			},
			want: "The Beatles/The Beatles/Disc 2/08 Helter Skelter.flac",
		},
		{
			name: "separators in a field cannot escape the tree",
			tmpl: "{albumartist}/{album}/{title}.{ext}",
			meta: namer.TrackMeta{
				AlbumArtist: "Artist", Artist: "Artist",
				Album: "Album", Title: "../../../etc/passwd",
				Track: 1, Disc: 1, Year: "2020",
				Ext: "flac", ISRC: "", DiscCount: 1,
			},
			want: "Artist/Album/.._.._.._etc_passwd.flac",
		},
		{
			name: "unknown token is copied verbatim",
			tmpl: "{unknown}/{title}.{ext}",
			meta: namer.TrackMeta{Title: "Song", Ext: "flac", Track: 1, Disc: 1, DiscCount: 1},
			want: "{unknown}/Song.flac",
		},
		{
			name: "brace without a closing brace is literal",
			tmpl: "a{b/{title}.{ext}",
			meta: namer.TrackMeta{Title: "Song", Ext: "flac", Track: 1, Disc: 1, DiscCount: 1},
			want: "a{b/Song.flac",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := namer.Compile(tc.tmpl).Render(tc.meta)
			if err != nil {
				t.Fatalf("Compile(%q).Render() error = %v, want nil", tc.tmpl, err)
			}
			if got != tc.want {
				t.Errorf("Compile(%q).Render() = %q, want %q", tc.tmpl, got, tc.want)
			}
			free, freeErr := namer.Render(tc.tmpl, tc.meta)
			if freeErr != nil || free != got {
				t.Errorf("Render(%q) = %q, %v; want it to match Compile().Render() = %q",
					tc.tmpl, free, freeErr, got)
			}
		})
	}
}

// TestCompiledTemplateIsReusableAcrossTracks proves a template compiled once is
// safely reused for many tracks: rendering different metadata (including a
// switch between single- and multi-disc albums) through the same *Template
// yields each track's correct path with no state leaking between renders.
func TestCompiledTemplateIsReusableAcrossTracks(t *testing.T) {
	tmpl := namer.Compile(compileTemplate)
	cases := []struct {
		meta namer.TrackMeta
		want string
	}{
		{
			meta: namer.TrackMeta{
				AlbumArtist: "Pink Floyd", Artist: "Pink Floyd",
				Album: "Animals", Title: "Dogs",
				Track: 2, Disc: 1, Ext: "flac", DiscCount: 1,
			},
			want: "Pink Floyd/Animals/02 Dogs.flac",
		},
		{
			meta: namer.TrackMeta{
				AlbumArtist: "The Beatles", Artist: "The Beatles",
				Album: "The Beatles", Title: "Helter Skelter",
				Track: 8, Disc: 2, Ext: "flac", DiscCount: 2,
			},
			want: "The Beatles/The Beatles/Disc 2/08 Helter Skelter.flac",
		},
		{
			meta: namer.TrackMeta{
				AlbumArtist: "Artist", Artist: "Artist",
				Album: "Album", Title: "Third",
				Track: 3, Disc: 1, Ext: "flac", DiscCount: 1,
			},
			want: "Artist/Album/03 Third.flac",
		},
	}
	for _, tc := range cases {
		got, err := tmpl.Render(tc.meta)
		if err != nil {
			t.Fatalf("reused Render(%q) error = %v, want nil", tc.meta.Title, err)
		}
		if got != tc.want {
			t.Errorf("reused template Render() = %q, want %q", got, tc.want)
		}
	}
}

// TestCompiledTemplateRejectsEmptyResult confirms a template whose tokens
// substitute to nothing still surfaces ErrEmptyTemplate at render time, matching
// the free Render function's contract.
func TestCompiledTemplateRejectsEmptyResult(t *testing.T) {
	for _, tmpl := range []string{"", "///"} {
		if _, err := namer.Compile(tmpl).Render(namer.TrackMeta{}); !errors.Is(err, namer.ErrEmptyTemplate) {
			t.Errorf("Compile(%q).Render() error = %v, want ErrEmptyTemplate", tmpl, err)
		}
	}
}
