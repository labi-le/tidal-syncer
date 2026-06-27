package download_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mewkiz/flac"

	"github.com/labi-le/tidal-syncer/pkg/tidal/download"
	"github.com/labi-le/tidal-syncer/pkg/tidal/manifest"
)

const (
	dashInitName        = "/init.mp4"
	dashMediaSegments   = 3
	dashFirstMedia      = 1
	dashTimelineRepeat  = dashMediaSegments - dashFirstMedia
	flacMagic           = "fLaC"
	fakeFFmpegSignalEnv = "GO_DASH_FAKE_FFMPEG_SIGNAL"
	processWaitTimeout  = 5 * time.Second
	processPollStep     = 10 * time.Millisecond
	fakeFFmpegPerm      = 0o600
)

// TestMain re-execs this test binary as a stand-in ffmpeg when the signal-path
// env var is set: it records its PID and blocks so the DASH-cancel test can
// prove exec.CommandContext kills the child. Without the env var it runs the
// suite normally.
func TestMain(m *testing.M) {
	if signalPath := os.Getenv(fakeFFmpegSignalEnv); signalPath != "" {
		_ = os.WriteFile(signalPath, []byte(strconv.Itoa(os.Getpid())), fakeFFmpegPerm)
		for {
			time.Sleep(time.Hour)
		}
	}

	os.Exit(m.Run())
}

func TestDASHDemuxesToValidFLAC(t *testing.T) {
	ffmpegPath := requireFFmpeg(t)
	stream := generateFMP4(t, ffmpegPath)

	srv := newSegmentServer(t, stream)
	defer srv.Close()

	provider := fakeProvider{responses: map[string]fakeResponse{
		qualityHiRes: {mimeType: manifest.MimeDASH, manifestB64: dashManifestB64(t, srv.URL), granted: qualityHiRes},
	}}
	dl := download.New(provider, srv.Client(), download.WithFFmpeg(ffmpegPath))
	destPath := filepath.Join(t.TempDir(), "track.flac")

	quality, err := dl.Download(context.Background(), testTrackID, destPath)
	if err != nil {
		t.Fatalf("Download: unexpected error: %v", err)
	}
	if quality != qualityHiRes {
		t.Fatalf("obtained quality = %q, want %q", quality, qualityHiRes)
	}

	assertValidFLAC(t, destPath)
	assertAbsent(t, destPath+partSuffix, "part file must not survive a successful demux")
}

func TestDASHCancelKillsFFmpegChild(t *testing.T) {
	signalPath := filepath.Join(t.TempDir(), "ffmpeg.pid")
	t.Setenv(fakeFFmpegSignalEnv, signalPath)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("dash-segment-bytes"))
	}))
	defer srv.Close()

	provider := fakeProvider{responses: map[string]fakeResponse{
		qualityHiRes: {mimeType: manifest.MimeDASH, manifestB64: dashManifestB64(t, srv.URL), granted: qualityHiRes},
	}}
	dl := download.New(provider, srv.Client(), download.WithFFmpeg(os.Args[0]))
	destPath := filepath.Join(t.TempDir(), "track.flac")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := dl.Download(ctx, testTrackID, destPath)
		done <- err
	}()

	pid := waitForChildPID(t, signalPath)
	cancel()
	err := <-done

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Download error = %v, want errors.Is context.Canceled", err)
	}
	assertProcessGone(t, pid)
	assertAbsent(t, destPath+partSuffix, "part file must be removed after cancel")
	assertAbsent(t, destPath, "dest file must not exist after cancel")
}

// requireFFmpeg resolves the real ffmpeg binary; the DASH path is exercised for
// real, so its absence is a hard failure rather than a skip.
func requireFFmpeg(t *testing.T) string {
	t.Helper()

	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatalf("ffmpeg is required for the DASH download tests: %v", err)
	}

	return path
}

// generateFMP4 builds a fragmented MP4 carrying a FLAC audio stream, the shape
// Tidal serves over DASH, so the demux copies a real stream.
func generateFMP4(t *testing.T, ffmpegPath string) []byte {
	t.Helper()

	out := filepath.Join(t.TempDir(), "audio.mp4")
	cmd := exec.Command(ffmpegPath,
		"-nostdin", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-c:a", "flac",
		"-movflags", "+frag_keyframe+empty_moov+default_base_moof",
		"-f", "mp4", out)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate fmp4 fixture: %v\n%s", err, combined)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read fmp4 fixture: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("fmp4 fixture is empty")
	}

	return data
}

