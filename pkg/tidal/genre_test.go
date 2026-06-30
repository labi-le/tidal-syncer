package tidal_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/labi-le/tidal-syncer/pkg/tidal"
)

func TestTrackGenres_parsesIncludedGenreNames(t *testing.T) {
	t.Parallel()

	var gotPath, gotInclude, gotCountry, gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotInclude = r.URL.Query().Get("include")
		gotCountry = r.URL.Query().Get("countryCode")
		gotAccept = r.Header.Get("Accept")
		_, _ = io.WriteString(w, `{
			"data": {"id": "123", "type": "tracks"},
			"included": [
				{"id": "7", "type": "genres", "attributes": {"genreName": "Rock"}},
				{"id": "8", "type": "genres", "attributes": {"genreName": "Hard Rock"}},
				{"id": "99", "type": "artists", "attributes": {"name": "Ignored"}}
			]
		}`)
	}))
	defer srv.Close()

	client := tidal.New(
		staticTokens{access: "tok", countryCode: "BR", userID: "u"},
		tidal.WithV2BaseURL(srv.URL),
		tidal.WithRequestsPerMinute(testRPM),
	)

	genres, err := client.TrackGenres(context.Background(), "123")
	if err != nil {
		t.Fatalf("TrackGenres: unexpected error: %v", err)
	}

	if want := []string{"Rock", "Hard Rock"}; !reflect.DeepEqual(genres, want) {
		t.Errorf("genres: got %v want %v", genres, want)
	}
	if gotPath != "/tracks/123" {
		t.Errorf("path: got %q want %q", gotPath, "/tracks/123")
	}
	if gotInclude != "genres" {
		t.Errorf("include param: got %q want %q", gotInclude, "genres")
	}
	if gotCountry != "BR" {
		t.Errorf("countryCode param: got %q want %q", gotCountry, "BR")
	}
	if gotAccept != "application/vnd.api+json" {
		t.Errorf("Accept header: got %q want %q", gotAccept, "application/vnd.api+json")
	}
}

func TestTrackGenres_emptyWhenNoGenres(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data": {"id": "5", "type": "tracks"}}`)
	}))
	defer srv.Close()

	client := tidal.New(
		staticTokens{access: "tok", countryCode: "US", userID: "u"},
		tidal.WithV2BaseURL(srv.URL),
		tidal.WithRequestsPerMinute(testRPM),
	)

	genres, err := client.TrackGenres(context.Background(), "5")
	if err != nil {
		t.Fatalf("TrackGenres: unexpected error: %v", err)
	}
	if len(genres) != 0 {
		t.Errorf("genres: got %v want empty", genres)
	}
}
