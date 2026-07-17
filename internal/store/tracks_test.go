package store_test

import (
	"context"
	"testing"

	"github.com/labi-le/tidal-syncer/internal/store"
)

func Test_Store_genre_round_trips_through_MarkTrack_and_GetTrack(t *testing.T) {
	// Given a done track carrying two semicolon-joined genres.
	ctx := context.Background()
	st := newStore(t)
	rec := store.Track{
		TidalID:          "100",
		Path:             "/music/a.flac",
		ObtainedQuality:  "LOSSLESS",
		RequestedQuality: "LOSSLESS",
		Genre:            "Rock;Metal",
		Status:           store.StatusDone,
	}

	// When the track is marked and read back.
	if err := st.MarkTrack(ctx, rec); err != nil {
		t.Fatalf("mark track: %v", err)
	}
	got, err := st.GetTrack(ctx, "100")
	if err != nil {
		t.Fatalf("get track: %v", err)
	}

	// Then the genre column round-trips unchanged.
	if got.Genre != "Rock;Metal" {
		t.Fatalf("genre = %q, want %q", got.Genre, "Rock;Metal")
	}
}

func Test_Store_TracksMissingGenre_selects_only_done_genreless_with_path(t *testing.T) {
	// Given one track that needs a genre backfill and three that must be excluded.
	ctx := context.Background()
	st := newStore(t)
	seed := []store.Track{
		{TidalID: "1", Path: "/m/1.flac", Genre: "", Status: store.StatusDone},
		{TidalID: "2", Path: "/m/2.flac", Genre: "Rock", Status: store.StatusDone},
		{TidalID: "3", Path: "", Genre: "", Status: store.StatusDone},
		{TidalID: "4", Path: "/m/4.flac", Genre: "", Status: store.StatusFailed},
	}
	for _, tr := range seed {
		if err := st.MarkTrack(ctx, tr); err != nil {
			t.Fatalf("mark track %s: %v", tr.TidalID, err)
		}
	}

	// When the backfill gap set is queried.
	gaps, err := st.TracksMissingGenre(ctx)
	if err != nil {
		t.Fatalf("tracks missing genre: %v", err)
	}

	// Then only the done, path-bearing, genre-less track is returned.
	if len(gaps) != 1 {
		t.Fatalf("gaps = %d, want 1 (%+v)", len(gaps), gaps)
	}
	if gaps[0].TidalID != "1" || gaps[0].Path != "/m/1.flac" {
		t.Fatalf("gap = %+v, want {TidalID:1 Path:/m/1.flac}", gaps[0])
	}
}

func Test_Store_SetTrackGenre_updates_only_the_genre(t *testing.T) {
	// Given a done track stored without a genre.
	ctx := context.Background()
	st := newStore(t)
	if err := st.MarkTrack(ctx, store.Track{TidalID: "7", Path: "/m/7.flac", Status: store.StatusDone}); err != nil {
		t.Fatalf("mark track: %v", err)
	}

	// When its genre is set.
	if err := st.SetTrackGenre(ctx, "7", "Jazz;Fusion"); err != nil {
		t.Fatalf("set track genre: %v", err)
	}

	// Then GetTrack reflects the new genre.
	got, err := st.GetTrack(ctx, "7")
	if err != nil {
		t.Fatalf("get track: %v", err)
	}
	if got.Genre != "Jazz;Fusion" {
		t.Fatalf("genre = %q, want %q", got.Genre, "Jazz;Fusion")
	}
}

func Test_Store_MarkTrack_round_trips_permanent_flag(t *testing.T) {
	// Given a track marked as a permanent failure.
	ctx := context.Background()
	st := newStore(t)
	want := store.Track{
		TidalID:          "42",
		ISRC:             "US1234567890",
		AlbumID:          "7",
		RequestedQuality: "HI_RES_LOSSLESS",
		Status:           store.StatusFailed,
		Permanent:        true,
	}
	if err := st.MarkTrack(ctx, want); err != nil {
		t.Fatalf("mark track: %v", err)
	}

	// When it is read back.
	got, err := st.GetTrack(ctx, want.TidalID)
	if err != nil {
		t.Fatalf("get track: %v", err)
	}

	// Then the permanent flag and requested tier survive the round trip.
	if !got.Permanent {
		t.Errorf("permanent = false, want true (got %+v)", got)
	}
	if got.Status != store.StatusFailed {
		t.Errorf("status = %q, want %q", got.Status, store.StatusFailed)
	}
	if got.RequestedQuality != want.RequestedQuality {
		t.Errorf("requested_quality = %q, want %q", got.RequestedQuality, want.RequestedQuality)
	}
}

