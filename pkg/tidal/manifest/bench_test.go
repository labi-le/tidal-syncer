package manifest_test

import (
	"testing"

	"github.com/labi-le/tidal-syncer/pkg/tidal/manifest"
)

// BenchmarkParse measures manifest decoding for both wire formats: BTS
// (base64 + JSON) and DASH (base64 + XML), each decoding its representative
// fixture from scratch on every call.
func BenchmarkParse(b *testing.B) {
	cases := []struct {
		name    string
		mime    string
		fixture string
	}{
		{"bts", manifest.MimeBTS, "bts.manifest.b64"},
		{"dash", manifest.MimeDASH, "dash.manifest.b64"},
	}
	for _, bc := range cases {
		b.Run(bc.name, func(b *testing.B) {
			encoded := loadFixture(b, bc.fixture)
			b.ReportAllocs()
			for b.Loop() {
				if _, err := manifest.Parse(bc.mime, encoded); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkSegmentURLs measures the DASH segment-URL builder in isolation: the
// manifest is parsed once, then the ordered init+media URL list is rebuilt on
// each call (one strings.ReplaceAll + strconv.Itoa per media segment).
func BenchmarkSegmentURLs(b *testing.B) {
	encoded := loadFixture(b, "dash.manifest.b64")
	m, err := manifest.Parse(manifest.MimeDASH, encoded)
	if err != nil {
		b.Fatalf("setup: parse dash: %v", err)
	}
	dash, ok := m.DASH()
	if !ok {
		b.Fatal("setup: DASH() ok = false, want true")
	}
	b.ReportAllocs()
	for b.Loop() {
		_ = dash.SegmentURLs()
	}
}
