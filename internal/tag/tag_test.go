package tag_test

import (
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"go.senan.xyz/taglib"

	"github.com/labi-le/tidal-syncer/internal/tag"
)

const (
	pipelineTitle       = "Pipeline Title"
	pipelineArtist      = "Pipeline Artist"
	pipelineAlbumArtist = "Pipeline Album Artist"
	pipelineAlbum       = "Pipeline Album"
	pipelineTrack       = 7
	pipelineDisc        = 1
	pipelineTrackTag    = "7"
	pipelineDiscTag     = "1"
	pipelineDate        = "2026"
	pipelineGenre       = "Ambient"
	pipelineGenreSecond = "Drone"
	pipelineISRC        = "USS1Z9900001"
	pipelineCopyright   = "© 2026 Pipeline Records"
	pipelinePlainLyrics = "first line\nsecond line"
	pipelineSyncedLRC   = "[00:00.00]first line\n[00:02.50]second line\n"
	lrcExt              = ".lrc"
	truncateDivisor     = 2
)

// pipelineMeta returns the canonical Meta the pipeline tests tag a copy of the
// FLAC fixture with: every Vorbis field T11 populates, incl. a UTF-8 copyright.
func pipelineMeta() tag.Meta {
	return tag.Meta{
		Title:       pipelineTitle,
		Artist:      pipelineArtist,
		AlbumArtist: pipelineAlbumArtist,
		Album:       pipelineAlbum,
		TrackNumber: pipelineTrack,
		DiscNumber:  pipelineDisc,
		Date:        pipelineDate,
		Genre:       []string{pipelineGenre, pipelineGenreSecond},
		ISRC:        pipelineISRC,
		Copyright:   pipelineCopyright,
	}
}

// wantCoreTags is the exact Vorbis comment map TagFile must produce for
// pipelineMeta with no lyrics: TrackNumber and DiscNumber rendered as decimal
// strings, every other field copied verbatim.
func wantCoreTags() map[string][]string {
	return map[string][]string{
		taglib.Title:       {pipelineTitle},
		taglib.Artist:      {pipelineArtist},
		taglib.AlbumArtist: {pipelineAlbumArtist},
		taglib.Album:       {pipelineAlbum},
		taglib.TrackNumber: {pipelineTrackTag},
		taglib.DiscNumber:  {pipelineDiscTag},
		taglib.Date:        {pipelineDate},
		taglib.Genre:       {pipelineGenre, pipelineGenreSecond},
		taglib.ISRC:        {pipelineISRC},
		taglib.Copyright:   {pipelineCopyright},
	}
}

// TestPipelineTagsCoverAndLyrics proves the full tag pipeline: writing every
// Vorbis field, embedding a JPEG front cover, the LYRICS comment, and a synced
// .lrc sidecar all round-trip back exactly as written.
func TestPipelineTagsCoverAndLyrics(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := makeCopy(t, dir, "pipeline.flac")
	cover := makeJPEG(t)

	if err := tag.TagFile(path, pipelineMeta(), cover, pipelinePlainLyrics); err != nil {
		t.Fatalf("TagFile: %v", err)
	}

	got, err := tag.ReadTags(path)
	if err != nil {
		t.Fatalf("ReadTags: %v", err)
	}
	want := wantCoreTags()
	want[taglib.Lyrics] = []string{pipelinePlainLyrics}
	if !maps.EqualFunc(want, got, eqStr) {
		t.Fatalf("tags mismatch:\n want=%v\n got =%v", want, got)
	}

	gotCover, err := tag.ReadImage(path)
	if err != nil {
		t.Fatalf("ReadImage: %v", err)
	}
	if !bytes.Equal(cover, gotCover) {
		t.Fatalf("cover mismatch: wrote %d bytes, read %d", len(cover), len(gotCover))
	}

	if err = tag.WriteLRC(path, pipelineSyncedLRC); err != nil {
		t.Fatalf("WriteLRC: %v", err)
	}
	sidecar := path[:len(path)-len(filepath.Ext(path))] + lrcExt
	data, err := os.ReadFile(sidecar)
	if err != nil {
		t.Fatalf("read .lrc sidecar: %v", err)
	}
	if string(data) != pipelineSyncedLRC {
		t.Fatalf(".lrc content = %q, want %q", data, pipelineSyncedLRC)
	}
}

