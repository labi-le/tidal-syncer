package download_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/labi-le/tidal-syncer/pkg/tidal/download"
)

const (
	filePerm = 0o600
	dirPerm  = 0o700
)

// writeFile plants a one-byte file at path so a test can assert whether sweeping
// keeps or removes it.
func writeFile(t *testing.T, path string) {
	t.Helper()

	if err := os.WriteFile(path, []byte("x"), filePerm); err != nil {
		t.Fatalf("write %q: %v", path, err)
	}
}

func mkdirAll(t *testing.T, dir string) {
	t.Helper()

	if err := os.MkdirAll(dir, dirPerm); err != nil {
		t.Fatalf("mkdir %q: %v", dir, err)
	}
}

func TestBTSSweepStaleRemovesOrphanPartFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	orphans := []string{"a.flac.part", "b.flac.part"}
	keep := "c.flac"
	for _, name := range orphans {
		writeFile(t, filepath.Join(dir, name))
	}
	writeFile(t, filepath.Join(dir, keep))

	removed, sweepErr := download.SweepStale(dir)
	if sweepErr != nil {
		t.Fatalf("SweepStale: unexpected error: %v", sweepErr)
	}
	if removed != len(orphans) {
		t.Fatalf("SweepStale removed = %d, want %d", removed, len(orphans))
	}

	for _, name := range orphans {
		if _, err := os.Stat(filepath.Join(dir, name)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("orphan %q must be removed, stat err = %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, keep)); err != nil {
		t.Fatalf("finished file %q must remain: %v", keep, err)
	}
}

func TestBTSSweepStaleRemovesNestedOrphanPartFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	albumDir := filepath.Join(root, "Artist", "Album")
	mkdirAll(t, albumDir)

	nestedOrphan := filepath.Join(albumDir, "x.flac.part")
	topOrphan := filepath.Join(root, "y.part")
	keeper := filepath.Join(albumDir, "z.flac")
	writeFile(t, nestedOrphan)
	writeFile(t, topOrphan)
	writeFile(t, keeper)

	const wantRemoved = 2

	removed, sweepErr := download.SweepStale(root)
	if sweepErr != nil {
		t.Fatalf("SweepStale: unexpected error: %v", sweepErr)
	}
	if removed != wantRemoved {
		t.Fatalf("SweepStale removed = %d, want %d", removed, wantRemoved)
	}

	for _, orphan := range []string{nestedOrphan, topOrphan} {
		if _, err := os.Stat(orphan); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("orphan %q must be removed, stat err = %v", orphan, err)
		}
	}
	if _, err := os.Stat(keeper); err != nil {
		t.Fatalf("keeper %q must remain: %v", keeper, err)
	}
}

func TestBTSSweepStaleMissingDirIsNotAnError(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "does-not-exist")
	removed, err := download.SweepStale(missing)
	if err != nil {
		t.Fatalf("SweepStale on missing dir: want nil, got %v", err)
	}
	if removed != 0 {
		t.Errorf("SweepStale on missing dir removed = %d, want 0", removed)
	}
}
