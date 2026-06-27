package download_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/labi-le/tidal-syncer/pkg/tidal/download"
	"github.com/labi-le/tidal-syncer/pkg/tidal/manifest"
)

const (
	testTrackID         = "12345"
	qualityHiRes        = "HI_RES_LOSSLESS"
	qualityLossless     = "LOSSLESS"
	qualityHigh         = "HIGH"
	codecMime           = "audio/mp4"
	manifestNone        = "NONE"
	partSuffix          = ".part"
	contentLengthHeader = "Content-Length"
	truncationPadding   = 512
)

// errNoSuchQuality is returned by fakeProvider when a quality is not stocked,
// standing in for TIDAL reporting a tier as unavailable.
var errNoSuchQuality = errors.New("fake: quality unavailable")

// fakeResponse is one canned PlaybackInfo reply keyed by requested quality.
type fakeResponse struct {
	mimeType    string
	manifestB64 string
	granted     string
	err         error
}

// fakeProvider is an in-memory PlaybackProvider: it answers PlaybackInfo from a
// quality-keyed table so tests drive the quality ladder deterministically.
type fakeProvider struct {
	responses map[string]fakeResponse
}

func (f fakeProvider) PlaybackInfo(_ context.Context, _, quality string) (string, string, string, error) {
	resp, ok := f.responses[quality]
	if !ok {
		return "", "", "", errNoSuchQuality
	}

	return resp.mimeType, resp.manifestB64, resp.granted, resp.err
}

// flacBytes returns opaque stream bytes; the download package never parses the
// payload, so a recognisable marker suffices.
func flacBytes() []byte {
	return []byte("fLaC tidal-syncer fake flac stream payload bytes")
}

// btsManifestB64 builds the base64-wrapped BTS manifest JSON that manifest.Parse
// decodes, pointing at the given direct-download URLs.
func btsManifestB64(t *testing.T, urls ...string) string {
	t.Helper()

	payload := struct {
		MimeType       string   `json:"mimeType"`
		EncryptionType string   `json:"encryptionType"`
		URLs           []string `json:"urls"`
	}{MimeType: codecMime, EncryptionType: manifestNone, URLs: urls}

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal bts manifest: %v", err)
	}

	return base64.StdEncoding.EncodeToString(raw)
}

// newContentServer serves body in full with a correct Content-Length.
func newContentServer(body []byte) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
}

// newTruncatingServer promises more bytes than it sends, then aborts the
// connection so the client observes a short read.
func newTruncatingServer(body []byte) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(contentLengthHeader, strconv.Itoa(len(body)+truncationPadding))
		_, _ = w.Write(body)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		panic(http.ErrAbortHandler)
	}))
}

// newBlockingServer flushes prefix, signals via the returned channel, then
// blocks until the client cancels, so a test can cancel mid-stream.
func newBlockingServer(prefix []byte) (*httptest.Server, <-chan struct{}) {
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(contentLengthHeader, strconv.Itoa(len(prefix)+truncationPadding))
		_, _ = w.Write(prefix)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		close(started)
		<-r.Context().Done()
	}))

	return srv, started
}

func TestBTSDownloadWritesFinalFileAtomically(t *testing.T) {
	t.Parallel()

	body := flacBytes()
	srv := newContentServer(body)
	defer srv.Close()

	provider := fakeProvider{responses: map[string]fakeResponse{
		qualityHiRes: {mimeType: manifest.MimeBTS, manifestB64: btsManifestB64(t, srv.URL), granted: qualityHiRes},
	}}
	dl := download.New(provider, srv.Client())
	destPath := filepath.Join(t.TempDir(), "track.flac")

	quality, err := dl.Download(context.Background(), testTrackID, destPath)
	if err != nil {
		t.Fatalf("Download: unexpected error: %v", err)
	}
	if quality != qualityHiRes {
		t.Fatalf("obtained quality = %q, want %q", quality, qualityHiRes)
	}

	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("dest content = %d bytes, want %d", len(got), len(body))
	}
	if _, statErr := os.Stat(destPath + partSuffix); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("part file must not survive a successful download, stat err = %v", statErr)
	}
}

