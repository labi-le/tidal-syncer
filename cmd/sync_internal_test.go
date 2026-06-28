package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/rs/zerolog"
	"golang.org/x/time/rate"

	"github.com/labi-le/tidal-syncer/internal/config"
	"github.com/labi-le/tidal-syncer/internal/lock"
	"github.com/labi-le/tidal-syncer/internal/store"
	synceng "github.com/labi-le/tidal-syncer/internal/sync"
	"github.com/labi-le/tidal-syncer/internal/tag"
	"github.com/labi-le/tidal-syncer/pkg/tidal"
	"github.com/labi-le/tidal-syncer/pkg/tidal/download"
	"github.com/labi-le/tidal-syncer/pkg/tidal/manifest"
)

// Test_newSyncCmd_uses_sync_name asserts the subcommand is registered under the
// "sync" verb with an executable RunE.
func Test_newSyncCmd_uses_sync_name(t *testing.T) {
	t.Parallel()

	// Given a config path, verbose flag and logger captured by the command closure
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	verbose := false
	logger := zerolog.Nop()

	// When the sync command is built
	cmd := newSyncCmd(&configPath, &verbose, &logger)

	// Then it is the "sync" verb and is runnable
	if cmd.Use != "sync" {
		t.Fatalf("Use = %q, want %q", cmd.Use, "sync")
	}
	if cmd.RunE == nil {
		t.Fatal("RunE = nil, want a runnable handler")
	}
}

// Test_runSync_reports_friendly_error_when_lock_held asserts that a second sync,
// finding the data-directory lock already held, fails fast with the friendly
// sentinel instead of panicking or reaching the network.
func Test_runSync_reports_friendly_error_when_lock_held(t *testing.T) {
	t.Parallel()

	// Given a valid config whose data-directory lock is already held
	ctx := context.Background()
	dataDir := t.TempDir()
	musicDir := t.TempDir()
	configPath := writeSyncConfig(t, dataDir, musicDir)

	release := acquireSyncLock(t, dataDir)
	defer func() { _ = release() }()

	// When a one-shot sync runs against the same data directory
	err := runSync(ctx, configPath, false, zerolog.Nop())

	// Then it returns the friendly "already running" sentinel
	if !errors.Is(err, errAnotherSyncRunning) {
		t.Fatalf("runSync error = %v, want errAnotherSyncRunning", err)
	}
}

