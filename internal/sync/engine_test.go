package sync_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/rs/zerolog"
	"golang.org/x/time/rate"

	"github.com/labi-le/tidal-syncer/internal/config"
	"github.com/labi-le/tidal-syncer/internal/store"
	synceng "github.com/labi-le/tidal-syncer/internal/sync"
	"github.com/labi-le/tidal-syncer/internal/tag"
	"github.com/labi-le/tidal-syncer/pkg/tidal"
)

const (
	testUserID  = "user-1"
	fixtureFLAC = "testdata/sample.flac"
	gotQuality  = "HI_RES_LOSSLESS"
	reqQuality  = "HI_RES_LOSSLESS"
	workerCount = 2

	albumOne = 201
	albumTwo = 202
	trackA   = 101
	trackB   = 102
	trackC   = 103

	coverOne    = "11111111-1111-1111-1111-111111111111"
	coverTwo    = "22222222-2222-2222-2222-222222222222"
	playlistOne = "playlist-1"
	playlistTwo = "playlist-2"
)

func TestSyncIdempotent(t *testing.T) {
	client := &fakeClient{
		userID:    testUserID,
		favTracks: []tidal.Track{makeTrack(trackA, albumOne, coverOne), makeTrack(trackB, albumOne, coverOne), makeTrack(trackC, albumTwo, coverTwo)},
		albums:    twoAlbums(),
		lyrics: map[string]tidal.Lyrics{
			strconv.Itoa(trackA): {Plain: "la la la", LRC: "[00:01.00]la la la"},
		},
	}
	dl := &fakeDownloader{src: fixtureFLAC, quality: gotQuality, failIDs: map[string]bool{}}
	covers := &fakeCovers{jpeg: minimalJPEG(t)}
	cfg := baseConfig(t, config.Scope{Favorites: config.Favorites{Tracks: true}})
	engine := newEngine(t, client, dl, covers, cfg)

	first, _, err := engine.SyncOnce(context.Background())
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	want := len(client.favTracks)
	if first.Downloaded != want || first.Failed != 0 {
		t.Fatalf("first run downloaded=%d failed=%d, want downloaded=%d failed=0", first.Downloaded, first.Failed, want)
	}

	second, _, err := engine.SyncOnce(context.Background())
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if second.Downloaded != 0 || second.Skipped != want {
		t.Fatalf("second run downloaded=%d skipped=%d, want downloaded=0 skipped=%d", second.Downloaded, second.Skipped, want)
	}
}

// TestSyncCarriesFavoriteAddedDateIntoSnapshot locks the favorite-ordering
// feature: the favorite-add timestamp the client reports for a track must surface
// on the matching snapshot item so the caller can persist and order the library
// by when each track was favorited.
func TestSyncCarriesFavoriteAddedDateIntoSnapshot(t *testing.T) {
	const addedAt = "2026-06-30T00:00:00.000+0000"
	client := &fakeClient{
		userID:        testUserID,
		favTracks:     []tidal.Track{makeTrack(trackA, albumOne, coverOne)},
		favTrackDates: map[int]string{trackA: addedAt},
		albums:        map[string]tidal.Album{strconv.Itoa(albumOne): makeAlbum(albumOne, coverOne)},
	}
	dl := &fakeDownloader{src: fixtureFLAC, quality: gotQuality, failIDs: map[string]bool{}}
	covers := &fakeCovers{jpeg: minimalJPEG(t)}
	cfg := baseConfig(t, config.Scope{Favorites: config.Favorites{Tracks: true}})
	engine := newEngine(t, client, dl, covers, cfg)

	_, snapshot, err := engine.SyncOnce(context.Background())
	if err != nil {
		t.Fatalf("sync: %v", err)
	}

	want := strconv.Itoa(trackA)
	found := -1
	for i := range snapshot {
		if snapshot[i].TidalID == want {
			found = i

			break
		}
	}
	if found < 0 {
		t.Fatalf("snapshot %+v missing item for track %s", snapshot, want)
	}
	if snapshot[found].AddedAt != addedAt {
		t.Fatalf("snapshot AddedAt = %q, want %q", snapshot[found].AddedAt, addedAt)
	}
}

