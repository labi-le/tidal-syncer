package sync_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/rs/zerolog"

	"github.com/labi-le/tidal-syncer/internal/config"
	"github.com/labi-le/tidal-syncer/internal/store"
	synceng "github.com/labi-le/tidal-syncer/internal/sync"
)

const (
	removalKindTracks             = "tracks"
	removedTrackID                = "removed-1"
	keptTrackID                   = "kept-1"
	removalFileMode   os.FileMode = 0o600
	removalDirMode    os.FileMode = 0o750
)

func TestRemovalKeep(t *testing.T) {
	// Given a previously favorited track present on disk and the keep policy
	ctx := context.Background()
	st := newTestStore(t)
	music := t.TempDir()
	audio := filepath.Join(music, "Artist", "Album", "track.flac")
	writeRemovalFile(t, audio)
	markRemovalTrack(t, st, audio)
	seedRemovalBaseline(t, st, removedTrackID, keptTrackID)

	// When reconciling with the track no longer favorited
	if _, err := newRemover(st, music, "keep").Reconcile(ctx, snapshotItems(keptTrackID)); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// Then the local file is left untouched
	requireExists(t, audio)
}

func TestRemovalMirror(t *testing.T) {
	// Given a previously favorited track with an .lrc sidecar in a dedicated dir
	ctx := context.Background()
	st := newTestStore(t)
	music := t.TempDir()
	audio := filepath.Join(music, "Artist", "Album", "track.flac")
	lrc := filepath.Join(music, "Artist", "Album", "track.lrc")
	writeRemovalFile(t, audio)
	writeRemovalFile(t, lrc)
	markRemovalTrack(t, st, audio)
	seedRemovalBaseline(t, st, removedTrackID, keptTrackID)

	// When reconciling under the mirror policy with the track unfavorited
	if _, err := newRemover(st, music, "mirror").Reconcile(ctx, snapshotItems(keptTrackID)); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// Then the file, its sidecar, and the now-empty parents are removed; music stays
	requireAbsent(t, audio)
	requireAbsent(t, lrc)
	requireAbsent(t, filepath.Join(music, "Artist", "Album"))
	requireAbsent(t, filepath.Join(music, "Artist"))
	requireExists(t, music)
}

func TestRemovalTrash(t *testing.T) {
	// Given a previously favorited track with an .lrc sidecar
	ctx := context.Background()
	st := newTestStore(t)
	music := t.TempDir()
	audio := filepath.Join(music, "Artist", "Album", "track.flac")
	lrc := filepath.Join(music, "Artist", "Album", "track.lrc")
	writeRemovalFile(t, audio)
	writeRemovalFile(t, lrc)
	markRemovalTrack(t, st, audio)
	seedRemovalBaseline(t, st, removedTrackID, keptTrackID)

	// When reconciling under the trash policy with the track unfavorited
	if _, err := newRemover(st, music, "trash").Reconcile(ctx, snapshotItems(keptTrackID)); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// Then both files are relocated under .trash preserving their layout
	requireAbsent(t, audio)
	requireAbsent(t, lrc)
	requireExists(t, filepath.Join(music, ".trash", "Artist", "Album", "track.flac"))
	requireExists(t, filepath.Join(music, ".trash", "Artist", "Album", "track.lrc"))
}

func TestRemovalFirstRunEmptySnapshot(t *testing.T) {
	// Given a track on disk but NO prior favorites snapshot (first run)
	ctx := context.Background()
	st := newTestStore(t)
	music := t.TempDir()
	audio := filepath.Join(music, "Artist", "Album", "track.flac")
	writeRemovalFile(t, audio)
	markRemovalTrack(t, st, audio)

	// When reconciling under the destructive mirror policy with no baseline
	if _, err := newRemover(st, music, "mirror").Reconcile(ctx, snapshotItems(removedTrackID)); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// Then nothing is deleted: an empty baseline can never mark a track removed
	requireExists(t, audio)
}

func TestRemovalSandbox(t *testing.T) {
	// Given a tracked file that lives OUTSIDE the music root
	ctx := context.Background()
	st := newTestStore(t)
	music := t.TempDir()
	outside := filepath.Join(t.TempDir(), "escape.flac")
	writeRemovalFile(t, outside)
	markRemovalTrack(t, st, outside)
	seedRemovalBaseline(t, st, removedTrackID, keptTrackID)

	// When reconciling under mirror with that track unfavorited
	if _, err := newRemover(st, music, "mirror").Reconcile(ctx, snapshotItems(keptTrackID)); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// Then the out-of-tree file is never touched
	requireExists(t, outside)
}

