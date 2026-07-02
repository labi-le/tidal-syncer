package tag_test

import (
	"testing"

	"github.com/labi-le/tidal-syncer/internal/tag"
)

// BenchmarkIntegrityCheck measures a full FLAC integrity pass: every audio
// frame is decoded and CRC-verified. The fixture is copied once during setup
// (untimed); the read-only check then runs against that file each iteration.
func BenchmarkIntegrityCheck(b *testing.B) {
	path := makeCopy(b, b.TempDir(), "integrity.flac")

	b.ReportAllocs()
	for b.Loop() {
		if err := tag.IntegrityCheck(path); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkWriteTags measures rewriting the full Vorbis comment set (Clear then
// write) to a FLAC on each call.
func BenchmarkWriteTags(b *testing.B) {
	path := makeCopy(b, b.TempDir(), "writetags.flac")
	tags := wantTags()

	b.ReportAllocs()
	for b.Loop() {
		if err := tag.WriteTags(path, tags); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkWriteImage measures embedding a JPEG front cover (PICTURE type-3) at
// index 0 on each call.
func BenchmarkWriteImage(b *testing.B) {
	path := makeCopy(b, b.TempDir(), "writeimage.flac")
	cover := makeJPEG(b)

	b.ReportAllocs()
	for b.Loop() {
		if err := tag.WriteImage(path, cover, coverMIME); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkTagFile measures the full per-track tag pipeline (WriteTags +
// WriteImage) the sync engine runs after a download completes.
func BenchmarkTagFile(b *testing.B) {
	path := makeCopy(b, b.TempDir(), "tagfile.flac")
	cover := makeJPEG(b)
	meta := pipelineMeta()

	b.ReportAllocs()
	for b.Loop() {
		if err := tag.TagFile(path, meta, cover, pipelinePlainLyrics); err != nil {
			b.Fatal(err)
		}
	}
}