// dashManifestB64 builds the base64-wrapped MPD that manifest.Parse decodes,
// pointing the init and media templates at srvURL.
func dashManifestB64(t *testing.T, srvURL string) string {
	t.Helper()

	mpd := fmt.Sprintf(`<?xml version="1.0"?>`+
		`<MPD xmlns="urn:mpeg:dash:schema:mpd:2011"><Period>`+
		`<AdaptationSet mimeType="audio/mp4"><Representation>`+
		`<SegmentTemplate initialization="%[1]s/init.mp4" media="%[1]s/seg_$Number$.mp4" startNumber="1">`+
		`<SegmentTimeline><S r="%[2]d"/></SegmentTimeline>`+
		`</SegmentTemplate></Representation></AdaptationSet></Period></MPD>`,
		srvURL, dashTimelineRepeat)

	return base64.StdEncoding.EncodeToString([]byte(mpd))
}

// newSegmentServer serves the fragmented-MP4 stream split across the init and
// media segments, so a correct download fetches them in order and concatenates
// them back into the original bytes.
func newSegmentServer(t *testing.T, stream []byte) *httptest.Server {
	t.Helper()

	routes := splitSegments(stream)

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)

			return
		}
		_, _ = w.Write(body)
	}))
}

// splitSegments maps each segment path to a slice of stream, with the init
// segment carrying any division remainder so the concatenation is byte-exact.
func splitSegments(stream []byte) map[string][]byte {
	names := segmentNames()
	size := len(stream) / len(names)
	routes := make(map[string][]byte, len(names))

	offset := 0
	for i, name := range names {
		span := size
		if i == 0 {
			span = len(stream) - size*(len(names)-1)
		}
		routes[name] = stream[offset : offset+span]
		offset += span
	}

	return routes
}

// segmentNames lists the segment paths in download order: the init segment
// first, then each media segment.
func segmentNames() []string {
	names := make([]string, 0, dashMediaSegments+dashFirstMedia)
	names = append(names, dashInitName)
	for number := dashFirstMedia; number <= dashMediaSegments; number++ {
		names = append(names, fmt.Sprintf("/seg_%d.mp4", number))
	}

	return names
}

// assertValidFLAC checks the file is a FLAC stream whose every frame decodes,
// proving the demux produced real, undamaged audio.
func assertValidFLAC(t *testing.T, path string) {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if !bytes.HasPrefix(raw, []byte(flacMagic)) {
		t.Fatalf("dest is not a FLAC stream: % x", raw[:min(len(raw), len(flacMagic))])
	}

	stream, err := flac.New(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parse FLAC: %v", err)
	}

	decoded := false
	for {
		_, parseErr := stream.ParseNext()
		if errors.Is(parseErr, io.EOF) {
			break
		}
		if parseErr != nil {
			t.Fatalf("decode FLAC frame: %v", parseErr)
		}
		decoded = true
	}
	if !decoded {
		t.Fatal("FLAC stream decoded zero frames")
	}
}

// waitForChildPID waits for the stand-in ffmpeg to record its PID, proving the
// child has started before the test cancels the context.
func waitForChildPID(t *testing.T, signalPath string) int {
	t.Helper()

	var pid int
	waitUntil(t, "fake ffmpeg to report its pid", func() bool {
		raw, err := os.ReadFile(signalPath)
		if err != nil || len(raw) == 0 {
			return false
		}
		parsed, convErr := strconv.Atoi(strings.TrimSpace(string(raw)))
		if convErr != nil {
			return false
		}
		pid = parsed

		return true
	})

	return pid
}

// assertProcessGone proves the ffmpeg child was killed and reaped: signal 0 to a
// live PID succeeds, but returns ESRCH once the process no longer exists.
func assertProcessGone(t *testing.T, pid int) {
	t.Helper()

	waitUntil(t, "ffmpeg child to terminate", func() bool {
		return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
	})
}

func assertAbsent(t *testing.T, path, why string) {
	t.Helper()

	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s: %q stat err = %v, want not-exist", why, path, err)
	}
}

func waitUntil(t *testing.T, desc string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(processWaitTimeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(processPollStep)
	}

	t.Fatalf("timed out waiting for %s", desc)
}
