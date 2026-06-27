// White-box tests for the health + selfcheck command wiring. The
// `_internal_test.go` suffix matches the testpackage linter's skip-regexp so
// `package main` is permitted, letting the tests drive the unexported
// runHealth / runSelfcheck / resolveFFmpegPath / firstLine helpers directly.

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

const healthConfigFileMode os.FileMode = 0o600

// writeHealthConfig writes a minimal valid config pointing at dataDir and
// returns the config path.
func writeHealthConfig(t *testing.T, dataDir string) string {
	t.Helper()

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	content := fmt.Sprintf("paths:\n  data: %q\n", dataDir)
	if err := os.WriteFile(cfgPath, []byte(content), healthConfigFileMode); err != nil {
		t.Fatalf("write config: %v", err)
	}

	return cfgPath
}

func TestRunHealthSucceedsOnWritableDataDir(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	cfgPath := writeHealthConfig(t, dataDir)

	var buf bytes.Buffer
	lg := zerolog.New(&buf)

	if err := runHealth(t.Context(), cfgPath, false, lg); err != nil {
		t.Fatalf("runHealth: %v", err)
	}

	if logs := buf.String(); !strings.Contains(logs, "healthy") {
		t.Errorf("healthy line not logged; logs=%s", logs)
	}
}

func TestRunHealthFailsOnMissingConfig(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	lg := zerolog.New(&buf)

	err := runHealth(t.Context(), filepath.Join(t.TempDir(), "nonexistent.yaml"), false, lg)
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
	if !strings.Contains(err.Error(), "health") {
		t.Errorf("error not namespaced; err=%v", err)
	}
}

func TestRunHealthFailsOnUnopenableStore(t *testing.T) {
	t.Parallel()

	// data dir points at an existing FILE (not a directory) → store.Open
	// either fails to stat or Migrate fails to create the DB beneath it.
	bogus := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(bogus, []byte("x"), healthConfigFileMode); err != nil {
		t.Fatalf("seed bogus path: %v", err)
	}
	cfgPath := writeHealthConfig(t, bogus)

	var buf bytes.Buffer
	lg := zerolog.New(&buf)

	if err := runHealth(t.Context(), cfgPath, false, lg); err == nil {
		t.Fatal("expected error when data path is not a directory")
	}
}

func TestRunSelfcheckLogsBanner(t *testing.T) {
	t.Parallel()

	// Use `echo` as a stand-in for ffmpeg: it accepts the -version arg as a
	// literal and emits a single banner line. runSelfcheck cares only that
	// the binary exists, exits 0, and prints stdout. LookPath rather than a
	// hard-coded /bin/echo so the test works in the nix-shell sandbox too.
	echoPath, err := exec.LookPath("echo")
	if err != nil {
		t.Skipf("echo not on PATH: %v", err)
	}

	cfgPath := writeHealthConfig(t, t.TempDir())

	var buf bytes.Buffer
	lg := zerolog.New(&buf)

	if err = runSelfcheck(t.Context(), cfgPath, echoPath, false, lg); err != nil {
		t.Fatalf("runSelfcheck: %v", err)
	}

	logs := buf.String()
	if !strings.Contains(logs, "config and store ok") {
		t.Errorf("config and store status not logged; logs=%s", logs)
	}
	if !strings.Contains(logs, "ffmpeg ok") {
		t.Errorf("ffmpeg ok line missing; logs=%s", logs)
	}
	if !strings.Contains(logs, "-version") {
		t.Errorf("banner (echo's args) not surfaced; logs=%s", logs)
	}
}

func TestRunSelfcheckFailsOnMissingBinary(t *testing.T) {
	t.Parallel()

	cfgPath := writeHealthConfig(t, t.TempDir())
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	var buf bytes.Buffer
	lg := zerolog.New(&buf)

	err := runSelfcheck(t.Context(), cfgPath, missing, false, lg)
	if err == nil {
		t.Fatal("expected error for missing ffmpeg binary")
	}
	if !strings.Contains(err.Error(), "selfcheck") {
		t.Errorf("error not namespaced; err=%v", err)
	}
}

func TestRunSelfcheckFailsOnMissingConfig(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	lg := zerolog.New(&buf)

	err := runSelfcheck(t.Context(), filepath.Join(t.TempDir(), "nope.yaml"), "/bin/echo", false, lg)
	if err == nil {
		t.Fatal("expected error for missing config")
	}
	if !strings.Contains(err.Error(), "selfcheck") {
		t.Errorf("error not namespaced; err=%v", err)
	}
}

func TestResolveFFmpegPathPrefersEnvVar(t *testing.T) {
	const overridePath = "/opt/custom/ffmpeg"
	t.Setenv(ffmpegEnvVar, overridePath)

	if got := resolveFFmpegPath(); got != overridePath {
		t.Errorf("resolveFFmpegPath: got %q, want %q", got, overridePath)
	}
}

func TestResolveFFmpegPathFallsBackToDefault(t *testing.T) {
	t.Setenv(ffmpegEnvVar, "")

	if got := resolveFFmpegPath(); got != defaultFFmpegPath {
		t.Errorf("resolveFFmpegPath: got %q, want %q", got, defaultFFmpegPath)
	}
}

func TestFirstLineHandlesEmptyInput(t *testing.T) {
	t.Parallel()

	if got := firstLine(nil); got != "" {
		t.Errorf("firstLine(nil): got %q, want empty", got)
	}
	if got := firstLine([]byte("hello\nworld\n")); got != "hello" {
		t.Errorf("firstLine: got %q, want %q", got, "hello")
	}
}
