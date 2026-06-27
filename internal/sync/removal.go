package sync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rs/zerolog"

	"github.com/labi-le/tidal-syncer/internal/config"
	"github.com/labi-le/tidal-syncer/internal/ctxlog"
	"github.com/labi-le/tidal-syncer/internal/store"
)

const (
	// removalComponent labels every log line emitted by the Remover.
	removalComponent = "removal"
	// policyKeep leaves local files in place when a track is unfavorited.
	policyKeep = "keep"
	// policyMirror deletes the local file and sidecar of an unfavorited track.
	policyMirror = "mirror"
	// policyTrash relocates an unfavorited track's files under the trash folder.
	policyTrash = "trash"
	// trashDirName is the directory, relative to the music root, that policyTrash moves files into.
	trashDirName = ".trash"
	// lrcExt is the extension of the lyrics sidecar written next to each track.
	lrcExt = ".lrc"
	// opReconcile scopes the logger for a single reconciliation pass.
	opReconcile = "removal.Reconcile"
)

// RemoverParams bundles the Remover's injected dependencies and configuration.
type RemoverParams struct {
	Store  *store.Store
	Config config.Config
	Logger zerolog.Logger
}

// Remover reconciles the local library with the remote favorites after a sync,
// deleting or relocating the files of tracks that are no longer favorited per the
// configured removal policy. Every action is confined to the music root, and the
// first run - which has no stored snapshot - can never mark a track removed, so it
// never deletes anything.
type Remover struct {
	store  *store.Store
	music  string
	policy string
	logger zerolog.Logger
}

// NewRemover builds a Remover from p, scoping its logger to the removal component
// and cleaning the music root used as the sandbox boundary.
func NewRemover(p RemoverParams) *Remover {
	return &Remover{
		store:  p.Store,
		music:  filepath.Clean(p.Config.Paths.Music),
		policy: p.Config.Removal.Policy,
		logger: p.Logger.With().Str("component", removalComponent).Logger(),
	}
}

// Reconcile diffs the previous favorites snapshot against current and applies the
// configured policy to every track present before but absent from current. On the
// first run there is no stored snapshot, so DiffSnapshot reports nothing removed
// and no file is touched. Per-track resolution failures are logged and skipped;
// only a snapshot or store error aborts the pass.
func (r *Remover) Reconcile(ctx context.Context, current []store.SnapshotItem) error {
	logger := ctxlog.Op(r.logger, opReconcile)

	_, removed, err := r.store.DiffSnapshot(ctx, snapshotKindTracks, current)
	if err != nil {
		return fmt.Errorf("removal: diff snapshot: %w", err)
	}
	if len(removed) == 0 {
		return nil
	}

	if r.policy == policyKeep {
		for _, id := range removed {
			logger.Info().Str("track", id).Str("policy", policyKeep).Msg("kept unfavorited track")
		}

		return nil
	}

	for _, id := range removed {
		if err = r.applyPolicy(ctx, logger, id); err != nil {
			return err
		}
	}

	return nil
}

// applyPolicy resolves the on-disk path of unfavorited track id and applies the
// mirror or trash policy. A track missing from the store, lacking a path, or whose
// path escapes the music root is logged and skipped rather than touched.
func (r *Remover) applyPolicy(ctx context.Context, logger zerolog.Logger, id string) error {
	track, err := r.store.GetTrack(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			logger.Warn().Str("track", id).Msg("unfavorited track absent from store; skipping")

			return nil
		}

		return fmt.Errorf("removal: get track %q: %w", id, err)
	}
	if track.Path == "" {
		logger.Warn().Str("track", id).Msg("unfavorited track has no stored path; skipping")

		return nil
	}
	if !r.withinMusic(track.Path) {
		logger.Warn().Str("track", id).Str("path", track.Path).
			Msg("unfavorited track path escapes music root; skipping")

		return nil
	}

	switch r.policy {
	case policyMirror:
		return r.mirror(logger, id, track.Path)
	case policyTrash:
		return r.trash(logger, id, track.Path)
	default:
		logger.Warn().Str("track", id).Str("policy", r.policy).Msg("unknown removal policy; skipping")

		return nil
	}
}

