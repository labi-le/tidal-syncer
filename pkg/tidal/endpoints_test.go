package tidal_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labi-le/tidal-syncer/pkg/tidal"
)

// Single-resource endpoint fixtures. staticTokens and testRPM are defined in the
// sibling client_test.go (same tidal_test package). favUserID is shared with
// favorites_test.go.
const (
	favUserID       = "u-1"
	trackID         = "12345"
	trackIDNum      = 12345
	albumID         = "99"
	albumTrackCount = 10
	unavailableID   = "999"
	qualityLossless = "LOSSLESS"
	coverSize       = 640
)

func TestLyricsLRCRemovesTrailingSpaceAfterTimestamp(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w,
			`{"lyrics":"first line\nsecond line","subtitles":"[00:12.34] Hello\n[01:02.789] World"}`)
	}))
	defer srv.Close()

	client := tidal.New(
		staticTokens{userID: favUserID},
		tidal.WithBaseURL(srv.URL),
		tidal.WithRequestsPerMinute(testRPM),
	)

	lyrics, err := client.Lyrics(context.Background(), trackID)
	if err != nil {
		t.Fatalf("Lyrics: unexpected error: %v", err)
	}
	if lyrics.Plain != "first line\nsecond line" {
		t.Errorf("Plain: got %q want %q", lyrics.Plain, "first line\nsecond line")
	}

	wantLRC := "[00:12.34]Hello\n[01:02.789]World"
	if lyrics.LRC != wantLRC {
		t.Errorf("LRC: got %q want %q", lyrics.LRC, wantLRC)
	}
	if strings.Contains(lyrics.LRC, "] ") {
		t.Errorf("LRC retains a trailing space after a timestamp: %q", lyrics.LRC)
	}
}

func TestPlaybackInfoReturnsManifestAndSendsStreamParams(t *testing.T) {
	t.Parallel()

	var gotPath, gotQuality, gotMode, gotAsset, gotCountry string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		query := r.URL.Query()
		gotQuality = query.Get("audioquality")
		gotMode = query.Get("playbackmode")
		gotAsset = query.Get("assetpresentation")
		gotCountry = query.Get("countryCode")
		_, _ = io.WriteString(w,
			`{"trackId":12345,"audioQuality":"LOSSLESS","manifestMimeType":"application/dash+xml","manifest":"YmFzZTY0"}`)
	}))
	defer srv.Close()

	client := tidal.New(
		staticTokens{countryCode: "US", userID: favUserID},
		tidal.WithBaseURL(srv.URL),
		tidal.WithRequestsPerMinute(testRPM),
	)

	info, err := client.PlaybackInfo(context.Background(), trackID, qualityLossless)
	if err != nil {
		t.Fatalf("PlaybackInfo: unexpected error: %v", err)
	}
	if info.ManifestMimeType != "application/dash+xml" {
		t.Errorf("ManifestMimeType: got %q want %q", info.ManifestMimeType, "application/dash+xml")
	}
	if info.Manifest != "YmFzZTY0" {
		t.Errorf("Manifest: got %q want %q", info.Manifest, "YmFzZTY0")
	}
	if gotPath != "/tracks/12345/playbackinfopostpaywall" {
		t.Errorf("path: got %q want %q", gotPath, "/tracks/12345/playbackinfopostpaywall")
	}
	if gotQuality != qualityLossless {
		t.Errorf("audioquality: got %q want %q", gotQuality, qualityLossless)
	}
	if gotMode != "STREAM" {
		t.Errorf("playbackmode: got %q want STREAM", gotMode)
	}
	if gotAsset != "FULL" {
		t.Errorf("assetpresentation: got %q want FULL", gotAsset)
	}
	if gotCountry != "US" {
		t.Errorf("countryCode: got %q want US", gotCountry)
	}
}

