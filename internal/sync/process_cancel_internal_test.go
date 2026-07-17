package sync

import (
	"context"
	"fmt"
	"testing"

	"github.com/rs/zerolog"

	"github.com/labi-le/tidal-syncer/internal/config"
	"github.com/labi-le/tidal-syncer/pkg/tidal"
)

// A cancelled run must not count its in-flight tracks as failures: markFailed
// returns before touching the counters or the store when the cause is a context
// cancellation, so a shutdown mid-run leaves an accurate summary rather than an
// inflated Failed count.
func TestMarkFailed_ignoresContextCancellation(t *testing.T) {
	e := &Engine{
		config: config.Config{Quality: config.Quality{Request: tidal.QualityHiResLossless}},
		logger: zerolog.Nop(),
	}
	c := &counters{}

	cause := fmt.Errorf("download track 42: %w", context.Canceled)
	e.markFailed(context.Background(), zerolog.Nop(), tidal.Track{ID: 42}, cause, c)

	if got := c.failed.Load(); got != 0 {
		t.Fatalf("failed count = %d, want 0 for a cancelled track", got)
	}
}

// A deadline-exceeded cause is likewise not a track failure.
func TestMarkFailed_ignoresDeadlineExceeded(t *testing.T) {
	e := &Engine{
		config: config.Config{Quality: config.Quality{Request: tidal.QualityHiResLossless}},
		logger: zerolog.Nop(),
	}
	c := &counters{}

	cause := fmt.Errorf("download track 7: %w", context.DeadlineExceeded)
	e.markFailed(context.Background(), zerolog.Nop(), tidal.Track{ID: 7}, cause, c)

	if got := c.failed.Load(); got != 0 {
		t.Fatalf("failed count = %d, want 0 for a deadline-exceeded track", got)
	}
}