// withinMusic reports whether path lies inside the music root. It returns false
// when path sits on another root, escapes via "..", or cannot be made relative -
// the sandbox that keeps every removal action confined to the music tree.
func (r *Remover) withinMusic(path string) bool {
	rel, err := filepath.Rel(r.music, filepath.Clean(path))
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}

	return !filepath.IsAbs(rel)
}

// mirror deletes the unfavorited track's audio file, its orphaned .lrc sidecar,
// and any parent directories left empty up to but excluding the music root.
func (r *Remover) mirror(logger zerolog.Logger, id, path string) error {
	deleted, err := removePath(path)
	if err != nil {
		return fmt.Errorf("removal: mirror track %q: %w", id, err)
	}
	if deleted {
		logger.Info().Str("track", id).Str("path", path).Str("policy", policyMirror).Msg("deleted unfavorited track")
	}

	if lrc := lrcSidecar(path); r.withinMusic(lrc) {
		deletedLRC, lrcErr := removePath(lrc)
		if lrcErr != nil {
			return fmt.Errorf("removal: mirror lyrics %q: %w", id, lrcErr)
		}
		if deletedLRC {
			logger.Info().Str("track", id).Str("path", lrc).Msg("deleted lyrics sidecar")
		}
	}

	r.pruneEmptyDirs(logger, filepath.Dir(path))

	return nil
}

// trash relocates the unfavorited track's audio file and its .lrc sidecar under
// <music>/.trash/, preserving their layout relative to the music root.
func (r *Remover) trash(logger zerolog.Logger, id, path string) error {
	moved, err := r.moveToTrash(path)
	if err != nil {
		return fmt.Errorf("removal: trash track %q: %w", id, err)
	}
	if moved {
		logger.Info().Str("track", id).Str("path", path).Str("policy", policyTrash).Msg("trashed unfavorited track")
	}

	if lrc := lrcSidecar(path); r.withinMusic(lrc) {
		movedLRC, lrcErr := r.moveToTrash(lrc)
		if lrcErr != nil {
			return fmt.Errorf("removal: trash lyrics %q: %w", id, lrcErr)
		}
		if movedLRC {
			logger.Info().Str("track", id).Str("path", lrc).Msg("trashed lyrics sidecar")
		}
	}

	return nil
}

// moveToTrash moves src to <music>/.trash/<rel>, where rel is src relative to the
// music root, creating the destination directory first. A missing source is a
// no-op. The destination lives under the music root, on the same volume as src, so
// os.Rename never crosses devices and no copy fallback is needed. It returns true
// only when a file was actually moved.
func (r *Remover) moveToTrash(src string) (bool, error) {
	if _, err := os.Lstat(src); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}

		return false, fmt.Errorf("stat %q: %w", src, err)
	}

	rel, err := filepath.Rel(r.music, filepath.Clean(src))
	if err != nil {
		return false, fmt.Errorf("relativize %q: %w", src, err)
	}
	dest := filepath.Join(r.music, trashDirName, rel)

	if err = os.MkdirAll(filepath.Dir(dest), dirMode); err != nil {
		return false, fmt.Errorf("create trash dir for %q: %w", src, err)
	}
	if err = os.Rename(src, dest); err != nil {
		return false, fmt.Errorf("move %q to trash: %w", src, err)
	}

	return true, nil
}

// pruneEmptyDirs removes empty directories walking up from dir toward the music
// root, stopping at the root itself. It is best-effort: a non-empty directory or
// any filesystem error simply halts the walk.
func (r *Remover) pruneEmptyDirs(logger zerolog.Logger, dir string) {
	for current := filepath.Clean(dir); current != r.music && r.withinMusic(current); current = filepath.Dir(current) {
		entries, err := os.ReadDir(current)
		if err != nil || len(entries) > 0 {
			return
		}
		if err = os.Remove(current); err != nil {
			return
		}
		logger.Info().Str("dir", current).Msg("removed empty directory")
	}
}

// removePath deletes path, treating an already-absent file as success. It reports
// whether a file was actually removed so the caller logs only real deletions.
func removePath(path string) (bool, error) {
	err := os.Remove(path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	default:
		return false, fmt.Errorf("remove %q: %w", path, err)
	}
}

// lrcSidecar returns the .lrc sidecar path for an audio file: the same path with
// its extension replaced by .lrc.
func lrcSidecar(audioPath string) string {
	return strings.TrimSuffix(audioPath, filepath.Ext(audioPath)) + lrcExt
}
