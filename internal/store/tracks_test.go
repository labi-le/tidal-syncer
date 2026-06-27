package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/labi-le/tidal-syncer/internal/store"
)

func Test_Store_MarkTrack_then_GetTrack_returns_it(t *testing.T) {
	// Given
	ctx := context.Background()
	st := newStore(t)
	tr := store.Track{TidalID: "t-1", ObtainedQuality: "LOSSLESS", Status: store.StatusDone}

	// When
	if err := st.MarkTrack(ctx, tr); err != nil {
		t.Fatalf("mark: %v", err)
	}
	got, err := st.GetTrack(ctx, "t-1")

	// Then
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.TidalID != "t-1" || got.Status != store.StatusDone || got.ObtainedQuality != "LOSSLESS" {
		t.Errorf("unexpected track: %+v", got)
	}
}

func Test_Store_MarkTrack_updates_status_on_conflict(t *testing.T) {
	// Given a failed track
	ctx := context.Background()
	st := newStore(t)
	if err := st.MarkTrack(ctx, store.Track{TidalID: "t-1", Status: store.StatusFailed}); err != nil {
		t.Fatalf("mark failed: %v", err)
	}

	// When the same track is re-marked done
	if err := st.MarkTrack(ctx, store.Track{TidalID: "t-1", Status: store.StatusDone}); err != nil {
		t.Fatalf("mark done: %v", err)
	}

	// Then GetTrack reports the latest status with no duplicate row
	got, err := st.GetTrack(ctx, "t-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != store.StatusDone {
		t.Errorf("status = %q, want %q", got.Status, store.StatusDone)
	}
}

func Test_Store_GetTrack_round_trips(t *testing.T) {
	// Given
	ctx := context.Background()
	st := newStore(t)
	tr := store.Track{
		TidalID: "t-9", ISRC: "US1234567890", AlbumID: "alb-1",
		Path: "/music/x.flac", ObtainedQuality: "HI_RES", Status: store.StatusDone,
	}

	// When
	if err := st.MarkTrack(ctx, tr); err != nil {
		t.Fatalf("mark: %v", err)
	}
	got, err := st.GetTrack(ctx, "t-9")

	// Then (updated_at is assigned by the store, so it is ignored here)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	got.UpdatedAt = 0
	if got != tr {
		t.Errorf("got %+v want %+v", got, tr)
	}
}

func Test_Store_GetTrack_returns_ErrNotFound_when_absent(t *testing.T) {
	// Given an empty store
	st := newStore(t)

	// When
	_, err := st.GetTrack(context.Background(), "missing")

	// Then
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func Test_Store_MarkTrack_assigns_updated_at(t *testing.T) {
	// Given
	ctx := context.Background()
	st := newStore(t)

	// When
	if err := st.MarkTrack(ctx, store.Track{TidalID: "t-1", Status: store.StatusDone}); err != nil {
		t.Fatalf("mark: %v", err)
	}

	// Then updated_at is populated by the store clock
	got, err := st.GetTrack(ctx, "t-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.UpdatedAt <= 0 {
		t.Errorf("expected updated_at to be set, got %d", got.UpdatedAt)
	}
}
