package store_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/labi-le/tidal-syncer/internal/store"
)

func Test_Store_DiffSnapshot_returns_added_and_removed(t *testing.T) {
	// Given a baseline snapshot {a, b, c}
	ctx := context.Background()
	st := newStore(t)
	const kind = "albums"
	base := []store.SnapshotItem{{TidalID: "a", Name: "A"}, {TidalID: "b", Name: "B"}, {TidalID: "c", Name: "C"}}
	if err := st.ReplaceSnapshot(ctx, kind, base); err != nil {
		t.Fatalf("replace: %v", err)
	}

	// When diffing incoming {b, c, d} against the baseline
	incoming := []store.SnapshotItem{{TidalID: "b", Name: "B"}, {TidalID: "c", Name: "C"}, {TidalID: "d", Name: "D"}}
	added, removed, err := st.DiffSnapshot(ctx, kind, incoming)

	// Then added=[d] and removed=[a]
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if !reflect.DeepEqual(added, []string{"d"}) {
		t.Errorf("added: got %v want [d]", added)
	}
	if !reflect.DeepEqual(removed, []string{"a"}) {
		t.Errorf("removed: got %v want [a]", removed)
	}
}

func Test_Store_DiffSnapshot_all_added_when_no_baseline(t *testing.T) {
	// Given an empty snapshot for the kind
	ctx := context.Background()
	st := newStore(t)

	// When diffing incoming items
	incoming := []store.SnapshotItem{{TidalID: "x"}, {TidalID: "y"}}
	added, removed, err := st.DiffSnapshot(ctx, "tracks", incoming)

	// Then everything is added and nothing removed
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if !reflect.DeepEqual(added, []string{"x", "y"}) {
		t.Errorf("added: got %v want [x y]", added)
	}
	if len(removed) != 0 {
		t.Errorf("removed: got %v want []", removed)
	}
}

func Test_Store_ReplaceSnapshot_overwrites_previous(t *testing.T) {
	// Given a snapshot that is later fully replaced
	ctx := context.Background()
	st := newStore(t)
	const kind = "artists"
	if err := st.ReplaceSnapshot(ctx, kind, []store.SnapshotItem{{TidalID: "old"}}); err != nil {
		t.Fatalf("first replace: %v", err)
	}
	if err := st.ReplaceSnapshot(ctx, kind, []store.SnapshotItem{{TidalID: "new"}}); err != nil {
		t.Fatalf("second replace: %v", err)
	}

	// When diffing against an empty incoming set
	_, removed, err := st.DiffSnapshot(ctx, kind, nil)

	// Then only the surviving "new" row is reported as removed (old was overwritten)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if !reflect.DeepEqual(removed, []string{"new"}) {
		t.Errorf("removed: got %v want [new]", removed)
	}
}

