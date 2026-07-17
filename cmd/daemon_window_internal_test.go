package main

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/labi-le/tidal-syncer/internal/config"
)

func TestNextTimeWindowStart_ReturnsCurrentStartWhenBeforeWindow(t *testing.T) {
	loc := time.FixedZone("UTC+3", 3*60*60)
	now := time.Date(2026, 7, 1, 1, 0, 0, 0, loc)
	window := config.DaemonTimeWindow{Start: "03:00", End: "05:00", Min: 10 * time.Minute, Max: 20 * time.Minute}

	got, err := nextTimeWindowStart(now, window)
	if err != nil {
		t.Fatalf("nextTimeWindowStart() error = %v", err)
	}
	want := time.Date(2026, 7, 1, 3, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("next start = %s, want %s", got, want)
	}
}

func TestWindowContainsNow_HandlesMidnightCrossing(t *testing.T) {
	loc := time.FixedZone("UTC+3", 3*60*60)
	window := config.DaemonTimeWindow{Start: "23:00", End: "02:00", Min: 10 * time.Minute, Max: 20 * time.Minute}
	now := time.Date(2026, 7, 2, 1, 30, 0, 0, loc)

	inside, err := windowContainsNow(now, window)
	if err != nil {
		t.Fatalf("windowContainsNow() error = %v", err)
	}
	if !inside {
		t.Fatal("expected 01:30 to be inside 23:00-02:00 window")
	}
}

func TestRunDaemon_TimeWindowSleepsUntilWindowThenRuns(t *testing.T) {
	log := zerolog.Nop()
	cc := &cycleCounter{reached: make(chan struct{})}
	loc := time.FixedZone("UTC+3", 3*60*60)
	times := []time.Time{
		time.Date(2026, 7, 1, 1, 0, 0, 0, loc),
		time.Date(2026, 7, 1, 3, 0, 0, 0, loc),
		time.Date(2026, 7, 1, 3, 20, 0, 0, loc),
	}
	var nowIndex int
	oldNow := daemonNowFn
	oldWait := daemonWaitFn
	oldDelay := daemonDelayFn
	daemonNowFn = func() time.Time {
		if nowIndex >= len(times) {
			return times[len(times)-1]
		}
		current := times[nowIndex]
		nowIndex++

		return current
	}
	var waits []time.Duration
	daemonWaitFn = func(ctx context.Context, delay time.Duration) error {
		waits = append(waits, delay)
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	daemonDelayFn = func(rng config.DurationRange) time.Duration {
		return rng.Min
	}
	t.Cleanup(func() {
		daemonNowFn = oldNow
		daemonWaitFn = oldWait
		daemonDelayFn = oldDelay
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runDaemon(ctx, &log, config.Daemon{
			Mode: config.DaemonModeTimeWindow,
			TimeWindow: config.DaemonTimeWindow{
				Start: "03:00",
				End:   "05:00",
				Min:   20 * time.Minute,
				Max:   20 * time.Minute,
			},
		}, cc.cycle(nil), nil)
	}()

	select {
	case <-cc.reached:
	case <-time.After(time.Second):
		t.Fatal("daemon did not run inside time window")
	}

	cancel()
	requireDaemonExit(t, done)
	if len(waits) < 2 {
		t.Fatalf("waits = %d, want at least 2", len(waits))
	}
	if waits[0] != 2*time.Hour {
		t.Fatalf("first wait = %s, want 2h until window start", waits[0])
	}
	if waits[1] != 20*time.Minute {
		t.Fatalf("second wait = %s, want 20m in-window delay", waits[1])
	}
}