// Test_sync_wiring_runs_end_to_end_with_mock_playback drives the real sync
// wiring path the cmd layer composes — the cross-process file lock, the
// download.SweepStale janitor, the real download.Downloader built by
// synceng.NewDownloader, the engine's SyncOnce, and snapshot persistence —
// against temp config and music directories, swapping only the unmockable seam:
// a mock download.PlaybackProvider standing in for the TIDAL wire client.
//
// It is a deliberate near-seam: cmd/sync.go's executeSync constructs the real
// *tidal.Client internally with no injection point, and engine.SyncOnce calls
// client.UserID before any download, so runSync/executeSync cannot complete
// without a real network. This test reproduces every other piece of that wiring
// verbatim and asserts on real observable behavior: the lock blocks a concurrent
// holder, a stale .part file is swept, a real FLAC lands on disk, the store
// records the track as done at the granted quality, and the run summary counts
// exactly one download.
func Test_sync_wiring_runs_end_to_end_with_mock_playback(t *testing.T) {
	t.Parallel()

	// Given temp data and music directories, a migrated store, and a real FLAC
	// served over HTTP through a mock playback provider feeding a real downloader.
	ctx := context.Background()
	dataDir := t.TempDir()
	musicDir := t.TempDir()

	flacBody, err := os.ReadFile(filepath.Join("testdata", "sample.flac"))
	if err != nil {
		t.Fatalf("read flac fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(flacBody)
	}))
	defer srv.Close()

	st := openMigratedStore(t, dataDir)
	cfg := wiringConfig(dataDir, musicDir)

	// And a stale .part orphan that the SweepStale step must remove.
	staleDir := filepath.Join(musicDir, "Artist", "Album 1")
	if err = os.MkdirAll(staleDir, 0o755); err != nil {
		t.Fatalf("mkdir stale dir: %v", err)
	}
	stalePart := filepath.Join(staleDir, "orphan.flac"+download.PartSuffix)
	if err = os.WriteFile(stalePart, []byte("partial"), 0o600); err != nil {
		t.Fatalf("write stale part: %v", err)
	}

	// When the cmd-layer wiring runs: acquire the lock, sweep, then run one cycle.
	release, err := (&lock.FileLock{}).TryAcquire(filepath.Join(dataDir, lockFileName))
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	defer func() { _ = release() }()

	// Then the lock is genuinely held: a concurrent acquisition is refused.
	if _, lockErr := (&lock.FileLock{}).TryAcquire(filepath.Join(dataDir, lockFileName)); !errors.Is(lockErr, lock.ErrLocked) {
		t.Fatalf("second TryAcquire = %v, want lock.ErrLocked (lock not held)", lockErr)
	}

	swept, err := download.SweepStale(cfg.Paths.Music)
	if err != nil {
		t.Fatalf("sweep stale: %v", err)
	}
	if swept != 1 {
		t.Fatalf("swept = %d, want 1 stale .part removed", swept)
	}
	if _, statErr := os.Stat(stalePart); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("stale part must be removed, stat err = %v", statErr)
	}

	provider := mockPlaybackProvider{
		manifestB64: btsManifest(t, srv.URL),
		granted:     tidal.QualityLossless,
	}
	engine := synceng.NewEngine(synceng.Params{
		Client:     &mockTidalClient{userID: "user-1", favTracks: []tidal.Track{wiringTrack()}},
		Downloader: synceng.NewDownloader(provider, srv.Client()),
		Covers:     synceng.NewCoverFetcher(srv.Client()),
		Store:      st,
		Config:     cfg,
		Logger:     zerolog.Nop(),
		Limiter:    rate.NewLimiter(rate.Inf, 1),
	})

	summary, current, err := engine.SyncOnce(ctx)
	if err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}
	if err = st.ReplaceSnapshot(ctx, synceng.SnapshotKindTracks, current); err != nil {
		t.Fatalf("persist snapshot: %v", err)
	}

	// Then exactly one track was downloaded and none failed.
	if summary.Downloaded != 1 || summary.Failed != 0 {
		t.Fatalf("summary downloaded=%d failed=%d, want downloaded=1 failed=0", summary.Downloaded, summary.Failed)
	}

	// And the store records that track as done at the granted lossless quality,
	// with a valid FLAC at the recorded path.
	record, err := st.GetTrack(ctx, strconv.Itoa(wiringTrack().ID))
	if err != nil {
		t.Fatalf("get track: %v", err)
	}
	if record.Status != store.StatusDone {
		t.Fatalf("track status = %q, want %q", record.Status, store.StatusDone)
	}
	if record.ObtainedQuality != string(tidal.QualityLossless) {
		t.Fatalf("obtained quality = %q, want %q", record.ObtainedQuality, tidal.QualityLossless)
	}
	if integErr := tag.IntegrityCheck(record.Path); integErr != nil {
		t.Fatalf("downloaded file must be a valid FLAC: %v", integErr)
	}

	// And the snapshot persisted exactly the enumerated track.
	if len(current) != 1 || current[0].TidalID != strconv.Itoa(wiringTrack().ID) {
		t.Fatalf("snapshot = %+v, want one item for the synced track", current)
	}
}

func wiringConfig(dataDir, musicDir string) config.Config {
	cfg := config.Defaults()
	cfg.Paths.Data = dataDir
	cfg.Paths.Music = musicDir
	cfg.Concurrency = 1
	cfg.Quality.Request = tidal.QualityLossless
	cfg.Scope = config.Scope{Favorites: config.Favorites{Tracks: true}}
	cfg.Lyrics = config.Lyrics{Embed: false, Sidecar: false}

	return cfg
}

func wiringTrack() tidal.Track {
	return tidal.Track{
		ID:           101,
		Title:        "Track 101",
		TrackNumber:  1,
		VolumeNumber: 1,
		ISRC:         "US0000000101",
		Copyright:    "(C) Label",
		Artists:      []tidal.Artist{{ID: 1, Name: "Artist", Type: "MAIN"}},
		Album:        tidal.AlbumRef{ID: 201, Title: "Album 1", Cover: ""},
	}
}

func openMigratedStore(t *testing.T, dataDir string) *store.Store {
	t.Helper()

	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err = st.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate store: %v", err)
	}

	return st
}

