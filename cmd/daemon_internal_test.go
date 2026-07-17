package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/labi-le/tidal-syncer/internal/config"
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

// failingCycle builds a daemon cycle func that always yields err.
func failingCycle(err error) func(context.Context) error {
	return func(context.Context) error { return err }
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
	go func() {
		done <- runDaemon(ctx, &log, pollingDaemonConfig(time.Millisecond), cc.cycle(nil))
	}()

	select {
	case <-cc.reached:
	case <-time.After(time.Second):
		t.Fatal("daemon did not run multiple cycles")
	}

	cancel()

	requireDaemonExit(t, done)
}

// TestRunDaemon_UnknownModeReturnsError proves an unrecognized daemon mode is
// rejected with errUnknownDaemonMode instead of silently no-opping (the old
// two-if form exited 0 without ever running a cycle).
func TestRunDaemon_UnknownModeReturnsError(t *testing.T) {
	t.Parallel()

	log := zerolog.Nop()
	cc := &cycleCounter{reached: make(chan struct{})}

	err := runDaemon(context.Background(), &log, config.Daemon{Mode: "bogus"}, cc.cycle(nil))
	if !errors.Is(err, errUnknownDaemonMode) {
		t.Fatalf("runDaemon(mode=bogus) error = %v, want errUnknownDaemonMode", err)
	}
	if got := cc.count.Load(); got != 0 {
		t.Errorf("cycle ran %d times for unknown mode; want 0", got)
	}
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
	go func() {
		done <- runDaemon(ctx, &log, pollingDaemonConfig(time.Hour), cc.cycle(nil))
	}()

	requireDaemonExit(t, done)
}

// TestRunDaemon_DeadCredentialsKeepsLooping proves a dead-credentials error is
// logged but never fatal: the daemon keeps polling instead of exiting.
func TestRunDaemon_DeadCredentialsKeepsLooping(t *testing.T) {
	t.Parallel()

	log := zerolog.Nop()
	cc := &cycleCounter{reached: make(chan struct{})}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- runDaemon(ctx, &log, pollingDaemonConfig(time.Millisecond), cc.cycle(auth.ErrDeadCredentials))
	}()

	select {
	case <-cc.reached:
	case <-time.After(time.Second):
		t.Fatal("daemon stopped looping after dead-credentials error")
	}

	cancel()

	requireDaemonExit(t, done)
}

func pollingDaemonConfig(delay time.Duration) config.Daemon {
	return config.Daemon{
		Mode:    config.DaemonModePolling,
		Polling: config.DurationRange{Min: delay, Max: delay},
	}
}

// TestRunDaemonCycle_ReauthRequired proves a revoked refresh token logs a single
// actionable instruction to re-run the login command and returns nil, so the
// daemon keeps polling rather than hanging or starting a device flow itself.
func TestRunDaemonCycle_ReauthRequired(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	lg := zerolog.New(&buf)

	if err := runDaemonCycle(t.Context(), &lg, failingCycle(auth.ErrReauthRequired)); err != nil {
		t.Fatalf("runDaemonCycle on reauth-required: got %v, want nil (keep polling)", err)
	}

	logs := buf.String()
	if !strings.Contains(logs, "tidal-syncer login") {
		t.Errorf("reauth message must tell the operator to run 'tidal-syncer login'; logs=%s", logs)
	}
	if strings.Contains(logs, "client_id") {
		t.Errorf("reauth path must not raise the dead-credentials alert; logs=%s", logs)
	}
}

// TestRunDaemonCycle_DeadCreds proves dead client credentials raise a distinct
// operator alert naming the config keys.
func TestRunDaemonCycle_DeadCreds(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	lg := zerolog.New(&buf)

	if err := runDaemonCycle(t.Context(), &lg, failingCycle(auth.ErrDeadCredentials)); err != nil {
		t.Fatalf("runDaemonCycle on dead credentials: got %v, want nil", err)
	}

	logs := buf.String()
	if !strings.Contains(logs, "client_id") || !strings.Contains(logs, "config.yaml") {
		t.Errorf("dead-credentials alert must name client_id and config.yaml; logs=%s", logs)
	}
}

// TestRunDaemonCycle_Transient proves an unclassified (transient) cycle error is
// neither fatal nor a re-auth instruction: the daemon just retries next tick.
func TestRunDaemonCycle_Transient(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	lg := zerolog.New(&buf)

	if err := runDaemonCycle(t.Context(), &lg, failingCycle(errors.New("network blip"))); err != nil {
		t.Fatalf("runDaemonCycle on transient error: got %v, want nil", err)
	}

	logs := buf.String()
	if strings.Contains(logs, "client_id") || strings.Contains(logs, "tidal-syncer login") {
		t.Errorf("transient error must not raise a reauth/dead-credentials alert; logs=%s", logs)
	}
}

// TestRunDaemonCycle_Canceled proves a cancelled cycle returns the shutdown
// signal so the loop exits gracefully.
func TestRunDaemonCycle_Canceled(t *testing.T) {
	t.Parallel()

	log := zerolog.Nop()

	got := runDaemonCycle(context.Background(), &log, failingCycle(context.Canceled))
	if !errors.Is(got, context.Canceled) {
		t.Fatalf("runDaemonCycle on cancellation: got %v, want context.Canceled", got)
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