// TestSyncSkipsSubRequestQualityOnSecondRun locks the no-churn guarantee: a track
// whose best available master (LOSSLESS) is below the requested HI_RES_LOSSLESS
// tier must be downloaded once and then skipped, not re-downloaded on every cycle
// just because the obtained tier can never reach the requested one.
func TestSyncSkipsSubRequestQualityOnSecondRun(t *testing.T) {
	client := &fakeClient{
		userID:    testUserID,
		favTracks: []tidal.Track{makeTrack(trackA, albumOne, coverOne)},
		albums:    map[string]tidal.Album{strconv.Itoa(albumOne): makeAlbum(albumOne, coverOne)},
	}
	dl := &fakeDownloader{src: fixtureFLAC, quality: "LOSSLESS", failIDs: map[string]bool{}}
	covers := &fakeCovers{jpeg: minimalJPEG(t)}
	cfg := baseConfig(t, config.Scope{Favorites: config.Favorites{Tracks: true}})
	engine := newEngine(t, client, dl, covers, cfg)

	if _, _, err := engine.SyncOnce(context.Background()); err != nil {
		t.Fatalf("first run: %v", err)
	}

	second, _, err := engine.SyncOnce(context.Background())
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if second.Downloaded != 0 || second.Skipped != 1 {
		t.Fatalf("second run downloaded=%d skipped=%d, want downloaded=0 skipped=1", second.Downloaded, second.Skipped)
	}
	if got := dl.countFor(strconv.Itoa(trackA)); got != 1 {
		t.Fatalf("download calls=%d, want 1 (sub-request track must not re-download)", got)
	}
}

func TestSyncPartialFailure(t *testing.T) {
	tracks := []tidal.Track{makeTrack(trackA, albumOne, coverOne), makeTrack(trackB, albumOne, coverOne), makeTrack(trackC, albumTwo, coverTwo)}
	client := &fakeClient{userID: testUserID, favTracks: tracks, albums: twoAlbums()}
	dl := &fakeDownloader{src: fixtureFLAC, quality: gotQuality, failIDs: map[string]bool{strconv.Itoa(trackB): true}}
	covers := &fakeCovers{jpeg: minimalJPEG(t)}
	cfg := baseConfig(t, config.Scope{Favorites: config.Favorites{Tracks: true}})
	engine := newEngine(t, client, dl, covers, cfg)

	summary, _, err := engine.SyncOnce(context.Background())
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if want := len(tracks) - 1; summary.Downloaded != want {
		t.Fatalf("downloaded=%d, want %d", summary.Downloaded, want)
	}
	if summary.Failed != 1 {
		t.Fatalf("failed=%d, want 1", summary.Failed)
	}
}

func TestSyncDedupTwoPlaylists(t *testing.T) {
	shared := makeTrack(trackA, albumOne, coverOne)
	client := &fakeClient{
		userID:       testUserID,
		favPlaylists: []tidal.Playlist{{UUID: playlistOne, Title: "P1"}, {UUID: playlistTwo, Title: "P2"}},
		playlistTracks: map[string][]tidal.Track{
			playlistOne: {shared},
			playlistTwo: {shared},
		},
		albums: map[string]tidal.Album{strconv.Itoa(albumOne): makeAlbum(albumOne, coverOne)},
	}
	dl := &fakeDownloader{src: fixtureFLAC, quality: gotQuality, failIDs: map[string]bool{}}
	covers := &fakeCovers{jpeg: minimalJPEG(t)}
	cfg := baseConfig(t, config.Scope{Favorites: config.Favorites{Playlists: true}})
	engine := newEngine(t, client, dl, covers, cfg)

	summary, _, err := engine.SyncOnce(context.Background())
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if summary.Downloaded != 1 {
		t.Fatalf("downloaded=%d, want 1", summary.Downloaded)
	}
	if got := dl.countFor(strconv.Itoa(trackA)); got != 1 {
		t.Fatalf("download calls=%d, want 1", got)
	}
}

