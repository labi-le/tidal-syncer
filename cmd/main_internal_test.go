// White-box tests for the root logger wiring. The `_internal_test.go` suffix
// matches the testpackage linter's skip-regexp so `package main` is permitted,
// letting the tests drive the unexported initLogger / parseLogLevel /
// buildLogger helpers directly.

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

func Test_initLogger_defaults_to_info_and_trace_when_verbose(t *testing.T) {
	t.Parallel()

	if got := initLogger(false).GetLevel(); got != zerolog.InfoLevel {
		t.Errorf("initLogger(false) level = %v, want %v", got, zerolog.InfoLevel)
	}
	if got := initLogger(true).GetLevel(); got != zerolog.TraceLevel {
		t.Errorf("initLogger(true) level = %v, want %v", got, zerolog.TraceLevel)
	}
}

func Test_parseLogLevel_resolves_config_levels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		level string
		want  zerolog.Level
	}{
		{"trace", "trace", zerolog.TraceLevel},
		{"debug", "debug", zerolog.DebugLevel},
		{"info", "info", zerolog.InfoLevel},
		{"warn", "warn", zerolog.WarnLevel},
		{"error", "error", zerolog.ErrorLevel},
		{"empty falls back to info", "", zerolog.InfoLevel},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseLogLevel(tc.level, false)
			if err != nil {
				t.Fatalf("parseLogLevel(%q): %v", tc.level, err)
			}
			if got != tc.want {
				t.Errorf("parseLogLevel(%q) = %v, want %v", tc.level, got, tc.want)
			}
		})
	}
}

func Test_parseLogLevel_rejects_unknown_level(t *testing.T) {
	t.Parallel()

	if _, err := parseLogLevel("bogus", false); !errors.Is(err, errInvalidLogLevel) {
		t.Fatalf("parseLogLevel(bogus) error = %v, want errInvalidLogLevel", err)
	}
}

func Test_buildLogger_applies_config_level(t *testing.T) {
	t.Parallel()

	got, err := buildLogger(io.Discard, "console", "debug", false)
	if err != nil {
		t.Fatalf("buildLogger: %v", err)
	}
	if got.GetLevel() != zerolog.DebugLevel {
		t.Errorf("buildLogger level = %v, want %v", got.GetLevel(), zerolog.DebugLevel)
	}
}

func Test_buildLogger_verbose_overrides_config(t *testing.T) {
	t.Parallel()

	got, err := buildLogger(io.Discard, "console", "error", true)
	if err != nil {
		t.Fatalf("buildLogger: %v", err)
	}
	if got.GetLevel() != zerolog.TraceLevel {
		t.Errorf("verbose override level = %v, want %v", got.GetLevel(), zerolog.TraceLevel)
	}
}

func Test_buildLogger_rejects_invalid_config_level(t *testing.T) {
	t.Parallel()

	if _, err := buildLogger(io.Discard, "console", "bogus", false); !errors.Is(err, errInvalidLogLevel) {
		t.Fatalf("buildLogger error = %v, want errInvalidLogLevel", err)
	}
}

// Test_buildLogger_json_format_emits_parseable_json asserts that format "json"
// selects the raw JSON writer: a logged line is a single valid JSON object
// carrying the message and fields.
func Test_buildLogger_json_format_emits_parseable_json(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	lg, err := buildLogger(&buf, "json", "info", false)
	if err != nil {
		t.Fatalf("buildLogger: %v", err)
	}

	lg.Info().Str("phase", "startup").Msg("hello")

	var fields map[string]any
	if err = json.Unmarshal(buf.Bytes(), &fields); err != nil {
		t.Fatalf("json format did not emit parseable JSON: %v; raw=%q", err, buf.String())
	}
	if fields["message"] != "hello" {
		t.Errorf("message = %v, want hello", fields["message"])
	}
	if fields["phase"] != "startup" {
		t.Errorf("phase = %v, want startup", fields["phase"])
	}
}

// Test_buildLogger_console_format_is_not_json asserts the default console format
// does NOT emit raw JSON (it flows through the ConsoleWriter), proving the
// writer is selected by format rather than fixed at bootstrap.
func Test_buildLogger_console_format_is_not_json(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	lg, err := buildLogger(&buf, "console", "info", false)
	if err != nil {
		t.Fatalf("buildLogger: %v", err)
	}

	lg.Info().Msg("hello")

	if out := strings.TrimSpace(buf.String()); json.Valid([]byte(out)) {
		t.Errorf("console format unexpectedly emitted valid JSON: %q", out)
	}
	if !strings.Contains(buf.String(), "hello") {
		t.Errorf("console output missing message; got %q", buf.String())
	}
}

// Test_version_runs_without_config asserts the version subcommand succeeds even
// when no config file exists, since it reads no configuration.
func Test_version_runs_without_config(t *testing.T) {
	t.Parallel()

	root := newRootCmd()
	root.SetArgs([]string{"version", "--config", filepath.Join(t.TempDir(), "nonexistent.yaml")})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)

	if err := root.Execute(); err != nil {
		t.Fatalf("version with no config: %v", err)
	}
}
