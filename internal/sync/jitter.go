package sync

import (
	"context"
	"time"

	"github.com/labi-le/tidal-syncer/internal/config"
)

var workerDelayFn = func(rng config.DurationRange) time.Duration {
	return rng.Random()
}

var waitForDelay = func(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
