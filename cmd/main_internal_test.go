// White-box tests for the root logger wiring. The `_internal_test.go` suffix
// matches the testpackage linter's skip-regexp so `package main` is permitted,
// letting the tests drive the unexported initLogger / parseLogLevel /
// leveledLogger helpers directly.

package main

import (
	"errors"
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

func Test_leveledLogger_applies_config_level(t *testing.T) {
	t.Parallel()

	got, err := leveledLogger(initLogger(false), "debug", false)
	if err != nil {
		t.Fatalf("leveledLogger: %v", err)
	}
	if got.GetLevel() != zerolog.DebugLevel {
		t.Errorf("leveledLogger level = %v, want %v", got.GetLevel(), zerolog.DebugLevel)
	}
}

func Test_leveledLogger_verbose_overrides_config(t *testing.T) {
	t.Parallel()

	got, err := leveledLogger(initLogger(true), "error", true)
	if err != nil {
		t.Fatalf("leveledLogger: %v", err)
	}
	if got.GetLevel() != zerolog.TraceLevel {
		t.Errorf("verbose override level = %v, want %v", got.GetLevel(), zerolog.TraceLevel)
	}
}

func Test_leveledLogger_rejects_invalid_config_level(t *testing.T) {
	t.Parallel()

	if _, err := leveledLogger(initLogger(false), "bogus", false); !errors.Is(err, errInvalidLogLevel) {
		t.Fatalf("leveledLogger error = %v, want errInvalidLogLevel", err)
	}
}
