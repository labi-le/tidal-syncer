package tag_test

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"go.senan.xyz/taglib"
	"golang.org/x/sync/errgroup"

	"github.com/labi-le/tidal-syncer/internal/tag"
)

const (
	fixtureFLAC      = "testdata/sample.flac"
	coverMIME        = "image/jpeg"
	coverDim         = 64
	wantImageCount   = 1
	concurrentCopies = 8
	perfCopies       = 100
)

// wantTags is the canonical Vorbis comment set the helpers write and expect to
// read back byte-for-byte: every tag field the production pipeline populates,
// including multi-line Lyrics and a UTF-8 Copyright to exercise non-ASCII
// round-tripping.
func wantTags() map[string][]string {
	return map[string][]string{
		taglib.Title:       {"Test Title"},
		taglib.Artist:      {"Test Artist"},
		taglib.AlbumArtist: {"Test Album Artist"},
		taglib.Album:       {"Test Album"},
		taglib.TrackNumber: {"7"},
		taglib.DiscNumber:  {"1"},
		taglib.Date:        {"2026"},
		taglib.Genre:       {"Ambient"},
		taglib.Lyrics:      {"first line\nsecond line"},
		taglib.ISRC:        {"USS1Z9900001"},
		taglib.Copyright:   {"© 2026 Test Records"},
	}
}

// eqStr adapts slices.Equal to the signature maps.EqualFunc expects.
func eqStr(a, b []string) bool {
	return slices.Equal(a, b)
}

// makeJPEG renders a tiny valid JPEG cover entirely in memory so the only
// committed binary fixture is the FLAC file.
func makeJPEG(t *testing.T) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, coverDim, coverDim))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode jpeg cover: %v", err)
	}

	return buf.Bytes()
}

// makeCopy duplicates the committed FLAC fixture into dir under name and returns
// the new path, so each test mutates its own throwaway file.
func makeCopy(t *testing.T, dir, name string) string {
	t.Helper()

	src, err := os.ReadFile(fixtureFLAC)
	if err != nil {
		t.Fatalf("read fixture %s: %v", fixtureFLAC, err)
	}

	dst := filepath.Join(dir, name)
	if err = os.WriteFile(dst, src, 0o600); err != nil {
		t.Fatalf("write copy %s: %v", dst, err)
	}

	return dst
}

// verifyFile asserts a FLAC at path carries exactly want tags, the exact cover
// bytes, and remains a structurally valid FLAC (ReadProperties succeeds).
func verifyFile(t *testing.T, path string, want map[string][]string, cover []byte) {
	t.Helper()

	got, err := tag.ReadTags(path)
	if err != nil {
		t.Fatalf("read tags %s: %v", path, err)
	}
	if !maps.EqualFunc(want, got, eqStr) {
		t.Fatalf("%s tags mismatch:\n want=%v\n got =%v", path, want, got)
	}

	gotCover, err := tag.ReadImage(path)
	if err != nil {
		t.Fatalf("read image %s: %v", path, err)
	}
	if !bytes.Equal(cover, gotCover) {
		t.Fatalf("%s cover mismatch: wrote %d bytes, read %d bytes", path, len(cover), len(gotCover))
	}

	props, err := taglib.ReadProperties(path)
	if err != nil {
		t.Fatalf("%s not a valid FLAC after write: %v", path, err)
	}
	assertFrontCover(t, path, props)
}

// assertFrontCover proves the embedded picture is exactly one PICTURE type-3
// front cover with the JPEG MIME type we asked for.
func assertFrontCover(t *testing.T, path string, props taglib.Properties) {
	t.Helper()

	if len(props.Images) != wantImageCount {
		t.Fatalf("%s: want %d embedded image, got %d", path, wantImageCount, len(props.Images))
	}
	if props.Images[0].Type != tag.FrontCoverType {
		t.Fatalf("%s: want picture type %q (PICTURE type 3), got %q",
			path, tag.FrontCoverType, props.Images[0].Type)
	}
	if props.Images[0].MIMEType != coverMIME {
		t.Fatalf("%s: want cover MIME %q, got %q", path, coverMIME, props.Images[0].MIMEType)
	}
}

// TestHelpersRoundTripTagsAndCover proves the core taglib contract: writing
// every Vorbis field plus a JPEG front cover and reading them back yields
// exactly what was written.
func TestHelpersRoundTripTagsAndCover(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := makeCopy(t, dir, "roundtrip.flac")
	want := wantTags()
	cover := makeJPEG(t)

	if err := tag.WriteTags(path, want); err != nil {
		t.Fatalf("write tags: %v", err)
	}
	if err := tag.WriteImage(path, cover, coverMIME); err != nil {
		t.Fatalf("write image: %v", err)
	}

	verifyFile(t, path, want, cover)
}

// TestConcurrentWritersProduceValidFiles proves taglib is safe under concurrent
// writers: tagging 8 independent copies in parallel leaves every file
// individually correct and uncorrupted. Run under -race this also proves no
// data race in the wrapper.
func TestConcurrentWritersProduceValidFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	want := wantTags()
	cover := makeJPEG(t)

	paths := make([]string, concurrentCopies)
	for i := range paths {
		paths[i] = makeCopy(t, dir, fmt.Sprintf("concurrent-%d.flac", i))
	}

	var grp errgroup.Group
	for _, p := range paths {
		grp.Go(func() error {
			if err := tag.WriteTags(p, want); err != nil {
				return err
			}

			return tag.WriteImage(p, cover, coverMIME)
		})
	}
	if err := grp.Wait(); err != nil {
		t.Fatalf("concurrent tagging failed: %v", err)
	}

	for _, p := range paths {
		verifyFile(t, p, want, cover)
	}
}

// TestConcurrentWritePerf measures the steady-state cost of the write pipeline
// (WriteTags + WriteImage) across 100 fresh copies and prints ms/op.
func TestConcurrentWritePerf(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	want := wantTags()
	cover := makeJPEG(t)

	paths := make([]string, perfCopies)
	for i := range paths {
		paths[i] = makeCopy(t, dir, fmt.Sprintf("perf-%03d.flac", i))
	}

	start := time.Now()
	for _, p := range paths {
		if err := tag.WriteTags(p, want); err != nil {
			t.Fatalf("write tags: %v", err)
		}
		if err := tag.WriteImage(p, cover, coverMIME); err != nil {
			t.Fatalf("write image: %v", err)
		}
	}
	elapsed := time.Since(start)
	perOp := elapsed / perfCopies

	t.Logf("PERF: tagged %d FLAC copies (WriteTags+WriteImage) in %s -> %s/op",
		perfCopies, elapsed, perOp)
}
