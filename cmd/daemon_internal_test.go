package main

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/labi-le/tidal-syncer/pkg/tidal/auth"
)

// daemonTestMinCycles is the number of cycles a looping test waits for before it
// considers the poll loop proven and cancels the daemon.
const daemonTestMinCycles = 2

// cycleCounter builds daemon cycle functions that count their invocations and
// close reached once the loop has run daemonTestMinCycles times, letting a test
// prove the daemon keeps polling without sleeping on wall-clock time.
type cycleCounter struct {
	count   atomic.Int64
	once    sync.Once
	reached chan struct{}
}

// cycle returns a daemon cycle func that always yields err; capturing err keeps
// the loop-termination behavior (nil vs ErrDeadCredentials) caller-selected.
func (c *cycleCounter) cycle(err error) func(context.Context) error {
	return func(_ context.Context) error {
		if c.count.Add(1) >= daemonTestMinCycles {
			c.once.Do(func() { close(c.reached) })
		}

		return err
	}
}

// TestRunDaemon_LoopRunsMultipleCycles proves the daemon runs a cycle
// immediately and then keeps polling on the ticker until the context cancels.
func TestRunDaemon_LoopRunsMultipleCycles(t *testing.T) {
	t.Parallel()

	log := zerolog.Nop()
	cc := &cycleCounter{reached: make(chan struct{})}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- runDaemon(ctx, &log, time.Millisecond, cc.cycle(nil), noopReauth) }()

	select {
	case <-cc.reached:
	case <-time.After(time.Second):
		t.Fatal("daemon did not run multiple cycles")
	}

	cancel()

	requireDaemonExit(t, done)
}

// TestRunDaemon_GracefulExitOnCancel proves a cancelled context makes the daemon
// return nil promptly: the SIGTERM/SIGINT graceful-shutdown path.
func TestRunDaemon_GracefulExitOnCancel(t *testing.T) {
	t.Parallel()

	log := zerolog.Nop()
	cc := &cycleCounter{reached: make(chan struct{})}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() { done <- runDaemon(ctx, &log, time.Hour, cc.cycle(nil), noopReauth) }()

	requireDaemonExit(t, done)
}

// TestRunDaemon_DeadCredentialsKeepsLooping proves a dead-credentials error is
// logged but never fatal: the daemon keeps polling instead of returning.
func TestRunDaemon_DeadCredentialsKeepsLooping(t *testing.T) {
	t.Parallel()

	log := zerolog.Nop()
	cc := &cycleCounter{reached: make(chan struct{})}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- runDaemon(ctx, &log, time.Millisecond, cc.cycle(auth.ErrDeadCredentials), noopReauth)
	}()

	select {
	case <-cc.reached:
	case <-time.After(time.Second):
		t.Fatal("daemon stopped looping after dead-credentials error")
	}

	cancel()

	requireDaemonExit(t, done)
}

// noopReauth is a re-auth stub for daemon tests that need a fresh-link callback
// but must not perform real network or store I/O.
func noopReauth(context.Context) error { return nil }

// TestRunDaemon_DeadCredentialsTriggersReauth proves the daemon invokes the
// re-auth callback (which emits a fresh login link) when a cycle reports dead
// credentials, and keeps polling afterwards.
func TestRunDaemon_DeadCredentialsTriggersReauth(t *testing.T) {
	t.Parallel()

	log := zerolog.Nop()
	cc := &cycleCounter{reached: make(chan struct{})}

	var reauthCalls atomic.Int64

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- runDaemon(ctx, &log, time.Millisecond, cc.cycle(auth.ErrDeadCredentials),
			func(context.Context) error {
				reauthCalls.Add(1)

				return nil
			})
	}()

	select {
	case <-cc.reached:
	case <-time.After(time.Second):
		t.Fatal("daemon did not loop on dead-credentials error")
	}

	cancel()
	requireDaemonExit(t, done)

	if reauthCalls.Load() == 0 {
		t.Fatal("reauth callback was never invoked on ErrDeadCredentials")
	}
}

// requireDaemonExit fails the test unless runDaemon returns nil promptly after
// the context is cancelled.
func requireDaemonExit(t *testing.T, done <-chan error) {
	t.Helper()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runDaemon returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runDaemon did not return after cancel")
	}
}