func Test_Store_FavoritesByRecency_returns_dated_items_newest_first(t *testing.T) {
	// Given a tracks snapshot mixing dated favorites with one undated album-expanded track
	ctx := context.Background()
	st := newStore(t)
	const kind = "tracks"
	items := []store.SnapshotItem{
		{TidalID: "1", Name: "Oldest", AddedAt: "2026-06-01T00:00:00.000+0000"},
		{TidalID: "2", Name: "Newest", AddedAt: "2026-06-30T00:00:00.000+0000"},
		{TidalID: "3", Name: "Middle", AddedAt: "2026-06-15T00:00:00.000+0000"},
		{TidalID: "4", Name: "Undated"},
	}
	if err := st.ReplaceSnapshot(ctx, kind, items); err != nil {
		t.Fatalf("replace: %v", err)
	}

	// When reading the two most recently added favorites
	got, err := st.FavoritesByRecency(ctx, kind, 2)

	// Then they return newest-first, the undated track is excluded, and the limit holds
	if err != nil {
		t.Fatalf("favorites by recency: %v", err)
	}
	want := []store.SnapshotItem{
		{TidalID: "2", Name: "Newest", AddedAt: "2026-06-30T00:00:00.000+0000"},
		{TidalID: "3", Name: "Middle", AddedAt: "2026-06-15T00:00:00.000+0000"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("favorites by recency:\n got %+v\nwant %+v", got, want)
	}
}

func Test_Store_OrderedFavoriteFiles_returns_downloaded_favorites_newest_first(t *testing.T) {
	// Given a tracks snapshot mixing dated favorites (some downloaded), one undated
	// album-expanded track (id 4), and one dated favorite whose download never
	// completed so it has no track row / file (id 5).
	ctx := context.Background()
	st := newStore(t)
	const kind = "tracks"
	items := []store.SnapshotItem{
		{TidalID: "1", Name: "Oldest", AddedAt: "2026-06-01T00:00:00.000+0000"},
		{TidalID: "2", Name: "Newest", AddedAt: "2026-06-30T00:00:00.000+0000"},
		{TidalID: "3", Name: "Middle", AddedAt: "2026-06-15T00:00:00.000+0000"},
		{TidalID: "4", Name: "Undated"},
		{TidalID: "5", Name: "NoFile", AddedAt: "2026-06-20T00:00:00.000+0000"},
	}
	if err := st.ReplaceSnapshot(ctx, kind, items); err != nil {
		t.Fatalf("replace: %v", err)
	}
	for _, tr := range []store.Track{
		{TidalID: "1", Path: "/music/Artist/Album/01 - Oldest.flac", ObtainedQuality: "LOSSLESS", RequestedQuality: "LOSSLESS", Status: store.StatusDone},
		{TidalID: "2", Path: "/music/Artist/Album/02 - Newest.flac", ObtainedQuality: "LOSSLESS", RequestedQuality: "LOSSLESS", Status: store.StatusDone},
		{TidalID: "3", Path: "/music/Artist/Album/03 - Middle.flac", ObtainedQuality: "LOSSLESS", RequestedQuality: "LOSSLESS", Status: store.StatusDone},
		{TidalID: "4", Path: "/music/Artist/Album/04 - Undated.flac", ObtainedQuality: "LOSSLESS", RequestedQuality: "LOSSLESS", Status: store.StatusDone},
	} {
		if err := st.MarkTrack(ctx, tr); err != nil {
			t.Fatalf("mark track %s: %v", tr.TidalID, err)
		}
	}

	// When reading the favorite files in favorite-add order
	got, err := st.OrderedFavoriteFiles(ctx, kind)

	// Then downloaded dated favorites return newest-first; the undated track (id 4,
	// excluded by the date filter) and the dated-but-undownloaded favorite (id 5,
	// excluded by the join because it has no file) are both omitted.
	if err != nil {
		t.Fatalf("ordered favorite files: %v", err)
	}
	want := []store.FavoriteFile{
		{Title: "Newest", Path: "/music/Artist/Album/02 - Newest.flac", AddedAt: "2026-06-30T00:00:00.000+0000"},
		{Title: "Middle", Path: "/music/Artist/Album/03 - Middle.flac", AddedAt: "2026-06-15T00:00:00.000+0000"},
		{Title: "Oldest", Path: "/music/Artist/Album/01 - Oldest.flac", AddedAt: "2026-06-01T00:00:00.000+0000"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ordered favorite files:\n got %+v\nwant %+v", got, want)
	}
}

func Test_Store_Snapshot_isolated_by_kind(t *testing.T) {
	// Given two kinds with overlapping ids
	ctx := context.Background()
	st := newStore(t)
	if err := st.ReplaceSnapshot(ctx, "albums", []store.SnapshotItem{{TidalID: "shared"}}); err != nil {
		t.Fatalf("replace albums: %v", err)
	}
	if err := st.ReplaceSnapshot(ctx, "artists", []store.SnapshotItem{{TidalID: "shared"}}); err != nil {
		t.Fatalf("replace artists: %v", err)
	}

	// When diffing one kind against an empty set
	_, removed, err := st.DiffSnapshot(ctx, "albums", nil)

	// Then only that kind's row is affected
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if !reflect.DeepEqual(removed, []string{"shared"}) {
		t.Errorf("removed: got %v want [shared]", removed)
	}
}

func Test_Store_OrderedFavoriteFiles_keeps_failed_track_with_preserved_path(t *testing.T) {
	// Given a dated favorite that downloaded once (file on disk) and then had a
	// failed re-download attempt, so its status is failed but its path is preserved.
	ctx := context.Background()
	st := newStore(t)
	const kind = "tracks"
	if err := st.ReplaceSnapshot(ctx, kind, []store.SnapshotItem{
		{TidalID: "9", Name: "Kept", AddedAt: "2026-06-10T00:00:00.000+0000"},
	}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if err := st.MarkTrack(ctx, store.Track{
		TidalID: "9", Path: "/music/Artist/Album/09 - Kept.flac",
		ObtainedQuality: "LOSSLESS", RequestedQuality: "LOSSLESS", Status: store.StatusDone,
	}); err != nil {
		t.Fatalf("mark done: %v", err)
	}
	if err := st.MarkFailed(ctx, store.Track{
		TidalID: "9", RequestedQuality: "HI_RES_LOSSLESS", Status: store.StatusFailed,
	}); err != nil {
		t.Fatalf("mark failed: %v", err)
	}

	// When reading the favorite files
	got, err := st.OrderedFavoriteFiles(ctx, kind)
	if err != nil {
		t.Fatalf("ordered favorite files: %v", err)
	}

	// Then the still-on-disk favorite is listed despite the failed re-download,
	// keyed off the preserved path rather than the download status.
	want := []store.FavoriteFile{
		{Title: "Kept", Path: "/music/Artist/Album/09 - Kept.flac", AddedAt: "2026-06-10T00:00:00.000+0000"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ordered favorite files:\n got %+v\nwant %+v", got, want)
	}
}
