package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/labi-le/tidal-syncer/internal/config"
	"github.com/labi-le/tidal-syncer/pkg/tidal/auth"
)

// newDaemonCmd builds the `daemon` subcommand that runs the full sync pipeline
// on a poll loop until it receives SIGTERM/SIGINT, then shuts down gracefully.
// Each sync cycle opens its own short-lived store.
func newDaemonCmd(configPath *string, verbose *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "daemon",
		Short: "Run tidal-syncer as a background daemon",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(*configPath)
			if err != nil {
				return fmt.Errorf("daemon: %w", err)
			}

			logger, err := buildLogger(os.Stderr, cfg.Log.Format, cfg.Log.Level, *verbose)
			if err != nil {
				return fmt.Errorf("daemon: %w", err)
			}

			cycle := func(ctx context.Context) error {
				return runSync(ctx, *configPath, *verbose, os.Stderr, false)
			}

			return runDaemon(cmd.Context(), &logger, cfg.Daemon, cycle)
		},
	}
}

// errUnknownDaemonMode is returned by runDaemon when config.Daemon.Mode is not a
// known mode. Config validation rejects unknown modes up front, so this is a
// defensive guard that keeps the mode switch exhaustive instead of silently
// no-opping the daemon.
var errUnknownDaemonMode = errors.New("unknown daemon mode")

// runDaemon runs cycle once immediately, then once per interval tick, until ctx
// is cancelled by SIGTERM/SIGINT, at which point it returns nil for a graceful
// shutdown. The signal context is derived from ctx, so an in-flight cycle is
// cancelled within the shutdown window. Per-cycle errors are logged and never
// stop the loop; the daemon is a long-lived process that keeps polling.
func runDaemon(
	ctx context.Context,
	lg *zerolog.Logger,
	daemon config.Daemon,
	cycle func(context.Context) error,
) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	lg.Info().Str("mode", daemon.Mode).Msg("daemon started")

	var err error

	switch daemon.Mode {
	case config.DaemonModePolling:
		err = runPollingDaemon(ctx, lg, daemon.Polling, cycle)
	case config.DaemonModeTimeWindow:
		err = runTimeWindowDaemon(ctx, lg, daemon.TimeWindow, cycle)
	default:
		return fmt.Errorf("%w: %q", errUnknownDaemonMode, daemon.Mode)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}

	lg.Info().Msg("shutdown signal received; daemon stopping")

	return nil
}

// runDaemonCycle runs one pipeline cycle and classifies any error by recovery
// class. A contended lock is a benign skip; a revoked refresh token logs a
// single actionable instruction to re-run `tidal-syncer login` (the daemon
// never re-authenticates by itself); dead client credentials get a distinct
// operator alert; anything else is transient and retried next tick. Only
// cancellation returns a non-nil error, so the loop can exit on shutdown.
func runDaemonCycle(
	ctx context.Context,
	lg *zerolog.Logger,
	cycle func(context.Context) error,
) error {
	err := cycle(ctx)
	if err == nil {
		return nil
	}

	switch {
	case errors.Is(err, context.Canceled):
		lg.Debug().Msg("sync cycle cancelled by shutdown")

		return err
	case errors.Is(err, errAnotherSyncRunning):
		lg.Debug().Msg("another sync is already running; skipping this cycle")
	case errors.Is(err, auth.ErrReauthRequired):
		lg.Error().Msg("re-authentication required: run 'tidal-syncer login' to re-authorize")
	case errors.Is(err, auth.ErrDeadCredentials):
		lg.Error().Msg("dead TIDAL client credentials: set tidal_auth.client_id and tidal_auth.client_secret in config.yaml; not attempting re-auth")
	default:
		lg.Error().Err(err).Msg("sync cycle failed; will retry on next tick")
	}

	return nil
}
