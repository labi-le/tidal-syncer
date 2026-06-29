package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/labi-le/tidal-syncer/internal/config"
)

func TestRunDaemon_UsesPollingRangeForEachWait(t *testing.T) {
	log := zerolog.Nop()
	cc := &cycleCounter{reached: make(chan struct{})}
	rangeCfg := config.DurationRange{Min: 3 * time.Second, Max: 7 * time.Second}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		mu        sync.Mutex
		durations []time.Duration
	)
	oldPick := daemonDelayFn
	oldWait := daemonWaitFn
	daemonDelayFn = func(config.DurationRange) time.Duration {
		return 5 * time.Second
	}
	daemonWaitFn = func(waitCtx context.Context, d time.Duration) error {
		mu.Lock()
		durations = append(durations, d)
		mu.Unlock()

		select {
		case <-waitCtx.Done():
			return waitCtx.Err()
		default:
			return nil
		}
	}
	t.Cleanup(func() {
		daemonDelayFn = oldPick
		daemonWaitFn = oldWait
	})

	done := make(chan error, 1)
	go func() {
		done <- runDaemon(ctx, &log, config.Daemon{Mode: config.DaemonModePolling, Polling: rangeCfg}, cc.cycle(nil))
	}()

	select {
	case <-cc.reached:
	case <-time.After(time.Second):
		t.Fatal("daemon did not run multiple cycles")
	}

	cancel()
	requireDaemonExit(t, done)

	mu.Lock()
	defer mu.Unlock()
	if len(durations) == 0 {
		t.Fatal("daemon did not wait between cycles")
	}
	for _, got := range durations {
		if got != 5*time.Second {
			t.Fatalf("wait duration = %s, want injected 5s", got)
		}
	}
}
