// Package main is the entry point for tidal-syncer.
package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/labi-le/tidal-syncer/internal/buildinfo"
)

// flags carries persistent root flag values bound by cobra.
type flags struct {
	configPath string
	verbose    bool
}

// errInvalidLogLevel is returned when config log.level is not a recognized zerolog level.
var errInvalidLogLevel = errors.New("invalid log level")

// initLogger builds the bootstrap zerolog.Logger used before any config file is
// loaded (e.g. by the version command, which never reads config). Stderr
// ConsoleWriter; InfoLevel default (TraceLevel if verbose); always Timestamp;
// .Caller() only when verbose. No global logger is touched. Config-loading
// subcommands re-derive the level from config.log.level via leveledLogger.
func initLogger(verbose bool) zerolog.Logger {
	lvl := zerolog.InfoLevel
	if verbose {
		lvl = zerolog.TraceLevel
	}

	cw := zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339} //nolint:exhaustruct // ConsoleWriter has many optional knobs

	ctx := zerolog.New(cw).Level(lvl).With().Timestamp()
	if verbose {
		ctx = ctx.Caller()
	}

	return ctx.Logger()
}

// leveledLogger returns base re-leveled from the config log.level value. --verbose
// wins: it forces TraceLevel regardless of config. An unrecognized configLevel is
// rejected with errInvalidLogLevel. base keeps its Timestamp/Caller context; only
// the minimum level changes (zerolog loggers are immutable values).
func leveledLogger(base zerolog.Logger, configLevel string, verbose bool) (zerolog.Logger, error) {
	lvl, err := parseLogLevel(configLevel, verbose)
	if err != nil {
		return zerolog.Nop(), err
	}

	return base.Level(lvl), nil
}

// parseLogLevel resolves the effective zerolog level from the config log.level
// value and --verbose. --verbose overrides config to Trace. Empty falls back to Info.
func parseLogLevel(level string, verbose bool) (zerolog.Level, error) {
	if verbose {
		return zerolog.TraceLevel, nil
	}

	if level == "" {
		return zerolog.InfoLevel, nil
	}

	lvl, err := zerolog.ParseLevel(level)
	if err != nil {
		return zerolog.NoLevel, fmt.Errorf("%w %q: %w", errInvalidLogLevel, level, err)
	}

	return lvl, nil
}

// newRootCmd assembles the cobra command tree. The logger is created once in
// PersistentPreRunE and injected by value into every subcommand RunE closure
// captured at registration time via the returned *zerolog.Logger pointer.
func newRootCmd() *cobra.Command {
	f := &flags{} //nolint:exhaustruct // zero values are the intended defaults

	// loggerHolder is set by PersistentPreRunE and read by subcommands.
	// Pointer indirection keeps zero global state while letting child RunE
	// closures resolve the logger at call time (after flag parsing).
	var loggerHolder zerolog.Logger

	root := &cobra.Command{
		Use:   "tidal-syncer",
		Short: "Sync TIDAL library to local storage",
		Long:  "tidal-syncer downloads and keeps your TIDAL library in sync with local storage.",
		PersistentPreRun: func(_ *cobra.Command, _ []string) {
			loggerHolder = initLogger(f.verbose)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().StringVar(&f.configPath, "config", "/app/config.yaml", "path to config file")
	root.PersistentFlags().BoolVar(&f.verbose, "verbose", false, "verbose mode (Trace level + caller)")

	root.AddCommand(newVersionCmd(&loggerHolder))
	root.AddCommand(newLoginCmd(&f.configPath, &f.verbose, &loggerHolder))
	root.AddCommand(newSyncCmd(&f.configPath, &f.verbose, &loggerHolder))
	root.AddCommand(newDaemonCmd(&f.configPath, &f.verbose, &loggerHolder))
	root.AddCommand(newHealthCmd(&f.configPath, &f.verbose, &loggerHolder))
	root.AddCommand(newSelfcheckCmd(&f.configPath, &f.verbose, &loggerHolder))

	return root
}

// newVersionCmd builds the `version` subcommand that prints the ldflag-injected
// build metadata via the injected zerolog logger.
func newVersionCmd(lg *zerolog.Logger) *cobra.Command {
	return &cobra.Command{ //nolint:exhaustruct // cobra.Command is exhaustruct-excluded by .golangci.yml
		Use:   "version",
		Short: "Print build version information",
		RunE: func(_ *cobra.Command, _ []string) error {
			lg.Info().
				Str("version", buildinfo.Version).
				Str("commit", buildinfo.CommitHash).
				Str("built", buildinfo.BuildTime).
				Msg("version")

			return nil
		},
	}
}

func main() {
	root := newRootCmd()

	if err := root.Execute(); err != nil {
		// Build a stderr console logger to report the fatal startup error
		// without touching any global logger.
		cw := zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339} //nolint:exhaustruct // ConsoleWriter has many optional knobs
		lg := zerolog.New(cw).With().Timestamp().Logger()
		lg.Error().Err(err).Msg("fatal")
		os.Exit(1)
	}
}
