package sync_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	if err := newRemover(st, music, "keep").Reconcile(ctx, snapshotItems(keptTrackID)); err != nil {
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
	if err := newRemover(st, music, "mirror").Reconcile(ctx, snapshotItems(keptTrackID)); err != nil {
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
	if err := newRemover(st, music, "trash").Reconcile(ctx, snapshotItems(keptTrackID)); err != nil {
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
	if err := newRemover(st, music, "mirror").Reconcile(ctx, snapshotItems(removedTrackID)); err != nil {
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
	if err := newRemover(st, music, "mirror").Reconcile(ctx, snapshotItems(keptTrackID)); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// Then the out-of-tree file is never touched
	requireExists(t, outside)
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
