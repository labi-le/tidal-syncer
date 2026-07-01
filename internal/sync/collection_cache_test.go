package sync_test

import (
	"context"
	"slices"
	"testing"

	"github.com/rs/zerolog"
	"golang.org/x/time/rate"

	"github.com/labi-le/tidal-syncer/internal/config"
	"github.com/labi-le/tidal-syncer/internal/store"
	synceng "github.com/labi-le/tidal-syncer/internal/sync"
	"github.com/labi-le/tidal-syncer/pkg/tidal"
)

func snapshotContains(items []store.SnapshotItem, id string) bool {
	for _, item := range items {
		if item.TidalID == id {
			return true
		}
	}

	return false
}

func Test_Engine_caches_album_expansion_across_cycles(t *testing.T) {
	// Given a favorited single-track album and an empty cache
	ctx := context.Background()
	fc := &fakeClient{
		userID:      testUserID,
		favAlbums:   []tidal.Album{makeAlbum(albumOne, coverOne)},
		albumTracks: map[string][]tidal.Track{"201": {makeTrack(trackA, albumOne, coverOne)}},
		albums:      map[string]tidal.Album{"201": makeAlbum(albumOne, coverOne)},
	}
	dl := &fakeDownloader{src: fixtureFLAC, quality: gotQuality, failIDs: map[string]bool{}}
	covers := &fakeCovers{jpeg: minimalJPEG(t)}
	cfg := baseConfig(t, config.Scope{Favorites: config.Favorites{Albums: true}})
	st := newTestStore(t)
	engine := synceng.NewEngine(synceng.Params{
		Client: fc, Downloader: dl, Covers: covers, Store: st,
		Config: cfg, Logger: zerolog.Nop(), Limiter: rate.NewLimiter(rate.Inf, 1),
	})

	// When syncing twice
	if _, _, err := engine.SyncOnce(ctx); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	_, current, err := engine.SyncOnce(ctx)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}

	// Then the album is expanded from TIDAL only once; the second cycle serves it from cache
	if got := fc.countAlbumTracks("201"); got != 1 {
		t.Errorf("AlbumTracks fetch count = %d, want 1", got)
	}
	cached, ok, err := st.GetCollection(ctx, "album", "201")
	if err != nil {
		t.Fatalf("get collection: %v", err)
	}
	if !ok {
		t.Fatal("expected album 201 to be cached")
	}
	if !slices.Equal(cached.TrackIDs, []int{101}) {
		t.Errorf("cached TrackIDs = %v, want [101]", cached.TrackIDs)
	}
	if !snapshotContains(current, "101") {
		t.Errorf("second-cycle snapshot missing track 101: %+v", current)
	}
}

func Test_Engine_refetches_playlist_when_version_changes(t *testing.T) {
	// Given a favorited single-track playlist at content version v1
	ctx := context.Background()
	fc := &fakeClient{
		userID:         testUserID,
		favPlaylists:   []tidal.Playlist{{UUID: playlistOne, NumberOfTracks: 1, LastUpdated: "v1"}},
		playlistTracks: map[string][]tidal.Track{playlistOne: {makeTrack(trackA, albumOne, coverOne)}},
		albums:         map[string]tidal.Album{"201": makeAlbum(albumOne, coverOne)},
	}
	dl := &fakeDownloader{src: fixtureFLAC, quality: gotQuality, failIDs: map[string]bool{}}
	covers := &fakeCovers{jpeg: minimalJPEG(t)}
	cfg := baseConfig(t, config.Scope{Favorites: config.Favorites{Playlists: true}})
	st := newTestStore(t)
	engine := synceng.NewEngine(synceng.Params{
		Client: fc, Downloader: dl, Covers: covers, Store: st,
		Config: cfg, Logger: zerolog.Nop(), Limiter: rate.NewLimiter(rate.Inf, 1),
	})

	// When syncing twice at v1, then once after content changes to v2
	if _, _, err := engine.SyncOnce(ctx); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if _, _, err := engine.SyncOnce(ctx); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if got := fc.countPlaylistTracks(playlistOne); got != 1 {
		t.Fatalf("unchanged playlist fetched %d times, want 1", got)
	}
	fc.favPlaylists[0].LastUpdated = "v2"
	if _, _, err := engine.SyncOnce(ctx); err != nil {
		t.Fatalf("third sync: %v", err)
	}

	// Then the version bump invalidates the cache and forces exactly one refetch
	if got := fc.countPlaylistTracks(playlistOne); got != 2 {
		t.Errorf("PlaylistTracks fetch count = %d, want 2", got)
	}
}

