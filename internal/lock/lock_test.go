package lock_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/labi-le/tidal-syncer/internal/lock"
)

func TestTryAcquire_succeedsOnFreshFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "test.lock")
	fl := &lock.FileLock{}

	release, err := fl.TryAcquire(path)
	if err != nil {
		t.Fatalf("TryAcquire on fresh file: unexpected error: %v", err)
	}
	if release == nil {
		t.Fatal("TryAcquire returned nil release func")
	}

	if relErr := release(); relErr != nil {
		t.Fatalf("release: unexpected error: %v", relErr)
	}
}

func TestTryAcquire_returnsErrLockedWhenAlreadyHeld(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "test.lock")

	first := &lock.FileLock{}
	releaseFirst, err := first.TryAcquire(path)
	if err != nil {
		t.Fatalf("first TryAcquire: unexpected error: %v", err)
	}
	t.Cleanup(func() {
		if relErr := releaseFirst(); relErr != nil {
			t.Errorf("first release: unexpected error: %v", relErr)
		}
	})

	// Second FileLock = second open file description on the same path.
	// flock(2) is per open-file-description, so this MUST observe contention.
	second := &lock.FileLock{}
	releaseSecond, err := second.TryAcquire(path)
	if !errors.Is(err, lock.ErrLocked) {
		t.Fatalf("second TryAcquire: want ErrLocked, got err=%v release!=nil=%t", err, releaseSecond != nil)
	}
	if releaseSecond != nil {
		t.Fatal("second TryAcquire: release must be nil on error")
	}
}

func TestTryAcquire_reAcquirableAfterRelease(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "test.lock")

	first := &lock.FileLock{}
	releaseFirst, err := first.TryAcquire(path)
	if err != nil {
		t.Fatalf("first TryAcquire: unexpected error: %v", err)
	}
	if relErr := releaseFirst(); relErr != nil {
		t.Fatalf("first release: unexpected error: %v", relErr)
	}

	second := &lock.FileLock{}
	releaseSecond, err := second.TryAcquire(path)
	if err != nil {
		t.Fatalf("second TryAcquire after release: unexpected error: %v", err)
	}
	t.Cleanup(func() {
		if relErr := releaseSecond(); relErr != nil {
			t.Errorf("second release: unexpected error: %v", relErr)
		}
	})
}

func TestTryAcquire_failsOnMissingParentDir(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "does-not-exist", "test.lock")
	fl := &lock.FileLock{}

	release, err := fl.TryAcquire(path)
	if err == nil {
		t.Fatal("TryAcquire on missing dir: want error, got nil")
	}
	if errors.Is(err, lock.ErrLocked) {
		t.Fatalf("missing-dir error should NOT be ErrLocked: %v", err)
	}
	if release != nil {
		t.Fatal("release must be nil on error")
	}
}
