package sync

import (
	"context"
	"fmt"
	"time"

	"github.com/labi-le/tidal-syncer/internal/config"
)

// randomWorkerDelay picks a random per-worker jitter delay within rng.
func randomWorkerDelay(rng config.DurationRange) time.Duration {
	return rng.Random()
}

// waitForDelay sleeps for delay, returning early if ctx is cancelled. A
// non-positive delay is a no-op.
func waitForDelay(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return fmt.Errorf("wait for jitter delay: %w", ctx.Err())
	case <-timer.C:
		return nil
	}
}
