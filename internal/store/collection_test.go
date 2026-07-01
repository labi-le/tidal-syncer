package store_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/labi-le/tidal-syncer/internal/store"
)

func Test_Store_PutCollection_and_GetCollection_round_trip(t *testing.T) {
	// Given a fresh store with an album cached to three track ids
	ctx := context.Background()
	st := newStore(t)
	const kind, id = "album", "201"
	if err := st.PutCollection(ctx, kind, id, "", []int{101, 102, 103}); err != nil {
		t.Fatalf("put collection: %v", err)
	}

	// When the collection is read back
	got, ok, err := st.GetCollection(ctx, kind, id)

	// Then it returns the stored version, count, and ids in ascending order
	if err != nil {
		t.Fatalf("get collection: %v", err)
	}
	if !ok {
		t.Fatal("get collection: ok = false, want true")
	}
	want := store.CollectionCache{Version: "", NTracks: 3, TrackIDs: []int{101, 102, 103}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("get collection:\n got %+v\nwant %+v", got, want)
	}
}

func Test_Store_GetCollection_reports_missing(t *testing.T) {
	// Given a fresh store with nothing cached
	ctx := context.Background()
	st := newStore(t)

	// When an absent collection is read
	_, ok, err := st.GetCollection(ctx, "album", "999")

	// Then it reports absence without error
	if err != nil {
		t.Fatalf("get collection: %v", err)
	}
	if ok {
		t.Error("get collection: ok = true, want false")
	}
}

func Test_Store_PutCollection_replaces_existing(t *testing.T) {
	// Given a playlist cached with three tracks at version v1
	ctx := context.Background()
	st := newStore(t)
	const kind, id = "playlist", "pl-1"
	if err := st.PutCollection(ctx, kind, id, "v1", []int{101, 102, 103}); err != nil {
		t.Fatalf("put v1: %v", err)
	}

	// When it is re-cached with a new version and a different track set
	if err := st.PutCollection(ctx, kind, id, "v2", []int{104, 105}); err != nil {
		t.Fatalf("put v2: %v", err)
	}

	// Then only the new version and track set remain
	got, ok, err := st.GetCollection(ctx, kind, id)
	if err != nil {
		t.Fatalf("get collection: %v", err)
	}
	if !ok {
		t.Fatal("get collection: ok = false, want true")
	}
	want := store.CollectionCache{Version: "v2", NTracks: 2, TrackIDs: []int{104, 105}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("get collection:\n got %+v\nwant %+v", got, want)
	}
}

func Test_Store_PruneCollections_removes_unfavorited(t *testing.T) {
	// Given three cached albums
	ctx := context.Background()
	st := newStore(t)
	const kind = "album"
	for _, id := range []string{"1", "2", "3"} {
		if err := st.PutCollection(ctx, kind, id, "", []int{1}); err != nil {
			t.Fatalf("put %s: %v", id, err)
		}
	}

	// When pruning keeps only two of them
	if err := st.PruneCollections(ctx, kind, []string{"1", "3"}); err != nil {
		t.Fatalf("prune: %v", err)
	}

	// Then the unkept album is gone and the kept albums remain
	if _, ok, err := st.GetCollection(ctx, kind, "2"); err != nil || ok {
		t.Errorf("collection 2: ok=%v err=%v, want ok=false nil", ok, err)
	}
	for _, id := range []string{"1", "3"} {
		if _, ok, err := st.GetCollection(ctx, kind, id); err != nil || !ok {
			t.Errorf("collection %s: ok=%v err=%v, want ok=true nil", id, ok, err)
		}
	}
}