func btsManifest(t *testing.T, url string) string {
	t.Helper()

	payload := struct {
		MimeType       string   `json:"mimeType"`
		EncryptionType string   `json:"encryptionType"`
		URLs           []string `json:"urls"`
	}{MimeType: "audio/mp4", EncryptionType: "NONE", URLs: []string{url}}

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal bts manifest: %v", err)
	}

	return base64.StdEncoding.EncodeToString(raw)
}

// mockPlaybackProvider is the single mocked seam: it answers every quality with
// one canned BTS manifest and granted tier, standing in for the TIDAL client the
// real downloader would otherwise resolve manifests through.
type mockPlaybackProvider struct {
	manifestB64 string
	granted     tidal.Quality
}

func (m mockPlaybackProvider) PlaybackInfo(
	_ context.Context, _ string, _ tidal.Quality,
) (download.Playback, error) {
	return download.Playback{
		MimeType:       manifest.MimeBTS,
		ManifestB64:    m.manifestB64,
		GrantedQuality: m.granted,
	}, nil
}

// mockTidalClient is a minimal in-memory synceng.TidalClient: it yields one
// favorite track and resolves its album, with every other capability inert. It
// lets the engine enumerate and process without touching the network.
type mockTidalClient struct {
	userID    string
	favTracks []tidal.Track
}

func (m *mockTidalClient) UserID(context.Context) (string, error) { return m.userID, nil }

func (m *mockTidalClient) FavoriteTracks(context.Context) iter.Seq2[tidal.Track, error] {
	return seqOfTracks(m.favTracks)
}

func (m *mockTidalClient) FavoriteAlbums(context.Context) iter.Seq2[tidal.Album, error] {
	return func(func(tidal.Album, error) bool) {}
}

func (m *mockTidalClient) FavoritePlaylists(context.Context) iter.Seq2[tidal.Playlist, error] {
	return func(func(tidal.Playlist, error) bool) {}
}

func (m *mockTidalClient) AlbumTracks(context.Context, string) iter.Seq2[tidal.Track, error] {
	return func(func(tidal.Track, error) bool) {}
}

func (m *mockTidalClient) PlaylistTracks(context.Context, string) iter.Seq2[tidal.Track, error] {
	return func(func(tidal.Track, error) bool) {}
}

func (m *mockTidalClient) Album(_ context.Context, id string) (tidal.Album, error) {
	return tidal.Album{
		ID:              mustAtoi(id),
		Title:           "Album 1",
		NumberOfTracks:  1,
		NumberOfVolumes: 1,
		ReleaseDate:     "2020-01-01",
		Copyright:       "(C) Label",
		Artists:         []tidal.Artist{{ID: 1, Name: "Artist", Type: "MAIN"}},
		Cover:           "",
	}, nil
}

func (m *mockTidalClient) Lyrics(context.Context, string) (tidal.Lyrics, error) {
	return tidal.Lyrics{}, nil
}

// mustAtoi converts a numeric album id string back to int for the album record;
// the engine always passes a strconv.Itoa-formatted id, so this never fails.
func mustAtoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		panic(fmt.Sprintf("mockTidalClient: non-numeric album id %q: %v", s, err))
	}

	return n
}

// seqOfTracks yields each track with a nil error, matching the lazy iterator the
// engine consumes.
func seqOfTracks(tracks []tidal.Track) iter.Seq2[tidal.Track, error] {
	return func(yield func(tidal.Track, error) bool) {
		for _, track := range tracks {
			if !yield(track, nil) {
				return
			}
		}
	}
}

// writeSyncConfig writes a minimal valid config pointing data and music at the
// given temp directories and returns its path. Every other field falls back to
// config.Defaults, which already validates.
func writeSyncConfig(t *testing.T, dataDir, musicDir string) string {
	t.Helper()

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	body := "paths:\n  data: " + dataDir + "\n  music: " + musicDir + "\n" +
		"tidal_auth:\n  client_id: test-id\n  client_secret: test-secret\n"
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	return configPath
}

// acquireSyncLock takes the exact flock runSync contends on, so the call under
// test observes a held lock. The returned release frees it.
func acquireSyncLock(t *testing.T, dataDir string) func() error {
	t.Helper()

	release, err := (&lock.FileLock{}).TryAcquire(filepath.Join(dataDir, lockFileName))
	if err != nil {
		t.Fatalf("pre-acquire lock: %v", err)
	}

	return release
}
