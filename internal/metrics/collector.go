package metrics

import (
	"context"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/labi-le/tidal-syncer/internal/store"
)

// genreSeparator splits the semicolon-joined genre column; it mirrors
// internal/sync's genreSeparator so a multi-genre track counts once per genre.
const genreSeparator = ";"

// scrapeTimeout bounds the store queries a single scrape performs.
const scrapeTimeout = 5 * time.Second

// StatsQuerier is the read-only store surface the library collector needs. The
// concrete *store.Store satisfies it.
type StatsQuerier interface {
	LibraryCounts(ctx context.Context) (store.LibraryCounts, error)
	DoneTrackFacets(ctx context.Context) ([]store.TrackFacet, error)
}

// LibraryCollector reports the current library composition as Prometheus gauges,
// querying the store fresh on every scrape. A scrape that fails to read the
// store reports library_scrape_error=1 and emits no stale library gauges.
type LibraryCollector struct {
	q         StatsQuerier
	musicRoot string

	tracks      *prometheus.Desc
	byQuality   *prometheus.Desc
	byGenre     *prometheus.Desc
	favorites   *prometheus.Desc
	permanent   *prometheus.Desc
	albums      *prometheus.Desc
	artists     *prometheus.Desc
	lastUpdate  *prometheus.Desc
	scrapeError *prometheus.Desc
}

// NewLibraryCollector builds a collector over q. musicRoot is the configured
// music directory, used to recover the album-artist from each track's path.
func NewLibraryCollector(q StatsQuerier, musicRoot string) *LibraryCollector {
	return &LibraryCollector{
		q:         q,
		musicRoot: musicRoot,
		tracks: prometheus.NewDesc(
			namespace+"_tracks",
			"Number of cached tracks by download status.",
			[]string{"status"}, nil,
		),
		byQuality: prometheus.NewDesc(
			namespace+"_tracks_by_quality",
			"Number of downloaded tracks by obtained quality tier.",
			[]string{"quality"}, nil,
		),
		byGenre: prometheus.NewDesc(
			namespace+"_tracks_by_genre",
			"Number of downloaded tracks tagged with each genre.",
			[]string{"genre"}, nil,
		),
		favorites: prometheus.NewDesc(
			namespace+"_favorites",
			"Number of favorited tracks in the last snapshot.",
			nil, nil,
		),
		permanent: prometheus.NewDesc(
			namespace+"_permanent_failures",
			"Number of tracks that failed permanently.",
			nil, nil,
		),
		albums: prometheus.NewDesc(
			namespace+"_distinct_albums",
			"Number of distinct albums with at least one downloaded track.",
			nil, nil,
		),
		artists: prometheus.NewDesc(
			namespace+"_distinct_artists",
			"Number of distinct album-artists with at least one downloaded track.",
			nil, nil,
		),
		lastUpdate: prometheus.NewDesc(
			namespace+"_last_track_update_timestamp_seconds",
			"Unix timestamp of the most recent track state change.",
			nil, nil,
		),
		scrapeError: prometheus.NewDesc(
			namespace+"_library_scrape_error",
			"1 when the most recent library scrape failed to read the store, else 0.",
			nil, nil,
		),
	}
}

// Describe implements prometheus.Collector.
func (c *LibraryCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.tracks
	ch <- c.byQuality
	ch <- c.byGenre
	ch <- c.favorites
	ch <- c.permanent
	ch <- c.albums
	ch <- c.artists
	ch <- c.lastUpdate
	ch <- c.scrapeError
}

// Collect implements prometheus.Collector, reading the store once per scrape.
func (c *LibraryCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), scrapeTimeout)
	defer cancel()

	counts, err := c.q.LibraryCounts(ctx)
	if err != nil {
		ch <- prometheus.MustNewConstMetric(c.scrapeError, prometheus.GaugeValue, 1)

		return
	}
	facets, err := c.q.DoneTrackFacets(ctx)
	if err != nil {
		ch <- prometheus.MustNewConstMetric(c.scrapeError, prometheus.GaugeValue, 1)

		return
	}
	ch <- prometheus.MustNewConstMetric(c.scrapeError, prometheus.GaugeValue, 0)

	c.collectCounts(ch, counts)
	c.collectFacets(ch, facets)
}

func (c *LibraryCollector) collectCounts(ch chan<- prometheus.Metric, counts store.LibraryCounts) {
	for status, n := range counts.ByStatus {
		ch <- prometheus.MustNewConstMetric(c.tracks, prometheus.GaugeValue, float64(n), status)
	}
	for quality, n := range counts.ByObtainedQuality {
		label := quality
		if label == "" {
			label = "unknown"
		}
		ch <- prometheus.MustNewConstMetric(c.byQuality, prometheus.GaugeValue, float64(n), label)
	}
	ch <- prometheus.MustNewConstMetric(c.favorites, prometheus.GaugeValue, float64(counts.Favorites))
	ch <- prometheus.MustNewConstMetric(c.permanent, prometheus.GaugeValue, float64(counts.PermanentFailures))
	ch <- prometheus.MustNewConstMetric(c.albums, prometheus.GaugeValue, float64(counts.DistinctAlbums))
	ch <- prometheus.MustNewConstMetric(c.lastUpdate, prometheus.GaugeValue, float64(counts.LastSyncUnix))
}

func (c *LibraryCollector) collectFacets(ch chan<- prometheus.Metric, facets []store.TrackFacet) {
	genres := make(map[string]int)
	artists := make(map[string]struct{})
	for _, f := range facets {
		for genre := range strings.SplitSeq(f.Genre, genreSeparator) {
			if g := strings.TrimSpace(genre); g != "" {
				genres[g]++
			}
		}
		if a := albumArtist(f.Path, c.musicRoot); a != "" {
			artists[a] = struct{}{}
		}
	}
	for genre, n := range genres {
		ch <- prometheus.MustNewConstMetric(c.byGenre, prometheus.GaugeValue, float64(n), genre)
	}
	ch <- prometheus.MustNewConstMetric(c.artists, prometheus.GaugeValue, float64(len(artists)))
}

// albumArtist recovers the album-artist (the first path component beneath the
// music root) from a rendered track path. It returns "" when the path does not
// sit under the music root or has no artist/track structure.
func albumArtist(path, musicRoot string) string {
	rel := strings.TrimPrefix(path, strings.TrimRight(musicRoot, "/")+"/")
	if rel == path {
		return "" // path is not under the music root
	}
	rel = strings.TrimPrefix(rel, "/")
	first, rest, ok := strings.Cut(rel, "/")
	if !ok || first == "" || rest == "" {
		return "" // no <artist>/<...> structure
	}

	return first
}