func TestBTSFallsBackToLosslessWhenHiResUnavailable(t *testing.T) {
	t.Parallel()

	body := flacBytes()
	srv := newContentServer(body)
	defer srv.Close()

	provider := fakeProvider{responses: map[string]fakeResponse{
		qualityLossless: {mimeType: manifest.MimeBTS, manifestB64: btsManifestB64(t, srv.URL), granted: qualityLossless},
	}}
	dl := download.New(provider, srv.Client())
	destPath := filepath.Join(t.TempDir(), "track.flac")

	quality, err := dl.Download(context.Background(), testTrackID, destPath)
	if err != nil {
		t.Fatalf("Download: unexpected error: %v", err)
	}
	if quality != qualityLossless {
		t.Fatalf("obtained quality = %q, want %q (lossless floor)", quality, qualityLossless)
	}

	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("dest content = %d bytes, want %d", len(got), len(body))
	}
}

func TestBTSRejectsSubFloorGrantedQuality(t *testing.T) {
	t.Parallel()

	// Given TIDAL answers every lossless request with HTTP 200 but grants only
	// HIGH, serving a lossy MP4/AAC body (ftyp box header) rather than FLAC.
	body := []byte("\x00\x00\x00\x1cftypM4A lossy aac mp4 payload, not flac")
	srv := newContentServer(body)
	defer srv.Close()

	provider := fakeProvider{responses: map[string]fakeResponse{
		qualityHiRes:    {mimeType: manifest.MimeBTS, manifestB64: btsManifestB64(t, srv.URL), granted: qualityHigh},
		qualityLossless: {mimeType: manifest.MimeBTS, manifestB64: btsManifestB64(t, srv.URL), granted: qualityHigh},
	}}
	dl := download.New(provider, srv.Client())
	destPath := filepath.Join(t.TempDir(), "track.flac")

	// When the track is downloaded
	quality, err := dl.Download(context.Background(), testTrackID, destPath)

	// Then it fails with ErrBelowFloor and writes neither the .flac nor the .part,
	// so a lossy stream can never masquerade as a FLAC file.
	if !errors.Is(err, download.ErrBelowFloor) {
		t.Fatalf("Download error = %v, want errors.Is ErrBelowFloor", err)
	}
	if quality != "" {
		t.Fatalf("obtained quality = %q, want empty on sub-floor rejection", quality)
	}
	assertAbsent(t, destPath, "a sub-floor grant must never create the .flac destination")
	assertAbsent(t, destPath+partSuffix, "a sub-floor grant must leave no .part file")
}

func TestTruncatedStreamRemovesPartAndFails(t *testing.T) {
	t.Parallel()

	body := flacBytes()
	srv := newTruncatingServer(body)
	defer srv.Close()

	provider := fakeProvider{responses: map[string]fakeResponse{
		qualityHiRes: {mimeType: manifest.MimeBTS, manifestB64: btsManifestB64(t, srv.URL), granted: qualityHiRes},
	}}
	dl := download.New(provider, srv.Client())
	destPath := filepath.Join(t.TempDir(), "track.flac")

	quality, err := dl.Download(context.Background(), testTrackID, destPath)
	if err == nil {
		t.Fatal("Download: want error on a truncated stream, got nil")
	}
	if quality != "" {
		t.Fatalf("obtained quality = %q, want empty on failure", quality)
	}
	if _, statErr := os.Stat(destPath + partSuffix); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("part file must be removed on truncation, stat err = %v", statErr)
	}
	if _, statErr := os.Stat(destPath); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("dest file must not exist on truncation, stat err = %v", statErr)
	}
}

func TestCancelledContextRemovesPart(t *testing.T) {
	t.Parallel()

	srv, started := newBlockingServer(flacBytes())
	defer srv.Close()

	provider := fakeProvider{responses: map[string]fakeResponse{
		qualityHiRes: {mimeType: manifest.MimeBTS, manifestB64: btsManifestB64(t, srv.URL), granted: qualityHiRes},
	}}
	dl := download.New(provider, srv.Client())
	destPath := filepath.Join(t.TempDir(), "track.flac")

	ctx, cancel := context.WithCancel(context.Background())
	type outcome struct {
		quality string
		err     error
	}
	done := make(chan outcome, 1)
	go func() {
		quality, err := dl.Download(ctx, testTrackID, destPath)
		done <- outcome{quality: quality, err: err}
	}()

	<-started
	cancel()
	got := <-done

	if got.err == nil {
		t.Fatal("Download: want error after context cancel, got nil")
	}
	if !errors.Is(got.err, context.Canceled) {
		t.Fatalf("error = %v, want errors.Is context.Canceled", got.err)
	}
	if _, statErr := os.Stat(destPath + partSuffix); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("part file must be removed after cancel, stat err = %v", statErr)
	}
	if _, statErr := os.Stat(destPath); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatalf("dest file must not exist after cancel, stat err = %v", statErr)
	}
}