func TestSyncCoverOncePerAlbum(t *testing.T) {
	client := &fakeClient{
		userID:    testUserID,
		favTracks: []tidal.Track{makeTrack(trackA, albumOne, coverOne), makeTrack(trackB, albumOne, coverOne), makeTrack(trackC, albumTwo, coverTwo)},
		albums:    twoAlbums(),
	}
	dl := &fakeDownloader{src: fixtureFLAC, quality: gotQuality, failIDs: map[string]bool{}}
	covers := &fakeCovers{jpeg: minimalJPEG(t)}
	cfg := baseConfig(t, config.Scope{Favorites: config.Favorites{Tracks: true}})
	engine := newEngine(t, client, dl, covers, cfg)

	summary, _, err := engine.SyncOnce(context.Background())
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if summary.Downloaded != len(client.favTracks) {
		t.Fatalf("downloaded=%d, want %d", summary.Downloaded, len(client.favTracks))
	}
	if got := covers.countFor(coverOne); got != 1 {
		t.Fatalf("album one cover fetches=%d, want 1", got)
	}
	if got := covers.countFor(coverTwo); got != 1 {
		t.Fatalf("album two cover fetches=%d, want 1", got)
	}
}

// TestSyncRemovesUnfavoritedTrack locks the caller-driven removal ordering: when a
// track leaves the favorites between runs, reconciling the second run's enumerated
// set against the prior snapshot (before that snapshot is refreshed) deletes the
// track's file under the mirror policy.
func TestSyncRemovesUnfavoritedTrack(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	client := &fakeClient{
		userID:    testUserID,
		favTracks: []tidal.Track{makeTrack(trackA, albumOne, coverOne)},
		albums:    map[string]tidal.Album{strconv.Itoa(albumOne): makeAlbum(albumOne, coverOne)},
	}
	dl := &fakeDownloader{src: fixtureFLAC, quality: gotQuality, failIDs: map[string]bool{}}
	covers := &fakeCovers{jpeg: minimalJPEG(t)}
	cfg := baseConfig(t, config.Scope{Favorites: config.Favorites{Tracks: true}})
	cfg.Removal = config.Removal{Policy: "mirror"}
	engine := synceng.NewEngine(synceng.Params{
		Client:     client,
		Downloader: dl,
		Covers:     covers,
		Store:      st,
		Config:     cfg,
		Logger:     zerolog.Nop(),
		Limiter:    rate.NewLimiter(rate.Inf, 1),
	})

	_, current, err := engine.SyncOnce(ctx)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if err = st.ReplaceSnapshot(ctx, "tracks", current); err != nil {
		t.Fatalf("persist first snapshot: %v", err)
	}

	track, err := st.GetTrack(ctx, strconv.Itoa(trackA))
	if err != nil {
		t.Fatalf("get track: %v", err)
	}
	if _, err = os.Stat(track.Path); err != nil {
		t.Fatalf("downloaded track should exist on disk: %v", err)
	}

	client.favTracks = nil
	_, current, err = engine.SyncOnce(ctx)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}

	remover := synceng.NewRemover(synceng.RemoverParams{Store: st, Config: cfg, Logger: zerolog.Nop()})
	if err = remover.Reconcile(ctx, current); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if err = st.ReplaceSnapshot(ctx, "tracks", current); err != nil {
		t.Fatalf("persist second snapshot: %v", err)
	}

	if _, err = os.Stat(track.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unfavorited track should be removed, stat err = %v", err)
	}
}

// newEngine wires an Engine over a fresh migrated store and an unlimited limiter.
func newEngine(
	t *testing.T, client synceng.TidalClient, dl synceng.Downloader, covers synceng.CoverFetcher, cfg config.Config,
) *synceng.Engine {
	t.Helper()

	return synceng.NewEngine(synceng.Params{
		Client:     client,
		Downloader: dl,
		Covers:     covers,
		Store:      newTestStore(t),
		Config:     cfg,
		Logger:     zerolog.Nop(),
		Limiter:    rate.NewLimiter(rate.Inf, 1),
	})
}

