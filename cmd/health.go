package main

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/labi-le/tidal-syncer/internal/config"
	"github.com/labi-le/tidal-syncer/internal/store"
)

// defaultFFmpegPath is the absolute path where the Dockerfile (task 21) copies
// the bundled static ffmpeg binary (`ENV TIDAL_FFMPEG=/usr/local/bin/ffmpeg`).
// Outside the image the operator overrides it via the TIDAL_FFMPEG env var.
const defaultFFmpegPath = "/usr/local/bin/ffmpeg"

// ffmpegEnvVar names the environment variable that overrides the bundled
// ffmpeg path resolved by selfcheck.
const ffmpegEnvVar = "TIDAL_FFMPEG"

// newHealthCmd builds the `health` subcommand used as the distroless container
// HEALTHCHECK. It validates config and proves the store is reachable. Exit 0
// on success; any failure surfaces a non-zero exit via cobra.
func newHealthCmd(configPath *string, verbose *bool, lg *zerolog.Logger) *cobra.Command {
	return &cobra.Command{
		Use:   "health",
		Short: "Check tidal-syncer health (config + store reachability)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runHealth(cmd.Context(), *configPath, *verbose, *lg)
		},
	}
}

// newSelfcheckCmd builds the `selfcheck` subcommand. It validates the config,
// pings the store, and surfaces the bundled ffmpeg version through zerolog, so a
// single command confirms config, database and ffmpeg are all healthy.
func newSelfcheckCmd(configPath *string, verbose *bool, lg *zerolog.Logger) *cobra.Command {
	return &cobra.Command{
		Use:   "selfcheck",
		Short: "Check config, store reachability and the bundled ffmpeg version",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSelfcheck(cmd.Context(), *configPath, resolveFFmpegPath(), *verbose, *lg)
		},
	}
}

// runHealth loads the configuration file then opens and migrates the SQLite
// cache store. Migrate is idempotent and double-functions as a cheap
// reachability ping. The returned error (if any) is fatal to the process via
// main()'s top-level error handler.
func runHealth(ctx context.Context, configPath string, verbose bool, lg zerolog.Logger) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("health: %w", err)
	}

	lg, err = leveledLogger(lg, cfg.Log.Level, verbose)
	if err != nil {
		return fmt.Errorf("health: %w", err)
	}

	st, err := store.Open(cfg.Paths.Data)
	if err != nil {
		return fmt.Errorf("health: open store: %w", err)
	}
	defer func() { _ = st.Close() }()

	if err = st.Migrate(ctx); err != nil {
		return fmt.Errorf("health: migrate store: %w", err)
	}

	lg.Info().
		Str("config", configPath).
		Str("data", cfg.Paths.Data).
		Msg("healthy")

	return nil
}

// resolveFFmpegPath returns the ffmpeg binary path, preferring the TIDAL_FFMPEG
// env var and falling back to the bundled-image default. The distroless image
// (task 21) sets TIDAL_FFMPEG=/usr/local/bin/ffmpeg so both paths agree there.
func resolveFFmpegPath() string {
	return cmp.Or(os.Getenv(ffmpegEnvVar), defaultFFmpegPath)
}

// runSelfcheck reports overall service health: it loads and validates the
// config (config.Load validates), opens and migrates the store as a database
// reachability ping, then surfaces the bundled ffmpeg version. Every stage logs
// via zerolog; the first failure is wrapped and returned to the caller.
func runSelfcheck(ctx context.Context, configPath, ffmpegPath string, verbose bool, lg zerolog.Logger) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("selfcheck: %w", err)
	}

	lg, err = leveledLogger(lg, cfg.Log.Level, verbose)
	if err != nil {
		return fmt.Errorf("selfcheck: %w", err)
	}

	st, err := store.Open(cfg.Paths.Data)
	if err != nil {
		return fmt.Errorf("selfcheck: open store: %w", err)
	}
	defer func() { _ = st.Close() }()

	if err = st.Migrate(ctx); err != nil {
		return fmt.Errorf("selfcheck: migrate store: %w", err)
	}

	lg.Info().
		Str("config", configPath).
		Str("data", cfg.Paths.Data).
		Msg("config and store ok")

	return checkFFmpeg(ctx, ffmpegPath, lg)
}

// checkFFmpeg exec's `<ffmpegPath> -version` and logs the first stdout line
// (the banner) via zerolog. Any exec or read failure is returned to the caller.
func checkFFmpeg(ctx context.Context, ffmpegPath string, lg zerolog.Logger) error {
	cmd := exec.CommandContext(ctx, ffmpegPath, "-version")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("selfcheck: exec %q: %w (stderr: %s)",
			ffmpegPath, err, strings.TrimSpace(stderr.String()))
	}

	banner := firstLine(stdout.Bytes())

	lg.Info().
		Str("ffmpeg", ffmpegPath).
		Str("version", banner).
		Msg("ffmpeg ok")

	return nil
}

// firstLine returns the first newline-delimited line of b with trailing
// whitespace trimmed. An empty input yields the empty string.
func firstLine(b []byte) string {
	scanner := bufio.NewScanner(bytes.NewReader(b))
	if scanner.Scan() {
		return strings.TrimSpace(scanner.Text())
	}

	return ""
}
