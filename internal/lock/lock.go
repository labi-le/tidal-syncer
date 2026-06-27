// Package lock provides cross-process advisory file locks via flock(2).
//
// A FileLock guards a single resource path against concurrent access by
// other processes (and other goroutines using a separate FileLock instance
// on the same path). Linux-only: it relies on flock(2) semantics where the
// lock is bound to the open file description, not the file or the process.
package lock

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// ErrLocked is returned by TryAcquire when the lock is held by another
// open file description (i.e. another process or another FileLock).
var ErrLocked = errors.New("lock: already held")

// lockFileMode is the permission bits used when the lockfile is created.
// 0o600 keeps it readable only by the owner; the lockfile carries no
// meaningful content.
const lockFileMode os.FileMode = 0o600

// FileLock acquires an exclusive advisory lock on a path using flock(2).
// The zero value is ready to use. A single FileLock holds at most one
// active lock at a time; call the returned release func before reusing it.
type FileLock struct{}

// TryAcquire attempts to take an exclusive non-blocking flock on path.
// On success it returns a release func that unlocks and closes the
// underlying fd. On contention it returns ErrLocked. Any other failure
// (e.g. missing parent directory, permission denied) is wrapped and
// returned verbatim.
func (*FileLock) TryAcquire(path string) (func() error, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, lockFileMode)
	if err != nil {
		return nil, fmt.Errorf("lock: open %q: %w", path, err)
	}

	if flockErr := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); flockErr != nil {
		if closeErr := file.Close(); closeErr != nil {
			return nil, errors.Join(
				fmt.Errorf("lock: flock %q: %w", path, flockErr),
				fmt.Errorf("lock: close %q: %w", path, closeErr),
			)
		}
		if errors.Is(flockErr, unix.EWOULDBLOCK) {
			return nil, ErrLocked
		}

		return nil, fmt.Errorf("lock: flock %q: %w", path, flockErr)
	}

	return releaserFor(file), nil
}

// releaserFor builds the release closure for an acquired flock. It
// unlocks first, then closes the fd; if either fails the error is
// wrapped so the caller knows which step broke.
func releaserFor(file *os.File) func() error {
	return func() error {
		unlockErr := unix.Flock(int(file.Fd()), unix.LOCK_UN)
		closeErr := file.Close()
		if unlockErr != nil {
			return fmt.Errorf("lock: unlock: %w", unlockErr)
		}
		if closeErr != nil {
			return fmt.Errorf("lock: close: %w", closeErr)
		}

		return nil
	}
}