// newTestStore opens and migrates a store under a temporary directory.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err = st.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate store: %v", err)
	}

	return st
}

// baseConfig builds a config with a temp music root, the given scope, and lyrics enabled.
func baseConfig(t *testing.T, scope config.Scope) config.Config {
	t.Helper()

	cfg := config.Defaults()
	cfg.Paths.Music = t.TempDir()
	cfg.Concurrency = workerCount
	cfg.Quality.Request = reqQuality
	cfg.Scope = scope
	cfg.Lyrics = config.Lyrics{Embed: true, Sidecar: true}

	return cfg
}

// twoAlbums returns the album table shared by the multi-album tests.
func twoAlbums() map[string]tidal.Album {
	return map[string]tidal.Album{
		strconv.Itoa(albumOne): makeAlbum(albumOne, coverOne),
		strconv.Itoa(albumTwo): makeAlbum(albumTwo, coverTwo),
	}
}

// makeTrack builds a fully-populated track on the given album with a MAIN artist.
func makeTrack(id, albumID int, cover string) tidal.Track {
	return tidal.Track{
		ID:           id,
		Title:        fmt.Sprintf("Track %d", id),
		TrackNumber:  1,
		VolumeNumber: 1,
		ISRC:         fmt.Sprintf("US%010d", id),
		Copyright:    "(C) Label",
		Artists:      []tidal.Artist{{ID: 1, Name: "Artist", Type: "MAIN"}},
		Album:        tidal.AlbumRef{ID: albumID, Title: fmt.Sprintf("Album %d", albumID), Cover: cover},
	}
}

// makeAlbum builds a single-volume album record with the given cover uuid.
func makeAlbum(id int, cover string) tidal.Album {
	return tidal.Album{
		ID:              id,
		Title:           fmt.Sprintf("Album %d", id),
		NumberOfTracks:  1,
		NumberOfVolumes: 1,
		ReleaseDate:     "2020-01-01",
		Copyright:       "(C) Label",
		Artists:         []tidal.Artist{{ID: 1, Name: "Artist", Type: "MAIN"}},
		Cover:           cover,
	}
}

func TestSyncBackfillsGenreFromFileTags(t *testing.T) {
	// Given a done track stored without a genre whose FLAC carries GENRE tags.
	ctx := context.Background()
	st := newTestStore(t)
	path := filepath.Join(t.TempDir(), "x.flac")
	data, err := os.ReadFile(fixtureFLAC)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err = os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write copy: %v", err)
	}
	if err = tag.TagFile(path, tag.Meta{Title: "T", Genre: []string{"Rock", "Metal"}}, nil, ""); err != nil {
		t.Fatalf("tag fixture: %v", err)
	}
	if err = st.MarkTrack(ctx, store.Track{
		TidalID:          strconv.Itoa(trackA),
		Path:             path,
		ObtainedQuality:  gotQuality,
		RequestedQuality: reqQuality,
		Status:           store.StatusDone,
	}); err != nil {
		t.Fatalf("seed track: %v", err)
	}
	engine := synceng.NewEngine(synceng.Params{
		Store:      st,
		Client:     &fakeClient{userID: testUserID},
		Downloader: &fakeDownloader{src: fixtureFLAC, failIDs: map[string]bool{}},
		Covers:     &fakeCovers{jpeg: minimalJPEG(t)},
		Config:     baseConfig(t, config.Scope{Favorites: config.Favorites{Tracks: true}}),
		Logger:     zerolog.Nop(),
		Limiter:    rate.NewLimiter(rate.Inf, 1),
	})

	// When a sync runs: empty favorites mean no downloads, but backfill runs first.
	if _, _, err = engine.SyncOnce(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}

	// Then the genre column is backfilled from the file's Vorbis tags.
	got, err := st.GetTrack(ctx, strconv.Itoa(trackA))
	if err != nil {
		t.Fatalf("get track: %v", err)
	}
	if got.Genre != "Rock;Metal" {
		t.Fatalf("genre = %q, want %q", got.Genre, "Rock;Metal")
	}
}
