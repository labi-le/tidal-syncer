package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/rs/zerolog"

	"github.com/labi-le/tidal-syncer/internal/lock"
)

// Test_newSyncCmd_uses_sync_name asserts the subcommand is registered under the
// "sync" verb with an executable RunE.
func Test_newSyncCmd_uses_sync_name(t *testing.T) {
	t.Parallel()

	// Given a config path, verbose flag and logger captured by the command closure
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	verbose := false
	logger := zerolog.Nop()

	// When the sync command is built
	cmd := newSyncCmd(&configPath, &verbose, &logger)

	// Then it is the "sync" verb and is runnable
	if cmd.Use != "sync" {
		t.Fatalf("Use = %q, want %q", cmd.Use, "sync")
	}
	if cmd.RunE == nil {
		t.Fatal("RunE = nil, want a runnable handler")
	}
}

// Test_runSync_reports_friendly_error_when_lock_held asserts that a second sync,
// finding the data-directory lock already held, fails fast with the friendly
// sentinel instead of panicking or reaching the network.
func Test_runSync_reports_friendly_error_when_lock_held(t *testing.T) {
	t.Parallel()

	// Given a valid config whose data-directory lock is already held
	ctx := context.Background()
	dataDir := t.TempDir()
	musicDir := t.TempDir()
	configPath := writeSyncConfig(t, dataDir, musicDir)

	release := acquireSyncLock(t, dataDir)
	defer func() { _ = release() }()

	// When a one-shot sync runs against the same data directory
	err := runSync(ctx, configPath, false, zerolog.Nop())

	// Then it returns the friendly "already running" sentinel
	if !errors.Is(err, errAnotherSyncRunning) {
		t.Fatalf("runSync error = %v, want errAnotherSyncRunning", err)
	}
}

// writeSyncConfig writes a minimal valid config pointing data and music at the
// given temp directories and returns its path. Every other field falls back to
// config.Defaults, which already validates.
func writeSyncConfig(t *testing.T, dataDir, musicDir string) string {
	t.Helper()

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	body := "paths:\n  data: " + dataDir + "\n  music: " + musicDir + "\n"
	if err := os.WriteFile(configPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	return configPath
}

// acquireSyncLock takes the exact flock runSync contends on, so the call under
// test observes a held lock. The returned release frees it.
func acquireSyncLock(t *testing.T, dataDir string) func() error {
	t.Helper()

	release, err := (&lock.FileLock{}).TryAcquire(filepath.Join(dataDir, lockFileName))
	if err != nil {
		t.Fatalf("pre-acquire lock: %v", err)
	}

	return release
}
