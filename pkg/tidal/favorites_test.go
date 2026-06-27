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
	for track, err := range client.FavoriteTracks(context.Background()) {
		if err != nil {
			t.Fatalf("FavoriteTracks: unexpected error: %v", err)
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
