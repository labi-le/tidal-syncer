package manifest_test

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/labi-le/tidal-syncer/pkg/tidal/manifest"
)

const codecMime = "audio/mp4"

func loadFixture(t *testing.T, name string) string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}

	return strings.TrimSpace(string(raw))
}

func TestParseBTSReturnsDirectURLs(t *testing.T) {
	// Given a base64-wrapped BTS manifest with NONE encryption
	encoded := loadFixture(t, "bts.manifest.b64")

	// When it is parsed
	got, err := manifest.Parse(manifest.MimeBTS, encoded)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	// Then the result is a BTS manifest exposing the direct download URLs
	if got.Kind() != manifest.KindBTS {
		t.Fatalf("Kind() = %q, want %q", got.Kind(), manifest.KindBTS)
	}

	bts, ok := got.BTS()
	if !ok {
		t.Fatal("BTS() ok = false, want true")
	}

	want := []string{
		"https://tidal.example/track/12345/part0.mp4",
		"https://tidal.example/track/12345/part1.mp4",
	}
	if urls := bts.URLs(); !slices.Equal(urls, want) {
		t.Fatalf("URLs() = %v, want %v", urls, want)
	}

	if mime := bts.MimeType(); mime != codecMime {
		t.Fatalf("MimeType() = %q, want %q", mime, codecMime)
	}
}

func TestParseDASHReturnsOrderedSegmentURLsInitFirst(t *testing.T) {
	// Given a base64-wrapped DASH manifest with a SegmentTimeline (4+1 segments)
	encoded := loadFixture(t, "dash.manifest.b64")

	// When it is parsed
	got, err := manifest.Parse(manifest.MimeDASH, encoded)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	// Then the result is a DASH manifest yielding the init segment first,
	// followed by media segments in timeline order
	if got.Kind() != manifest.KindDASH {
		t.Fatalf("Kind() = %q, want %q", got.Kind(), manifest.KindDASH)
	}

	dash, ok := got.DASH()
	if !ok {
		t.Fatal("DASH() ok = false, want true")
	}

	want := []string{
		"https://tidal.example/dash/777/init.mp4",
		"https://tidal.example/dash/777/segment_1.mp4",
		"https://tidal.example/dash/777/segment_2.mp4",
		"https://tidal.example/dash/777/segment_3.mp4",
		"https://tidal.example/dash/777/segment_4.mp4",
		"https://tidal.example/dash/777/segment_5.mp4",
	}
	if segments := dash.SegmentURLs(); !slices.Equal(segments, want) {
		t.Fatalf("SegmentURLs() = %v, want %v", segments, want)
	}

	if mime := dash.MimeType(); mime != codecMime {
		t.Fatalf("MimeType() = %q, want %q", mime, codecMime)
	}
}

func TestParseReturnsErrEncrypted(t *testing.T) {
	// Given a base64-wrapped BTS manifest declaring a non-NONE encryption type
	encoded := loadFixture(t, "encrypted.manifest.b64")

	// When it is parsed
	_, err := manifest.Parse(manifest.MimeBTS, encoded)

	// Then the sentinel ErrEncrypted is reported
	if !errors.Is(err, manifest.ErrEncrypted) {
		t.Fatalf("error = %v, want errors.Is ErrEncrypted", err)
	}
}

func TestParseWrapsMalformedBase64(t *testing.T) {
	// Given an input that is not valid base64
	const malformed = "@@@ not valid base64 @@@"

	// When it is parsed
	_, err := manifest.Parse(manifest.MimeBTS, malformed)

	// Then the underlying base64 error is wrapped and recoverable
	if err == nil {
		t.Fatal("Parse error = nil, want non-nil")
	}

	var corrupt base64.CorruptInputError
	if !errors.As(err, &corrupt) {
		t.Fatalf("error = %v, want wrapped base64.CorruptInputError", err)
	}
}

func TestParseReturnsTypedErrorForUnknownMime(t *testing.T) {
	// Given a valid base64 payload but an unrecognized MIME type
	const unknownMime = "application/octet-stream"
	encoded := base64.StdEncoding.EncodeToString([]byte("{}"))

	// When it is parsed
	_, err := manifest.Parse(unknownMime, encoded)

	// Then a typed UnknownMimeTypeError carrying the MIME type is returned
	var mimeErr *manifest.UnknownMimeTypeError
	if !errors.As(err, &mimeErr) {
		t.Fatalf("error = %v, want *manifest.UnknownMimeTypeError", err)
	}

	if mimeErr.MimeType != unknownMime {
		t.Fatalf("MimeType = %q, want %q", mimeErr.MimeType, unknownMime)
	}
}

func TestParseReturnsErrInvalidManifestForEmptyDASH(t *testing.T) {
	// Given a structurally valid MPD that contains no Representation
	const emptyMPD = `<?xml version="1.0"?>` +
		`<MPD xmlns="urn:mpeg:dash:schema:mpd:2011"><Period></Period></MPD>`
	encoded := base64.StdEncoding.EncodeToString([]byte(emptyMPD))

	// When it is parsed
	_, err := manifest.Parse(manifest.MimeDASH, encoded)

	// Then the sentinel ErrInvalidManifest is reported
	if !errors.Is(err, manifest.ErrInvalidManifest) {
		t.Fatalf("error = %v, want errors.Is ErrInvalidManifest", err)
	}
}
