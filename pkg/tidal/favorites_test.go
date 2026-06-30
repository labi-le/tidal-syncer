package tidal_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/labi-le/tidal-syncer/pkg/tidal"
)

// Favorites paging fixtures. staticTokens and testRPM are defined in the sibling
// client_test.go, and favUserID in endpoints_test.go (same tidal_test package).
// favTracksPath embeds favUserID's value ("u-1").
const (
	favTracksPath      = "/users/u-1/favorites/tracks"
	favTotal           = 250
	wantPagingRequests = 3
	bigTotal           = 10000
)

// writeFavoritesPage emulates the TIDAL favorites endpoint: it honors the
// limit/offset query and returns the matching slice of {item:{id,title}} entries
// alongside the constant totalNumberOfItems, so a client must page to drain it.
func writeFavoritesPage(w http.ResponseWriter, r *http.Request, total int) {
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	end := min(offset+limit, total)

	items := make([]map[string]any, 0, max(0, end-offset))
	for id := offset; id < end; id++ {
		items = append(items, map[string]any{
			"item": map[string]any{"id": id, "title": "T" + strconv.Itoa(id)},
		})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"limit":              limit,
		"offset":             offset,
		"totalNumberOfItems": total,
		"items":              items,
	})
}

func TestFavoritesPagingStreamsAllPagesWithoutDuplicates(t *testing.T) {
	t.Parallel()

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
	for fav, err := range client.FavoriteTracks(context.Background()) {
		if err != nil {
			t.Fatalf("FavoriteTracks: unexpected error: %v", err)
		}
		if _, dup := seen[fav.Track.ID]; dup {
			t.Fatalf("duplicate track id %d streamed", fav.Track.ID)
		}
		seen[fav.Track.ID] = struct{}{}
		if fav.Track.ID != want {
			t.Errorf("track order: got id %d want %d", fav.Track.ID, want)
		}
		want++
	}

	if len(seen) != favTotal {
		t.Fatalf("streamed %d tracks, want %d (no missing items)", len(seen), favTotal)
	}
	if got := requests.Load(); got != wantPagingRequests {
		t.Fatalf("server requests: got %d want %d (must page, not buffer all)", got, wantPagingRequests)
	}
	if gotPath != favTracksPath {
		t.Errorf("favorites path: got %q want %q", gotPath, favTracksPath)
	}
}

func TestFavoritesPagingIsLazyAndDoesNotDrainTheLibrary(t *testing.T) {
	t.Parallel()

	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		writeFavoritesPage(w, r, bigTotal)
	}))
	defer srv.Close()

	client := tidal.New(
		staticTokens{userID: favUserID},
		tidal.WithBaseURL(srv.URL),
		tidal.WithRequestsPerMinute(testRPM),
	)

	count := 0
	for _, err := range client.FavoriteTracks(context.Background()) {
		if err != nil {
			t.Fatalf("FavoriteTracks: unexpected error: %v", err)
		}
		count++
		break
	}

	if count != 1 {
		t.Fatalf("read %d tracks, want exactly 1 before break", count)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("server requests: got %d want 1 (iterator must be lazy, never prefetch %d items)", got, bigTotal)
	}
}

func TestFavoriteTracksCarriesAddedAtFromCreated(t *testing.T) {
	t.Parallel()

	// Given a favorites page whose entries carry explicit per-favorite created timestamps
	const (
		firstCreated  = "2024-04-12T21:51:19.759+0000"
		secondCreated = "2026-06-30T13:15:45.438+0000"
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"limit":              2,
			"offset":             0,
			"totalNumberOfItems": 2,
			"items": []map[string]any{
				{"created": firstCreated, "item": map[string]any{"id": 10, "title": "First"}},
				{"created": secondCreated, "item": map[string]any{"id": 20, "title": "Second"}},
			},
		})
	}))
	defer srv.Close()

	client := tidal.New(
		staticTokens{userID: favUserID},
		tidal.WithBaseURL(srv.URL),
		tidal.WithRequestsPerMinute(testRPM),
	)

	// When the favorites are streamed
	var got []tidal.FavoriteTrack
	for fav, err := range client.FavoriteTracks(context.Background()) {
		if err != nil {
			t.Fatalf("FavoriteTracks: unexpected error: %v", err)
		}
		got = append(got, fav)
	}

	// Then each track carries the add timestamp from its entry's created field
	if len(got) != 2 {
		t.Fatalf("streamed %d tracks, want 2", len(got))
	}
	if got[0].Track.ID != 10 || got[0].AddedAt != firstCreated {
		t.Errorf("first = {id:%d added:%q}, want {id:10 added:%q}", got[0].Track.ID, got[0].AddedAt, firstCreated)
	}
	if got[1].Track.ID != 20 || got[1].AddedAt != secondCreated {
		t.Errorf("second = {id:%d added:%q}, want {id:20 added:%q}", got[1].Track.ID, got[1].AddedAt, secondCreated)
	}
}
