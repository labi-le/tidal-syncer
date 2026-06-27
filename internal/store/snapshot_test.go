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
