package namer_test

import (
	"strings"
	"testing"

	"github.com/labi-le/tidal-syncer/internal/namer"
)

// renderTemplate is the canonical library template both render benchmarks
// exercise: an album-artist root, an album directory, and a track-numbered file.
const renderTemplate = "{albumartist}/{album}/{track} {title}.{ext}"

type renderCase struct {
	name string
	meta namer.TrackMeta
}

// renderCases returns the single- and multi-disc inputs the render benchmarks
// share; the multi-disc case adds the extra "Disc N" component.
func renderCases() []renderCase {
	return []renderCase{
		{
			name: "single_disc",
			meta: namer.TrackMeta{
				AlbumArtist: "Pink Floyd", Artist: "Pink Floyd",
				Album: "Animals", Title: "Dogs",
				Track: 2, Disc: 1, Year: "1977",
				Ext: "flac", ISRC: "GBN9Y1100001", DiscCount: 1,
			},
		},
		{
			name: "multi_disc",
			meta: namer.TrackMeta{
				AlbumArtist: "The Beatles", Artist: "The Beatles",
				Album: "The Beatles", Title: "Helter Skelter",
				Track: 8, Disc: 2, Year: "1968",
				Ext: "flac", ISRC: "GBAYE6800008", DiscCount: 2,
			},
		},
	}
}

// BenchmarkRender measures the one-shot Render, which compiles the template and
// renders it on every call. It stands in for callers that render without
// caching a compiled template; hot-loop callers use BenchmarkTemplateRender.
func BenchmarkRender(b *testing.B) {
	for _, bc := range renderCases() {
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := namer.Render(renderTemplate, bc.meta); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkTemplateRender measures the hot-loop path the sync engine and
// playlist writer take: the template is compiled once, then Render runs per
// track. This isolates the per-track cost from the one-time parse.
func BenchmarkTemplateRender(b *testing.B) {
	compiled := namer.Compile(renderTemplate)
	for _, bc := range renderCases() {
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := compiled.Render(bc.meta); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkSanitize measures per-component sanitization across the input
// classes that exercise distinct code paths: an already-clean component, one
// dense with illegal characters, a decomposed-Unicode component that forces an
// NFC rewrite, a Windows reserved name, and a long multibyte component that
// trips the 255-byte truncation on a rune boundary.
func BenchmarkSanitize(b *testing.B) {
	const truncateRuneCount = 300
	cases := []struct {
		name  string
		input string
	}{
		{"clean", "Pink Floyd"},
		{"illegal_chars", `a/b\c:d*e?f"g<h>i|j`},
		{"decomposed_nfc", "Bjo\u0308rk Guðmundsdóttir"},
		{"reserved_name", "CON"},
		{"truncate_multibyte", strings.Repeat("\uac00", truncateRuneCount)},
	}
	for _, bc := range cases {
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = namer.Sanitize(bc.input)
			}
		})
	}
}