func TestPlaybackInfoUnavailableTrackYieldsTypedSkip(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"status":404,"subStatus":4005,"userMessage":"Asset is not available"}`)
	}))
	defer srv.Close()

	client := tidal.New(
		staticTokens{userID: favUserID},
		tidal.WithBaseURL(srv.URL),
		tidal.WithRequestsPerMinute(testRPM),
	)

	_, err := client.PlaybackInfo(context.Background(), unavailableID, qualityLossless)
	if !errors.Is(err, tidal.ErrTrackUnavailable) {
		t.Fatalf("PlaybackInfo error: got %v, want errors.Is ErrTrackUnavailable", err)
	}

	var apiErr *tidal.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("PlaybackInfo error: want *tidal.APIError recoverable in chain, got %v", err)
	}
	if apiErr.Status != http.StatusNotFound {
		t.Errorf("APIError.Status: got %d want %d", apiErr.Status, http.StatusNotFound)
	}
}

func TestTrackReturnsTypedMetadata(t *testing.T) {
	t.Parallel()

	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = io.WriteString(w,
			`{"id":12345,"title":"Song","trackNumber":3,"volumeNumber":1,"isrc":"USAT21234",`+
				`"copyright":"(C) Label","artists":[{"id":1,"name":"Artist A","type":"MAIN"}],`+
				`"album":{"id":99,"title":"Album","cover":"cover-uuid"}}`)
	}))
	defer srv.Close()

	client := tidal.New(
		staticTokens{userID: favUserID},
		tidal.WithBaseURL(srv.URL),
		tidal.WithRequestsPerMinute(testRPM),
	)

	track, err := client.Track(context.Background(), trackID)
	if err != nil {
		t.Fatalf("Track: unexpected error: %v", err)
	}
	if gotPath != "/tracks/12345" {
		t.Errorf("path: got %q want %q", gotPath, "/tracks/12345")
	}
	if track.ID != trackIDNum {
		t.Errorf("ID: got %d want %d", track.ID, trackIDNum)
	}
	if track.Title != "Song" {
		t.Errorf("Title: got %q want Song", track.Title)
	}
	if track.ISRC != "USAT21234" {
		t.Errorf("ISRC: got %q want USAT21234", track.ISRC)
	}
	if len(track.Artists) != 1 || track.Artists[0].Name != "Artist A" {
		t.Errorf("Artists: got %+v want one Artist A", track.Artists)
	}
	if track.Album.Cover != "cover-uuid" {
		t.Errorf("Album.Cover: got %q want cover-uuid", track.Album.Cover)
	}
}

func TestAlbumReturnsMetadataAndCredits(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("/albums/99", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w,
			`{"id":99,"title":"Album","numberOfTracks":10,"numberOfVolumes":1,`+
				`"releaseDate":"2020-01-01","upc":"00602","cover":"cov",`+
				`"artists":[{"id":1,"name":"Artist A"}]}`)
	})
	mux.HandleFunc("/albums/99/credits", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `[{"type":"Producer","contributors":[{"name":"Pro Ducer","id":7}]}]`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := tidal.New(
		staticTokens{userID: favUserID},
		tidal.WithBaseURL(srv.URL),
		tidal.WithRequestsPerMinute(testRPM),
	)

	album, err := client.Album(context.Background(), albumID)
	if err != nil {
		t.Fatalf("Album: unexpected error: %v", err)
	}
	if album.Title != "Album" || album.NumberOfTracks != albumTrackCount {
		t.Errorf("Album: got %+v want title Album with %d tracks", album, albumTrackCount)
	}

	credits, err := client.AlbumCredits(context.Background(), albumID)
	if err != nil {
		t.Fatalf("AlbumCredits: unexpected error: %v", err)
	}
	if len(credits) != 1 || credits[0].Type != "Producer" {
		t.Fatalf("AlbumCredits: got %+v want one Producer credit", credits)
	}
	if len(credits[0].Contributors) != 1 || credits[0].Contributors[0].Name != "Pro Ducer" {
		t.Errorf("Contributors: got %+v want one Pro Ducer", credits[0].Contributors)
	}
}

func TestCoverURLReplacesDashesWithSlashes(t *testing.T) {
	t.Parallel()

	got := tidal.CoverURL("24f9bdf3-cd00-4f80-a20f-c34b793c8f4e", coverSize)
	want := "https://resources.tidal.com/images/24f9bdf3/cd00/4f80/a20f/c34b793c8f4e/640x640.jpg"
	if got != want {
		t.Errorf("CoverURL: got %q want %q", got, want)
	}
}