// TestRemovalContinuesPastPerTrackFailure verifies a per-track filesystem
// failure (an un-removable file) is logged and skipped rather than aborting the
// pass: Reconcile returns nil and every other unfavorited track is still
// processed.
func TestRemovalContinuesPastPerTrackFailure(t *testing.T) {
	// Given two unfavorited tracks, one whose parent dir is read-only so its
	// file cannot be deleted, and one in a writable dir
	ctx := context.Background()
	st := newTestStore(t)
	music := t.TempDir()
	badDir := filepath.Join(music, "Bad", "Album")
	badAudio := filepath.Join(badDir, "track.flac")
	goodAudio := filepath.Join(music, "Good", "Album", "track.flac")
	writeRemovalFile(t, badAudio)
	writeRemovalFile(t, goodAudio)
	markRemovalTrackID(t, st, "bad-1", badAudio)
	markRemovalTrackID(t, st, "good-1", goodAudio)
	seedRemovalBaseline(t, st, "bad-1", "good-1", keptTrackID)

	if err := os.Chmod(badDir, 0o500); err != nil {
		t.Fatalf("chmod bad dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(badDir, removalDirMode) })

	// When reconciling under mirror with both tracks unfavorited
	pending, err := newRemover(st, music, "mirror").Reconcile(ctx, snapshotItems(keptTrackID))
	if err != nil {
		t.Fatalf("reconcile must tolerate a per-track FS failure: %v", err)
	}

	// Then the failing track survives while the good one is still removed, and the
	// un-removable track is returned as pending so the caller retains it for retry.
	requireExists(t, badAudio)
	requireAbsent(t, goodAudio)
	if !slices.Contains(pending, "bad-1") {
		t.Fatalf("pending = %v, want it to contain un-removable bad-1", pending)
	}
	if slices.Contains(pending, "good-1") {
		t.Fatalf("pending = %v, must not contain removed good-1", pending)
	}
}

// TestRemovalStoreErrorAborts verifies a broken store aborts the pass: with the
// store closed, the snapshot diff fails and Reconcile returns a non-nil error
// rather than swallowing it like a per-track failure.
func TestRemovalStoreErrorAborts(t *testing.T) {
	// Given a seeded baseline and a track on disk, then a closed (broken) store
	ctx := context.Background()
	st := newTestStore(t)
	music := t.TempDir()
	audio := filepath.Join(music, "Artist", "Album", "track.flac")
	writeRemovalFile(t, audio)
	markRemovalTrack(t, st, audio)
	seedRemovalBaseline(t, st, removedTrackID, keptTrackID)
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	// When reconciling against the broken store
	_, err := newRemover(st, music, "mirror").Reconcile(ctx, snapshotItems(keptTrackID))

	// Then the pass aborts with an error and leaves the file untouched
	if err == nil {
		t.Fatal("reconcile must abort on a store error, got nil")
	}
	requireExists(t, audio)
}

// newRemover builds a Remover over st rooted at music with the given policy.
func newRemover(st *store.Store, music, policy string) *synceng.Remover {
	cfg := config.Defaults()
	cfg.Paths.Music = music
	cfg.Removal = config.Removal{Policy: policy}

	return synceng.NewRemover(synceng.RemoverParams{
		Store:  st,
		Config: cfg,
		Logger: zerolog.Nop(),
	})
}

// snapshotItems builds a snapshot slice from ids for use as a current favorites set.
func snapshotItems(ids ...string) []store.SnapshotItem {
	items := make([]store.SnapshotItem, 0, len(ids))
	for _, id := range ids {
		items = append(items, store.SnapshotItem{TidalID: id, Name: id})
	}

	return items
}

// seedRemovalBaseline stores a favorites snapshot containing the given track ids.
func seedRemovalBaseline(t *testing.T, st *store.Store, ids ...string) {
	t.Helper()

	if err := st.ReplaceSnapshot(context.Background(), removalKindTracks, snapshotItems(ids...)); err != nil {
		t.Fatalf("seed baseline: %v", err)
	}
}

// markRemovalTrack records removedTrackID as a done track at path in the store.
func markRemovalTrack(t *testing.T, st *store.Store, path string) {
	t.Helper()

	rec := store.Track{
		TidalID:         removedTrackID,
		ISRC:            "",
		AlbumID:         "",
		Path:            path,
		ObtainedQuality: gotQuality,
		Status:          store.StatusDone,
		UpdatedAt:       0,
	}
	if err := st.MarkTrack(context.Background(), rec); err != nil {
		t.Fatalf("mark track %q: %v", removedTrackID, err)
	}
}

// markRemovalTrackID records id as a done track at path in the store.
func markRemovalTrackID(t *testing.T, st *store.Store, id, path string) {
	t.Helper()

	rec := store.Track{
		TidalID:         id,
		Path:            path,
		ObtainedQuality: gotQuality,
		Status:          store.StatusDone,
	}
	if err := st.MarkTrack(context.Background(), rec); err != nil {
		t.Fatalf("mark track %q: %v", id, err)
	}
}

// writeRemovalFile creates path, including parent directories, with placeholder content.
func writeRemovalFile(t *testing.T, path string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), removalDirMode); err != nil {
		t.Fatalf("mkdir for %q: %v", path, err)
	}
	if err := os.WriteFile(path, []byte("audio"), removalFileMode); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

// requireExists fails the test unless path exists.
func requireExists(t *testing.T, path string) {
	t.Helper()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected %q to exist: %v", path, err)
	}
}

// requireAbsent fails the test unless path is absent.
func requireAbsent(t *testing.T, path string) {
	t.Helper()

	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected %q to be absent, stat err: %v", path, err)
	}
}
