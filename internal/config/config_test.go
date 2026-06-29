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

const exampleConfigPath = "../../config.example.yaml"

const (
	exampleInterval    = 15 * time.Minute
	exampleConcurrency = 3
	fileConcurrency    = 5
	highConcurrency    = 99
	lowConcurrency     = 0
	subMinInterval     = 30 * time.Second
	secureFileMode     = 0o600
)

func TestLoadExampleMatchesExpected(t *testing.T) {
	got, err := config.Load(exampleConfigPath)
	if err != nil {
		t.Fatalf("Load(%q) returned error: %v", exampleConfigPath, err)
	}

	want := config.Config{
		Paths: config.Paths{
			Music:  "/app/Music",
			Config: "/app/config.yaml",
			Data:   "/app/data",
		},
		PathTemplate: "{albumartist}/{album}/{track} - {title}.{ext}",
		Scope: config.Scope{
			All: false,
			Favorites: config.Favorites{
				Tracks:    true,
				Albums:    true,
				Playlists: true,
			},
		},
		Quality: config.Quality{
			Request: "HI_RES_LOSSLESS",
			Floor:   "LOSSLESS",
		},
		Lyrics: config.Lyrics{
			Embed:   true,
			Sidecar: true,
		},
		Removal: config.Removal{Policy: "keep"},
		Daemon: config.Daemon{
			Mode:       config.DaemonModePolling,
			Interval:   exampleInterval,
			Polling:    config.DurationRange{Min: exampleInterval, Max: exampleInterval},
			TimeWindow: config.DaemonTimeWindow{Start: "03:00", End: "05:00", Min: exampleInterval, Max: 30 * time.Minute},
		},
		Jitter:      config.Jitter{Worker: config.DurationRange{Min: 0, Max: 0}},
		Concurrency: exampleConcurrency,
		TidalAuth: config.TidalAuth{
			ClientID:     "your-tidal-client-id",
			ClientSecret: "your-tidal-client-secret",
		},
		Log: config.Log{
			Level:  "info",
			Format: "console",
		},
	}

	if got != want {
		t.Errorf("Load(example) mismatch:\n got = %+v\nwant = %+v", got, want)
	}
}

func TestLoadAppliesDefaultsForOmittedFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "partial.yaml")
	partial := fmt.Sprintf("concurrency: %d\ntidal_auth:\n  client_id: id\n  client_secret: secret\n", fileConcurrency)
	if err := os.WriteFile(path, []byte(partial), secureFileMode); err != nil {
		t.Fatalf("write partial config: %v", err)
	}

	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load(%q) returned error: %v", path, err)
	}

	defaults := config.Defaults()
	if got.Concurrency != fileConcurrency {
		t.Errorf("Concurrency = %d, want %d from file", got.Concurrency, fileConcurrency)
	}
	if got.Paths.Music != defaults.Paths.Music {
		t.Errorf("Paths.Music = %q, want default %q", got.Paths.Music, defaults.Paths.Music)
	}
	if got.Quality.Floor != defaults.Quality.Floor {
		t.Errorf("Quality.Floor = %q, want default %q", got.Quality.Floor, defaults.Quality.Floor)
	}
	if got.Daemon.Interval != defaults.Daemon.Interval {
		t.Errorf("Daemon.Interval = %s, want default %s", got.Daemon.Interval, defaults.Daemon.Interval)
	}
	if !got.Lyrics.Embed {
		t.Error("Lyrics.Embed = false, want default true to survive overlay")
	}
}

