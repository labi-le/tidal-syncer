package sync

import (
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
)

// Summary reports the outcome of a single SyncOnce run.
type Summary struct {
	Downloaded int           // Tracks downloaded and tagged this run.
	Skipped    int           // Tracks already present at the requested quality.
	Failed     int           // Tracks whose processing failed and were skipped.
	Duration   time.Duration // Wall-clock time the run took.
}

// counters accumulates per-track outcomes across concurrent workers. Its atomic
// fields must never be copied, so it is always passed by pointer.
type counters struct {
	downloaded atomic.Int64
	skipped    atomic.Int64
	failed     atomic.Int64
}

// snapshot folds the live counters and elapsed duration into an immutable Summary.
func (c *counters) snapshot(elapsed time.Duration) Summary {
	return Summary{
		Downloaded: int(c.downloaded.Load()),
		Skipped:    int(c.skipped.Load()),
		Failed:     int(c.failed.Load()),
		Duration:   elapsed,
	}
}

// emit logs the summary at info level on logger.
func (s Summary) emit(logger zerolog.Logger) {
	logger.Info().
		Int("downloaded", s.Downloaded).
		Int("skipped", s.Skipped).
		Int("failed", s.Failed).
		Dur("duration", s.Duration).
		Msg("sync complete")
}
