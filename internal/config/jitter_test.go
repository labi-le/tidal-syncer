package config_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labi-le/tidal-syncer/internal/config"
)

func TestLoadAppliesWorkerAndPollingRanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jitter.yaml")
	const workerMin = 2 * time.Second
	const workerMax = 5 * time.Second
	const pollingMin = 10 * time.Minute
	const pollingMax = 12 * time.Minute

	contents := fmt.Sprintf(
		"tidal_auth:\n  client_id: id\n  client_secret: secret\njitter:\n  worker:\n    min: %s\n    max: %s\ndaemon:\n  mode: polling\n  polling:\n    min: %s\n    max: %s\n",
		workerMin,
		workerMax,
		pollingMin,
		pollingMax,
	)
	if err := os.WriteFile(path, []byte(contents), secureFileMode); err != nil {
		t.Fatalf("write jitter config: %v", err)
	}

	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load(%q) returned error: %v", path, err)
	}

	if got.Jitter.Worker.Min != workerMin || got.Jitter.Worker.Max != workerMax {
		t.Fatalf("worker jitter = %+v, want min=%s max=%s", got.Jitter.Worker, workerMin, workerMax)
	}
	if got.Daemon.Mode != config.DaemonModePolling {
		t.Fatalf("daemon mode = %q, want %q", got.Daemon.Mode, config.DaemonModePolling)
	}
	if got.Daemon.Polling.Min != pollingMin || got.Daemon.Polling.Max != pollingMax {
		t.Fatalf("daemon polling = %+v, want min=%s max=%s", got.Daemon.Polling, pollingMin, pollingMax)
	}
}

func TestLoadAppliesTimeWindowMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "time-window.yaml")
	const windowMin = 10 * time.Minute
	const windowMax = 20 * time.Minute

	contents := fmt.Sprintf(
		"tidal_auth:\n  client_id: id\n  client_secret: secret\ndaemon:\n  mode: time_window\n  time_window:\n    start: \"03:00\"\n    end: \"05:00\"\n    min: %s\n    max: %s\n",
		windowMin,
		windowMax,
	)
	if err := os.WriteFile(path, []byte(contents), secureFileMode); err != nil {
		t.Fatalf("write time-window config: %v", err)
	}

	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load(%q) returned error: %v", path, err)
	}

	if got.Daemon.Mode != config.DaemonModeTimeWindow {
		t.Fatalf("daemon mode = %q, want %q", got.Daemon.Mode, config.DaemonModeTimeWindow)
	}
	if got.Daemon.TimeWindow.Start != "03:00" || got.Daemon.TimeWindow.End != "05:00" {
		t.Fatalf("time window = %+v, want 03:00-05:00", got.Daemon.TimeWindow)
	}
	if got.Daemon.TimeWindow.Min != windowMin || got.Daemon.TimeWindow.Max != windowMax {
		t.Fatalf("time window delays = %+v, want min=%s max=%s", got.Daemon.TimeWindow, windowMin, windowMax)
	}
}

func TestLoadMapsLegacyDaemonIntervalToPollingRange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy-daemon.yaml")
	const legacyInterval = 22 * time.Minute

	contents := fmt.Sprintf(
		"tidal_auth:\n  client_id: id\n  client_secret: secret\ndaemon:\n  interval: %s\n",
		legacyInterval,
	)
	if err := os.WriteFile(path, []byte(contents), secureFileMode); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}

	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load(%q) returned error: %v", path, err)
	}

	if got.Daemon.Polling.Min != legacyInterval || got.Daemon.Polling.Max != legacyInterval {
		t.Fatalf("daemon polling = %+v, want legacy interval %s mapped to min=max", got.Daemon.Polling, legacyInterval)
	}
}

func TestValidateRejectsInvalidJitterRanges(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*config.Config)
		wantSubstr []string
	}{
		{
			name: "worker min greater than max",
			mutate: func(c *config.Config) {
				c.Jitter.Worker.Min = 5 * time.Second
				c.Jitter.Worker.Max = 2 * time.Second
			},
			wantSubstr: []string{"jitter.worker", "min", "max"},
		},
		{
			name: "worker min negative",
			mutate: func(c *config.Config) {
				c.Jitter.Worker.Min = -time.Second
			},
			wantSubstr: []string{"jitter.worker.min"},
		},
		{
			name: "daemon polling max negative",
			mutate: func(c *config.Config) {
				c.Daemon.Polling.Max = -time.Second
			},
			wantSubstr: []string{"daemon.polling.max"},
		},
		{
			name: "daemon polling min greater than max",
			mutate: func(c *config.Config) {
				c.Daemon.Mode = config.DaemonModePolling
				c.Daemon.Polling.Min = 2 * time.Minute
				c.Daemon.Polling.Max = time.Minute
			},
			wantSubstr: []string{"daemon.polling", "min", "max"},
		},
		{
			name: "daemon mode unknown",
			mutate: func(c *config.Config) {
				c.Daemon.Mode = "other"
			},
			wantSubstr: []string{"daemon.mode"},
		},
		{
			name: "time window start malformed",
			mutate: func(c *config.Config) {
				c.Daemon.Mode = config.DaemonModeTimeWindow
				c.Daemon.TimeWindow.Start = "3:00"
				c.Daemon.TimeWindow.End = "05:00"
				c.Daemon.TimeWindow.Min = 10 * time.Minute
				c.Daemon.TimeWindow.Max = 20 * time.Minute
			},
			wantSubstr: []string{"daemon.time_window.start"},
		},
		{
			name: "time window min greater than max",
			mutate: func(c *config.Config) {
				c.Daemon.Mode = config.DaemonModeTimeWindow
				c.Daemon.TimeWindow.Start = "23:00"
				c.Daemon.TimeWindow.End = "02:00"
				c.Daemon.TimeWindow.Min = 30 * time.Minute
				c.Daemon.TimeWindow.Max = 10 * time.Minute
			},
			wantSubstr: []string{"daemon.time_window", "min", "max"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(&cfg)

			err := cfg.Validate()
			if err == nil {
				t.Fatal("Validate() = nil, want error")
			}
			for _, sub := range tt.wantSubstr {
				if !strings.Contains(err.Error(), sub) {
					t.Fatalf("error %q missing substring %q", err.Error(), sub)
				}
			}
		})
	}
}