func Test_Engine_refetches_album_with_unsettled_tracks(t *testing.T) {
	// Given a valid album cache entry whose track has never been downloaded
	ctx := context.Background()
	fc := &fakeClient{
		userID:      testUserID,
		favAlbums:   []tidal.Album{makeAlbum(albumOne, coverOne)},
		albumTracks: map[string][]tidal.Track{"201": {makeTrack(trackA, albumOne, coverOne)}},
		albums:      map[string]tidal.Album{"201": makeAlbum(albumOne, coverOne)},
	}
	dl := &fakeDownloader{src: fixtureFLAC, quality: gotQuality, failIDs: map[string]bool{}}
	covers := &fakeCovers{jpeg: minimalJPEG(t)}
	cfg := baseConfig(t, config.Scope{Favorites: config.Favorites{Albums: true}})
	st := newTestStore(t)
	if err := st.PutCollection(ctx, "album", "201", "", []int{101}); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	engine := synceng.NewEngine(synceng.Params{
		Client: fc, Downloader: dl, Covers: covers, Store: st,
		Config: cfg, Logger: zerolog.Nop(), Limiter: rate.NewLimiter(rate.Inf, 1),
	})

	// When syncing with the cache valid but no track settled on disk
	if _, _, err := engine.SyncOnce(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}

	// Then the album is refetched because its track is not yet downloaded
	if got := fc.countAlbumTracks("201"); got != 1 {
		t.Errorf("AlbumTracks fetch count = %d, want 1 (refetch on unsettled)", got)
	}
}

func Test_Engine_prunes_unfavorited_album_cache(t *testing.T) {
	// Given two favorited single-track albums cached on the first cycle
	ctx := context.Background()
	fc := &fakeClient{
		userID:    testUserID,
		favAlbums: []tidal.Album{makeAlbum(albumOne, coverOne), makeAlbum(albumTwo, coverTwo)},
		albumTracks: map[string][]tidal.Track{
			"201": {makeTrack(trackA, albumOne, coverOne)},
			"202": {makeTrack(trackB, albumTwo, coverTwo)},
		},
		albums: twoAlbums(),
	}
	dl := &fakeDownloader{src: fixtureFLAC, quality: gotQuality, failIDs: map[string]bool{}}
	covers := &fakeCovers{jpeg: minimalJPEG(t)}
	cfg := baseConfig(t, config.Scope{Favorites: config.Favorites{Albums: true}})
	st := newTestStore(t)
	engine := synceng.NewEngine(synceng.Params{
		Client: fc, Downloader: dl, Covers: covers, Store: st,
		Config: cfg, Logger: zerolog.Nop(), Limiter: rate.NewLimiter(rate.Inf, 1),
	})

	if _, _, err := engine.SyncOnce(ctx); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if _, ok, err := st.GetCollection(ctx, "album", "202"); err != nil || !ok {
		t.Fatalf("album 202 should be cached after first sync (ok=%v err=%v)", ok, err)
	}

	// When album 202 is unfavorited and we sync again
	fc.favAlbums = []tidal.Album{makeAlbum(albumOne, coverOne)}
	_, current, err := engine.SyncOnce(ctx)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}

	// Then the stale cache row is pruned and its track leaves the snapshot
	_, ok, err := st.GetCollection(ctx, "album", "202")
	if err != nil || ok {
		t.Errorf("album 202 cache not pruned (ok=%v err=%v)", ok, err)
	}
	if !snapshotContains(current, "101") {
		t.Errorf("snapshot missing album 201 track 101: %+v", current)
	}
	if snapshotContains(current, "102") {
		t.Errorf("snapshot still contains unfavorited track 102: %+v", current)
	}
}

func Test_Engine_caches_album_when_advertised_count_exceeds_yield(t *testing.T) {
	// Given a favorited album advertising 2 tracks whose item listing yields only 1
	// (e.g. a region-unavailable sibling omitted), against an empty cache
	ctx := context.Background()
	advertised := makeAlbum(albumOne, coverOne)
	advertised.NumberOfTracks = 2
	fc := &fakeClient{
		userID:      testUserID,
		favAlbums:   []tidal.Album{advertised},
		albumTracks: map[string][]tidal.Track{"201": {makeTrack(trackA, albumOne, coverOne)}},
		albums:      map[string]tidal.Album{"201": makeAlbum(albumOne, coverOne)},
	}
	dl := &fakeDownloader{src: fixtureFLAC, quality: gotQuality, failIDs: map[string]bool{}}
	covers := &fakeCovers{jpeg: minimalJPEG(t)}
	cfg := baseConfig(t, config.Scope{Favorites: config.Favorites{Albums: true}})
	st := newTestStore(t)
	engine := synceng.NewEngine(synceng.Params{
		Client: fc, Downloader: dl, Covers: covers, Store: st,
		Config: cfg, Logger: zerolog.Nop(), Limiter: rate.NewLimiter(rate.Inf, 1),
	})

	// When syncing twice
	if _, _, err := engine.SyncOnce(ctx); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if _, _, err := engine.SyncOnce(ctx); err != nil {
		t.Fatalf("second sync: %v", err)
	}

	// Then the immutable album still serves from cache on the second cycle even though
	// its advertised track count never equals the number of tracks actually yielded
	if got := fc.countAlbumTracks("201"); got != 1 {
		t.Errorf("AlbumTracks fetch count = %d, want 1 (cache must survive advertised!=yielded)", got)
	}
}
