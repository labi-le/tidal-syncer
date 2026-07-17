package metrics

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/labi-le/tidal-syncer/internal/store"
)

// fakeQuerier is a StatsQuerier that returns canned data or an error.
type fakeQuerier struct {
	counts store.LibraryCounts
	facets []store.TrackFacet
	err    error
}

func (f fakeQuerier) LibraryCounts(context.Context) (store.LibraryCounts, error) {
	return f.counts, f.err
}

func (f fakeQuerier) DoneTrackFacets(context.Context) ([]store.TrackFacet, error) {
	if f.err != nil {
		return nil, f.err
	}

	return f.facets, nil
}

func TestAlbumArtist(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, path, root, want string
	}{
		{"under root", "/app/Music/Korn/Issues/01 - Dead.flac", "/app/Music", "Korn"},
		{"trailing slash root", "/app/Music/Korn/Issues/01 - Dead.flac", "/app/Music/", "Korn"},
		{"not under root", "/other/Korn/Issues/01.flac", "/app/Music", ""},
		{"root only", "/app/Music/01.flac", "/app/Music", ""},
		{"empty path", "", "/app/Music", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := albumArtist(tt.path, tt.root); got != tt.want {
				t.Fatalf("albumArtist(%q, %q) = %q, want %q", tt.path, tt.root, got, tt.want)
			}
		})
	}
}

func TestLibraryCollectorReportsGauges(t *testing.T) {
	t.Parallel()

	q := fakeQuerier{
		counts: store.LibraryCounts{
			ByStatus:          map[string]int{"done": 2, "failed": 1},
			ByObtainedQuality: map[string]int{"LOSSLESS": 2},
			Favorites:         5,
			PermanentFailures: 1,
			DistinctAlbums:    2,
			LastSyncUnix:      1700000000,
		},
		facets: []store.TrackFacet{
			{Genre: "Rock;Metal", Path: "/m/Korn/Issues/01.flac"},
			{Genre: "Rock", Path: "/m/Tool/Lateralus/02.flac"},
		},
		err: nil,
	}
	c := NewLibraryCollector(q, "/m")

	want := `
# HELP tidal_syncer_favorites Number of favorited tracks in the last snapshot.
# TYPE tidal_syncer_favorites gauge
tidal_syncer_favorites 5
# HELP tidal_syncer_tracks_by_genre Number of downloaded tracks tagged with each genre.
# TYPE tidal_syncer_tracks_by_genre gauge
tidal_syncer_tracks_by_genre{genre="Metal"} 1
tidal_syncer_tracks_by_genre{genre="Rock"} 2
# HELP tidal_syncer_distinct_artists Number of distinct album-artists with at least one downloaded track.
# TYPE tidal_syncer_distinct_artists gauge
tidal_syncer_distinct_artists 2
# HELP tidal_syncer_library_scrape_error 1 when the most recent library scrape failed to read the store, else 0.
# TYPE tidal_syncer_library_scrape_error gauge
tidal_syncer_library_scrape_error 0
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(want),
		"tidal_syncer_favorites",
		"tidal_syncer_tracks_by_genre",
		"tidal_syncer_distinct_artists",
		"tidal_syncer_library_scrape_error",
	); err != nil {
		t.Fatalf("collect mismatch: %v", err)
	}
}

func TestLibraryCollectorReportsScrapeError(t *testing.T) {
	t.Parallel()

	c := NewLibraryCollector(fakeQuerier{err: errors.New("db down")}, "/m") //nolint:exhaustruct // only err drives this case

	want := `
# HELP tidal_syncer_library_scrape_error 1 when the most recent library scrape failed to read the store, else 0.
# TYPE tidal_syncer_library_scrape_error gauge
tidal_syncer_library_scrape_error 1
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(want), "tidal_syncer_library_scrape_error"); err != nil {
		t.Fatalf("collect mismatch: %v", err)
	}
	// No library gauges are emitted on a failed scrape.
	if n := testutil.CollectAndCount(c, "tidal_syncer_favorites"); n != 0 {
		t.Fatalf("favorites emitted on failed scrape: %d", n)
	}
}

func TestMetricsRecordCycle(t *testing.T) {
	t.Parallel()

	m := New("v1.2.3", "abc123", noopCollector{})
	m.RecordCycle(2*time.Second, "")
	m.RecordCycle(time.Second, ClassLock)
	m.RecordCycle(time.Second, ClassTransient)

	if got := testutil.ToFloat64(m.cycles); got != 3 {
		t.Errorf("cycles = %v, want 3", got)
	}
	if got := testutil.ToFloat64(m.errors.WithLabelValues(ClassLock)); got != 1 {
		t.Errorf("errors{lock} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.errors.WithLabelValues(ClassReauth)); got != 0 {
		t.Errorf("errors{reauth} = %v, want 0 (pre-initialised, no failures)", got)
	}
	if got := testutil.ToFloat64(m.lastSuccess); got == 0 {
		t.Error("last-success gauge not stamped after a successful cycle")
	}
}

func TestMetricsRecordCycleNilSafe(t *testing.T) {
	t.Parallel()

	var m *Metrics
	m.RecordCycle(time.Second, ClassLock) // must not panic
}

// noopCollector is an unchecked collector standing in for the library collector
// where only the runtime metrics are under test.
type noopCollector struct{}

func (noopCollector) Describe(chan<- *prometheus.Desc) {}
func (noopCollector) Collect(chan<- prometheus.Metric) {}

func TestNewServerRouting(t *testing.T) {
	t.Parallel()

	const marker = "METRICS-BODY"
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(marker))
	})
	srv := NewServer(":0", h)

	cases := []struct {
		path       string
		wantStatus int
		wantBody   string
	}{
		{MetricsPath, http.StatusOK, marker},
		{"/", http.StatusOK, "tidal-syncer"},
		{"/nope", http.StatusNotFound, ""},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			srv.Handler.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("%s status = %d, want %d", tc.path, rec.Code, tc.wantStatus)
			}
			if tc.wantBody != "" && !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Fatalf("%s body = %q, want contains %q", tc.path, rec.Body.String(), tc.wantBody)
			}
		})
	}
}
