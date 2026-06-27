package tidal_test

import (
	"context"
	"iter"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/labi-le/tidal-syncer/pkg/tidal"
)

// Item-expansion fixtures. staticTokens and testRPM are defined in the sibling
// client_test.go; favTotal, wantPagingRequests and writeFavoritesPage in
// favorites_test.go; favUserID and albumID in endpoints_test.go (same tidal_test
// package). The album- and playlist-items endpoints return the identical paged
// {item:{...}} envelope the favorites stream consumes, so that page writer
// doubles as their fixture. albumItemsPath embeds albumID's value ("99").
const (
	playlistUUID      = "pl-uuid-1"
	albumItemsPath    = "/albums/99/items"
	playlistItemsPath = "/playlists/pl-uuid-1/items"
)

func TestAlbumTracksPagingStreamsAllPagesWithoutDuplicates(t *testing.T) {
	t.Parallel()

	assertTrackStreamPagesCleanly(t, albumItemsPath, func(c *tidal.Client) iter.Seq2[tidal.Track, error] {
		return c.AlbumTracks(context.Background(), albumID)
	})
}

func TestPlaylistTracksPagingStreamsAllPagesWithoutDuplicates(t *testing.T) {
	t.Parallel()

	assertTrackStreamPagesCleanly(t, playlistItemsPath, func(c *tidal.Client) iter.Seq2[tidal.Track, error] {
		return c.PlaylistTracks(context.Background(), playlistUUID)
	})
}

// assertTrackStreamPagesCleanly drives stream against a 250-item collection
// served as 100-item pages (3 requests) and asserts every track is yielded
// exactly once, in ascending order, hitting wantPath with no duplicate or
// missing item. It reuses writeFavoritesPage because the items endpoints share
// the favorites page envelope.
func assertTrackStreamPagesCleanly(
	t *testing.T, wantPath string, stream func(c *tidal.Client) iter.Seq2[tidal.Track, error],
) {
	t.Helper()

	var requests atomic.Int32
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		gotPath = r.URL.Path
		writeFavoritesPage(w, r, favTotal)
	}))
	defer srv.Close()

	client := tidal.New(
		staticTokens{userID: favUserID},
		tidal.WithBaseURL(srv.URL),
		tidal.WithRequestsPerMinute(testRPM),
	)

	seen := make(map[int]struct{}, favTotal)
	want := 0
	for track, err := range stream(client) {
		if err != nil {
			t.Fatalf("stream: unexpected error: %v", err)
		}
		if _, dup := seen[track.ID]; dup {
			t.Fatalf("duplicate track id %d streamed", track.ID)
		}
		seen[track.ID] = struct{}{}
		if track.ID != want {
			t.Errorf("track order: got id %d want %d", track.ID, want)
		}
		want++
	}

	if len(seen) != favTotal {
		t.Fatalf("streamed %d tracks, want %d (no missing items)", len(seen), favTotal)
	}
	if got := requests.Load(); got != wantPagingRequests {
		t.Fatalf("server requests: got %d want %d (must page, not buffer all)", got, wantPagingRequests)
	}
	if gotPath != wantPath {
		t.Errorf("items path: got %q want %q", gotPath, wantPath)
	}
}
