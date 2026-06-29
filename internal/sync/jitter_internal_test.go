package sync

import (
	"context"
	"iter"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/time/rate"

	"github.com/labi-le/tidal-syncer/internal/config"
	"github.com/labi-le/tidal-syncer/internal/store"
	"github.com/labi-le/tidal-syncer/pkg/tidal"
)

func TestSyncOnce_WaitsForWorkerJitterBeforeTrackPickup(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err = st.Migrate(ctx); err != nil {
		t.Fatalf("migrate store: %v", err)
	}

	cfg := config.Defaults()
	cfg.Paths.Music = t.TempDir()
	cfg.Concurrency = 1
	cfg.Scope = config.Scope{Favorites: config.Favorites{Tracks: true}}
	cfg.Jitter.Worker = config.DurationRange{Min: time.Second, Max: 2 * time.Second}

	client := jitterTestClient{
		tracks: []tidal.Track{jitterTestTrack(101)},
		album:  jitterTestAlbum(),
	}
	dl := jitterTestDownloader{src: filepath.Join("testdata", "sample.flac")}
	engine := NewEngine(Params{
		Client:     client,
		Downloader: dl,
		Covers:     jitterTestCovers{},
		Store:      st,
		Config:     cfg,
		Logger:     zerolog.Nop(),
		Limiter:    rate.NewLimiter(rate.Inf, 1),
	})

	var (
		mu        sync.Mutex
		durations []time.Duration
	)
	oldPick := workerDelayFn
	oldWait := waitForDelay
	workerDelayFn = func(config.DurationRange) time.Duration {
		return 1500 * time.Millisecond
	}
	waitForDelay = func(waitCtx context.Context, d time.Duration) error {
		mu.Lock()
		durations = append(durations, d)
		mu.Unlock()

		select {
		case <-waitCtx.Done():
			return waitCtx.Err()
		default:
			return nil
		}
	}
	t.Cleanup(func() {
		workerDelayFn = oldPick
		waitForDelay = oldWait
	})

	if _, _, err = engine.SyncOnce(ctx); err != nil {
		t.Fatalf("SyncOnce: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(durations) != 1 {
		t.Fatalf("worker jitter waits = %d, want 1", len(durations))
	}
	if durations[0] != 1500*time.Millisecond {
		t.Fatalf("worker jitter wait = %s, want injected 1500ms", durations[0])
	}
}

type jitterTestClient struct {
	tracks []tidal.Track
	album  tidal.Album
}

func (j jitterTestClient) UserID(context.Context) (string, error) { return "user", nil }

func (j jitterTestClient) FavoriteTracks(context.Context) iter.Seq2[tidal.Track, error] {
	return func(yield func(tidal.Track, error) bool) {
		for _, track := range j.tracks {
			if !yield(track, nil) {
				return
			}
		}
	}
}

func (j jitterTestClient) FavoriteAlbums(context.Context) iter.Seq2[tidal.Album, error] {
	return func(func(tidal.Album, error) bool) {}
}

func (j jitterTestClient) FavoritePlaylists(context.Context) iter.Seq2[tidal.Playlist, error] {
	return func(func(tidal.Playlist, error) bool) {}
}

func (j jitterTestClient) AlbumTracks(context.Context, string) iter.Seq2[tidal.Track, error] {
	return func(func(tidal.Track, error) bool) {}
}

func (j jitterTestClient) PlaylistTracks(context.Context, string) iter.Seq2[tidal.Track, error] {
	return func(func(tidal.Track, error) bool) {}
}

func (j jitterTestClient) Album(context.Context, string) (tidal.Album, error) { return j.album, nil }

func (j jitterTestClient) Lyrics(context.Context, string) (tidal.Lyrics, error) {
	return tidal.Lyrics{}, nil
}

type jitterTestDownloader struct{ src string }

func (j jitterTestDownloader) Download(_ context.Context, _ string, destPath string) (tidal.Quality, error) {
	data, err := os.ReadFile(j.src)
	if err != nil {
		return "", err
	}
	if err = os.WriteFile(destPath, data, 0o600); err != nil {
		return "", err
	}

	return tidal.QualityHiResLossless, nil
}

type jitterTestCovers struct{}

func (jitterTestCovers) Cover(context.Context, string) ([]byte, error) { return nil, nil }

func jitterTestTrack(id int) tidal.Track {
	return tidal.Track{
		ID:           id,
		Title:        "Track",
		TrackNumber:  1,
		VolumeNumber: 1,
		ISRC:         "US1234567890",
		Artists:      []tidal.Artist{{ID: 1, Name: "Artist", Type: "MAIN"}},
		Album:        tidal.AlbumRef{ID: 201, Title: "Album"},
	}
}

func jitterTestAlbum() tidal.Album {
	return tidal.Album{
		ID:              201,
		Title:           "Album",
		NumberOfTracks:  1,
		NumberOfVolumes: 1,
		ReleaseDate:     "2020-01-01",
		Artists:         []tidal.Artist{{ID: 1, Name: "Artist", Type: "MAIN"}},
	}
}

func init() {
	_ = strconv.Itoa
}