func TestValidateRejectsInvalidFields(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(c *config.Config)
		wantSubstr []string
	}{
		{
			name:       "removal policy unknown",
			mutate:     func(c *config.Config) { c.Removal.Policy = "foo" },
			wantSubstr: []string{"removal.policy", "keep", "mirror", "trash"},
		},
		{
			name:       "concurrency above maximum",
			mutate:     func(c *config.Config) { c.Concurrency = highConcurrency },
			wantSubstr: []string{"concurrency"},
		},
		{
			name:       "concurrency below minimum",
			mutate:     func(c *config.Config) { c.Concurrency = lowConcurrency },
			wantSubstr: []string{"concurrency"},
		},
		{
			name:       "daemon interval below minimum",
			mutate:     func(c *config.Config) { c.Daemon.Polling = config.DurationRange{Min: subMinInterval, Max: time.Minute} },
			wantSubstr: []string{"daemon.polling"},
		},
		{
			name:       "quality floor lossy low",
			mutate:     func(c *config.Config) { c.Quality.Floor = "LOW" },
			wantSubstr: []string{"quality.floor", "LOSSLESS"},
		},
		{
			name:       "quality floor lossy high",
			mutate:     func(c *config.Config) { c.Quality.Floor = "HIGH" },
			wantSubstr: []string{"quality.floor"},
		},
		{
			name:       "path template missing title",
			mutate:     func(c *config.Config) { c.PathTemplate = "{albumartist}/{album}.{ext}" },
			wantSubstr: []string{"path_template", "title"},
		},
		{
			name:       "path template missing ext",
			mutate:     func(c *config.Config) { c.PathTemplate = "{albumartist}/{title}" },
			wantSubstr: []string{"path_template", "ext"},
		},
		{
			name:       "path template malformed braces",
			mutate:     func(c *config.Config) { c.PathTemplate = "{title}.{ext" },
			wantSubstr: []string{"path_template"},
		},
		{
			name:       "tidal_auth client_id empty",
			mutate:     func(c *config.Config) { c.TidalAuth.ClientID = "" },
			wantSubstr: []string{"tidal_auth.client_id"},
		},
		{
			name:       "tidal_auth client_secret empty",
			mutate:     func(c *config.Config) { c.TidalAuth.ClientSecret = "" },
			wantSubstr: []string{"tidal_auth.client_secret"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validConfig()
			tt.mutate(&c)

			err := c.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil, want error")
			}
			t.Logf("rejected %s: %v", tt.name, err)
			for _, sub := range tt.wantSubstr {
				if !strings.Contains(err.Error(), sub) {
					t.Errorf("error %q missing substring %q", err.Error(), sub)
				}
			}
		})
	}
}

func TestValidateAcceptsValidConfig(t *testing.T) {
	if err := validConfig().Validate(); err != nil {
		t.Errorf("validConfig().Validate() = %v, want nil", err)
	}
}

func TestValidateAcceptsHiResFloor(t *testing.T) {
	c := validConfig()
	c.Quality.Floor = "HI_RES_LOSSLESS"
	if err := c.Validate(); err != nil {
		t.Errorf("HI_RES_LOSSLESS floor should be valid, got %v", err)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := config.Load("/nonexistent/definitely/missing.yaml")
	if err == nil {
		t.Fatal("Load(missing) = nil error, want error")
	}
	t.Logf("missing file rejected: %v", err)
	if !strings.Contains(err.Error(), "read") {
		t.Errorf("error %q should mention read failure", err.Error())
	}
}

func TestLoadMalformedYAMLWrapped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.yaml")
	const malformed = "concurrency: [unterminated\n"
	if err := os.WriteFile(path, []byte(malformed), secureFileMode); err != nil {
		t.Fatalf("write broken config: %v", err)
	}

	_, err := config.Load(path)
	if err == nil {
		t.Fatal("Load(malformed) = nil error, want wrapped parse error")
	}
	t.Logf("malformed YAML rejected: %v", err)
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("error %q should mention parse failure", err.Error())
	}
}

func TestLoadRejectsDirectory(t *testing.T) {
	_, err := config.Load(t.TempDir())
	if err == nil {
		t.Fatal("Load(dir) = nil error, want error")
	}
	t.Logf("directory rejected: %v", err)
	if !strings.Contains(err.Error(), "directory") {
		t.Errorf("error %q should mention directory", err.Error())
	}
}

func validConfig() config.Config {
	c := config.Defaults()
	c.TidalAuth = config.TidalAuth{ClientID: "client-id", ClientSecret: "client-secret"}

	return c
}