func Test_Store_MarkTrack_defaults_permanent_to_false(t *testing.T) {
	// Given a done track marked without setting Permanent.
	ctx := context.Background()
	st := newStore(t)
	if err := st.MarkTrack(ctx, store.Track{
		TidalID:          "43",
		Path:             "/music/Artist/Album/01 - Song.flac",
		ObtainedQuality:  "LOSSLESS",
		RequestedQuality: "LOSSLESS",
		Status:           store.StatusDone,
	}); err != nil {
		t.Fatalf("mark track: %v", err)
	}

	// When it is read back.
	got, err := st.GetTrack(ctx, "43")
	if err != nil {
		t.Fatalf("get track: %v", err)
	}

	// Then permanent defaults to false.
	if got.Permanent {
		t.Errorf("permanent = true, want false (got %+v)", got)
	}
}

func Test_Store_MarkFailed_preserves_path_and_quality_of_a_done_track(t *testing.T) {
	// Given a track already stored as done with a file path, obtained quality and genre.
	ctx := context.Background()
	st := newStore(t)
	done := store.Track{
		TidalID:          "7",
		ISRC:             "US-XXX-00-00007",
		AlbumID:          "70",
		Path:             "/music/artist/album/07 - song.flac",
		ObtainedQuality:  "LOSSLESS",
		RequestedQuality: "LOSSLESS",
		Genre:            "Rock;Metal",
		Status:           store.StatusDone,
	}
	if err := st.MarkTrack(ctx, done); err != nil {
		t.Fatalf("seed done track: %v", err)
	}

	// When a later re-attempt fails and is recorded via MarkFailed.
	if err := st.MarkFailed(ctx, store.Track{
		TidalID:          "7",
		ISRC:             "US-XXX-00-00007",
		AlbumID:          "70",
		RequestedQuality: "HI_RES_LOSSLESS",
		Status:           store.StatusFailed,
		Permanent:        true,
	}); err != nil {
		t.Fatalf("mark failed: %v", err)
	}

	// Then status flips to failed but path/obtained-quality/genre survive, so the
	// removal reconciler can still delete the file and the playlist keeps listing it.
	got, err := st.GetTrack(ctx, "7")
	if err != nil {
		t.Fatalf("get track: %v", err)
	}
	if got.Status != store.StatusFailed {
		t.Fatalf("status = %q, want failed", got.Status)
	}
	if !got.Permanent {
		t.Fatal("permanent = false, want true")
	}
	if got.Path != done.Path {
		t.Fatalf("path = %q, want preserved %q", got.Path, done.Path)
	}
	if got.ObtainedQuality != done.ObtainedQuality {
		t.Fatalf("obtained quality = %q, want preserved %q", got.ObtainedQuality, done.ObtainedQuality)
	}
	if got.Genre != done.Genre {
		t.Fatalf("genre = %q, want preserved %q", got.Genre, done.Genre)
	}
	if got.RequestedQuality != "HI_RES_LOSSLESS" {
		t.Fatalf("requested quality = %q, want updated %q", got.RequestedQuality, "HI_RES_LOSSLESS")
	}
}

func Test_Store_MarkFailed_records_a_new_track_with_no_file(t *testing.T) {
	// Given a track never seen before that fails on its first attempt.
	ctx := context.Background()
	st := newStore(t)
	if err := st.MarkFailed(ctx, store.Track{
		TidalID:          "8",
		ISRC:             "US-XXX-00-00008",
		AlbumID:          "80",
		RequestedQuality: "HI_RES_LOSSLESS",
		Status:           store.StatusFailed,
	}); err != nil {
		t.Fatalf("mark failed: %v", err)
	}

	// Then it is stored as failed with an empty path, matching a track with no file.
	got, err := st.GetTrack(ctx, "8")
	if err != nil {
		t.Fatalf("get track: %v", err)
	}
	if got.Status != store.StatusFailed {
		t.Fatalf("status = %q, want failed", got.Status)
	}
	if got.Path != "" {
		t.Fatalf("path = %q, want empty for a never-downloaded track", got.Path)
	}
}