// TestPipelineSkipsMissingCoverAndLyrics proves TagFile still writes the core
// tags when given no cover (nil) and no lyrics (empty): the tags round-trip and
// no front cover is embedded, neither omission being fatal.
func TestPipelineSkipsMissingCoverAndLyrics(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := makeCopy(t, dir, "minimal.flac")

	if err := tag.TagFile(path, pipelineMeta(), nil, ""); err != nil {
		t.Fatalf("TagFile without cover/lyrics: %v", err)
	}

	got, err := tag.ReadTags(path)
	if err != nil {
		t.Fatalf("ReadTags: %v", err)
	}
	if want := wantCoreTags(); !maps.EqualFunc(want, got, eqStr) {
		t.Fatalf("tags mismatch:\n want=%v\n got =%v", want, got)
	}

	gotCover, err := tag.ReadImage(path)
	if err != nil {
		t.Fatalf("ReadImage: %v", err)
	}
	if len(gotCover) != 0 {
		t.Fatalf("want no embedded cover, got %d bytes", len(gotCover))
	}
}

// TestIntegrityAcceptsValidFLAC proves IntegrityCheck passes on an intact FLAC
// whose decoded sample count matches its StreamInfo header.
func TestIntegrityAcceptsValidFLAC(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := makeCopy(t, dir, "valid.flac")

	if err := tag.IntegrityCheck(path); err != nil {
		t.Fatalf("IntegrityCheck(valid) = %v, want nil", err)
	}
}

// TestIntegrityRejectsTruncatedFLAC proves IntegrityCheck fails on a FLAC whose
// audio frames were cut short mid-stream.
func TestIntegrityRejectsTruncatedFLAC(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := makeCopy(t, dir, "truncated.flac")
	truncateFile(t, path)

	err := tag.IntegrityCheck(path)
	if err == nil {
		t.Fatal("IntegrityCheck(truncated) = nil, want error")
	}
	t.Logf("truncated FLAC correctly rejected: %v", err)
}

// truncateFile rewrites path keeping only the first 1/truncateDivisor of its
// bytes, cutting the audio frames mid-stream to simulate a corrupt download.
func truncateFile(t *testing.T, path string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err = os.WriteFile(path, data[:len(data)/truncateDivisor], 0o600); err != nil {
		t.Fatalf("write truncated %s: %v", path, err)
	}
}

// TestReadGenreReturnsGenreComments proves ReadGenre returns every GENRE Vorbis
// comment of a file, in order.
func TestReadGenreReturnsGenreComments(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := makeCopy(t, dir, "genre.flac")
	if err := tag.WriteTags(path, map[string][]string{
		taglib.Genre: {pipelineGenre, pipelineGenreSecond},
	}); err != nil {
		t.Fatalf("WriteTags: %v", err)
	}

	got, err := tag.ReadGenre(path)
	if err != nil {
		t.Fatalf("ReadGenre: %v", err)
	}
	if want := []string{pipelineGenre, pipelineGenreSecond}; !slices.Equal(want, got) {
		t.Fatalf("genres = %v, want %v", got, want)
	}
}

// TestReadGenreEmptyWhenAbsent proves ReadGenre returns no genres for a file
// whose comments carry none.
func TestReadGenreEmptyWhenAbsent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := makeCopy(t, dir, "nogenre.flac")
	if err := tag.WriteTags(path, map[string][]string{
		taglib.Title: {pipelineTitle},
	}); err != nil {
		t.Fatalf("WriteTags: %v", err)
	}

	got, err := tag.ReadGenre(path)
	if err != nil {
		t.Fatalf("ReadGenre: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("genres = %v, want none", got)
	}
}
