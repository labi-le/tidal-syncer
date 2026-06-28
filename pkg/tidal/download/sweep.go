package download

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// SweepStale walks dir recursively and removes orphaned "*.part" files left by a
// previous run that was interrupted before it could atomically rename a completed
// download into place. Such orphans can appear at any depth, e.g. under
// <music>/<albumartist>/<album>/, so the whole tree is scanned. It returns the
// number of stale part files removed and is intended to run once at startup. A
// missing dir is not an error; per-file removal failures are collected and
// returned joined.
func SweepStale(dir string) (int, error) {
	root, openErr := os.OpenRoot(dir)
	if openErr != nil {
		if errors.Is(openErr, fs.ErrNotExist) {
			return 0, nil
		}

		return 0, fmt.Errorf("download: open root %q: %w", dir, openErr)
	}
	defer func() { _ = root.Close() }()

	var (
		removed int
		errs    []error
	)

	walkErr := fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), PartSuffix) {
			return nil
		}

		if rmErr := root.Remove(filepath.FromSlash(path)); rmErr != nil {
			if !errors.Is(rmErr, fs.ErrNotExist) {
				errs = append(errs, fmt.Errorf("download: remove stale part file %q: %w", path, rmErr))
			}

			return nil
		}
		removed++

		return nil
	})
	if walkErr != nil {
		errs = append(errs, fmt.Errorf("download: walk dir %q: %w", dir, walkErr))
	}

	return removed, errors.Join(errs...)
}
