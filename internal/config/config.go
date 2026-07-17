// Package config loads, defaults and validates the tidal-syncer configuration.
package config

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/goccy/go-yaml"

	"github.com/labi-le/tidal-syncer/pkg/tidal"
)

// Config is the fully-resolved tidal-syncer configuration.
type Config struct {
	Paths        Paths     `yaml:"paths"`
	PathTemplate string    `yaml:"path_template"`
	Scope        Scope     `yaml:"scope"`
	Quality      Quality   `yaml:"quality"`
	Lyrics       Lyrics    `yaml:"lyrics"`
	Removal      Removal   `yaml:"removal"`
	Daemon       Daemon    `yaml:"daemon"`
	Jitter       Jitter    `yaml:"jitter"`
	Concurrency  int       `yaml:"concurrency"`
	TidalAuth    TidalAuth `yaml:"tidal_auth"`
	Log          Log       `yaml:"log"`
	Metrics      Metrics   `yaml:"metrics"`
}

// Paths groups the filesystem locations tidal-syncer reads and writes.
type Paths struct {
	Music  string `yaml:"music"`
	Config string `yaml:"config"`
	Data   string `yaml:"data"`
}

// Scope selects which parts of the TIDAL library are synced.
type Scope struct {
	All       bool      `yaml:"all"`
	Favorites Favorites `yaml:"favorites"`
}

// Favorites toggles syncing of individual favorite collections.
type Favorites struct {
	Tracks    bool `yaml:"tracks"`
	Albums    bool `yaml:"albums"`
	Playlists bool `yaml:"playlists"`
}

// Quality describes the requested and minimum acceptable audio tiers.
type Quality struct {
	Request tidal.Quality `yaml:"request"`
	Floor   tidal.Quality `yaml:"floor"`
}

// Lyrics controls how lyrics are persisted.
type Lyrics struct {
	Embed   bool `yaml:"embed"`
	Sidecar bool `yaml:"sidecar"`
}

// Removal configures behavior when a track leaves the remote library.
type Removal struct {
	Policy string `yaml:"policy"`
}

// Daemon holds settings for scheduled background syncing.
type Daemon struct {
	Mode       string           `yaml:"mode"`
	Interval   time.Duration    `yaml:"interval"`
	Polling    DurationRange    `yaml:"polling"`
	TimeWindow DaemonTimeWindow `yaml:"time_window"`
}

type DaemonTimeWindow struct {
	Start string        `yaml:"start"`
	End   string        `yaml:"end"`
	Min   time.Duration `yaml:"min"`
	Max   time.Duration `yaml:"max"`
}

type Jitter struct {
	Worker DurationRange `yaml:"worker"`
}

type DurationRange struct {
	Min time.Duration `yaml:"min"`
	Max time.Duration `yaml:"max"`
}

// TidalAuth carries the TIDAL OAuth client credentials.
type TidalAuth struct {
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
}

// Log configures logging verbosity and output format.
type Log struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// Metrics configures the Prometheus metrics endpoint exposed by the daemon.
type Metrics struct {
	Enabled bool   `yaml:"enabled"`
	Address string `yaml:"address"`
}

const (
	DaemonModePolling    = "polling"
	DaemonModeTimeWindow = "time_window"

	defaultMusicPath    = "/app/Music"
	defaultConfigPath   = "/app/config.yaml"
	defaultDataPath     = "/app/data"
	defaultPathTemplate = "{albumartist}/{album}/{track} - {title}.{ext}"

	removalKeep   = "keep"
	removalMirror = "mirror"
	removalTrash  = "trash"

	defaultRemovalPolicy  = removalKeep
	defaultQualityRequest = tidal.QualityHiResLossless
	defaultQualityFloor   = tidal.QualityLossless

	defaultInterval    = 15 * time.Minute
	defaultConcurrency = 3
	defaultLogLevel    = "info"
	defaultLogFormat   = LogFormatConsole

	// LogFormatConsole selects human-readable console log output; LogFormatJSON
	// selects structured JSON output. Both are the valid values for log.format.
	LogFormatConsole = "console"
	LogFormatJSON    = "json"

	defaultMetricsAddress = ":9101"
)

// Defaults returns the baseline configuration applied before user overrides.
func Defaults() Config {
	return Config{
		Paths: Paths{
			Music:  defaultMusicPath,
			Config: defaultConfigPath,
			Data:   defaultDataPath,
		},
		PathTemplate: defaultPathTemplate,
		Scope: Scope{
			All:       false,
			Favorites: Favorites{Tracks: false, Albums: false, Playlists: false},
		},
		Quality: Quality{
			Request: defaultQualityRequest,
			Floor:   defaultQualityFloor,
		},
		Lyrics: Lyrics{
			Embed:   true,
			Sidecar: true,
		},
		Removal: Removal{Policy: defaultRemovalPolicy},
		Daemon: Daemon{
			Mode:     DaemonModePolling,
			Interval: defaultInterval,
		},
		Jitter:      Jitter{},
		Concurrency: defaultConcurrency,
		TidalAuth:   TidalAuth{ClientID: "", ClientSecret: ""},
		Log: Log{
			Level:  defaultLogLevel,
			Format: defaultLogFormat,
		},
		Metrics: Metrics{Enabled: false, Address: defaultMetricsAddress},
	}
}

var errConfigIsDir = errors.New("config path is a directory")

// Load reads the YAML file at path, overlays it onto Defaults and validates the result.
func Load(path string) (Config, error) {
	if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
		return Config{}, fmt.Errorf(
			"config path %q is a directory (Docker may have auto-created the bind-mount); "+
				"create config.yaml as a real file, e.g. cp config.example.yaml config.yaml: %w",
			path, errConfigIsDir,
		)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config %q: %w", path, err)
	}

	cfg := Defaults()
	if err = yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %q: %w", path, err)
	}
	cfg.applyDerivedDefaults()

	if err = cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate config %q: %w", path, err)
	}

	return cfg, nil
}

func (c *Config) applyDerivedDefaults() {
	if c.Daemon.Mode == "" {
		c.Daemon.Mode = DaemonModePolling
	}
	if c.Daemon.Polling == (DurationRange{}) {
		c.Daemon.Polling = DurationRange{Min: c.Daemon.Interval, Max: c.Daemon.Interval}
	}
}
