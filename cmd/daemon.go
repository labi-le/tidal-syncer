package main

import (
	"context"
	"errors"
	"fmt"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/labi-le/tidal-syncer/internal/authstore"
	"github.com/labi-le/tidal-syncer/internal/config"
	"github.com/labi-le/tidal-syncer/internal/store"
	"github.com/labi-le/tidal-syncer/pkg/tidal/auth"
)

// newDaemonCmd builds the `daemon` subcommand that runs the full sync pipeline
// on a poll loop until it receives SIGTERM/SIGINT, then shuts down gracefully.
// The poll interval is read once from the daemon configuration; each cycle then
// reuses runSync, which loads config and acquires the data-directory lock itself.
func newDaemonCmd(configPath *string, verbose *bool, lg *zerolog.Logger) *cobra.Command {
	return &cobra.Command{
		Use:   "daemon",
		Short: "Run tidal-syncer as a background daemon",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(*configPath)
			if err != nil {
				return fmt.Errorf("daemon: %w", err)
			}

			logger, err := leveledLogger(*lg, cfg.Log.Level, *verbose)
			if err != nil {
				return fmt.Errorf("daemon: %w", err)
			}

			cycle := func(ctx context.Context) error {
				return runSync(ctx, *configPath, *verbose, logger)
			}
			reauth := func(ctx context.Context) error {
				return emitDeviceLink(ctx, *configPath, logger)
			}

			return runDaemon(cmd.Context(), &logger, cfg.Daemon.Interval, cycle, reauth)
		},
	}
}

// runDaemon runs cycle once immediately, then once per interval tick, until ctx
// is cancelled by SIGTERM/SIGINT, at which point it returns nil for a graceful
// shutdown. The signal context is derived from ctx, so an in-flight cycle is
// cancelled within the shutdown window. Per-cycle errors are logged and never
// stop the loop; the daemon is a long-lived process that keeps polling.
func runDaemon(
	ctx context.Context,
	lg *zerolog.Logger,
	interval time.Duration,
	cycle func(context.Context) error,
	reauth func(context.Context) error,
) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	lg.Info().Dur("interval", interval).Msg("daemon started")

	for ctx.Err() == nil {
		runDaemonCycle(ctx, lg, cycle, reauth)

		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}

	lg.Info().Msg("shutdown signal received; daemon stopping")

	return nil
}

// runDaemonCycle runs one pipeline cycle and classifies any error so the loop
// keeps polling. Cancellation is the expected shutdown path; dead credentials
// get a prominent re-login instruction (mirroring the login command); a
// contended lock is a benign skip; anything else is a recoverable failure.
func runDaemonCycle(
	ctx context.Context,
	lg *zerolog.Logger,
	cycle func(context.Context) error,
	reauth func(context.Context) error,
) {
	err := cycle(ctx)
	if err == nil {
		return
	}

	switch {
	case errors.Is(err, context.Canceled):
		lg.Debug().Msg("sync cycle cancelled by shutdown")
	case errors.Is(err, auth.ErrDeadCredentials):
		if reErr := reauth(ctx); reErr != nil {
			lg.Error().Err(reErr).
				Msg("dead credentials and could not start a fresh device login; will retry on next tick")
		}
	case errors.Is(err, errAnotherSyncRunning):
		lg.Warn().Msg("another sync is already running; skipping this cycle")
	default:
		lg.Error().Err(err).Msg("sync cycle failed; will retry on next tick")
	}
}

// emitDeviceLink starts a fresh TIDAL device-authorization grant and logs the
// verification link the operator must open to re-authenticate. The daemon calls
// it when a cycle fails with auth.ErrDeadCredentials so a long-running daemon
// surfaces a working login link every cycle instead of silently failing. The
// returned error (config, store or device-auth failure) is logged by the caller;
// the daemon keeps polling regardless.
func emitDeviceLink(ctx context.Context, configPath string, lg zerolog.Logger) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("daemon: reauth: %w", err)
	}

	st, err := store.Open(cfg.Paths.Data)
	if err != nil {
		return fmt.Errorf("daemon: reauth: open store: %w", err)
	}
	defer func() { _ = st.Close() }()

	if err = st.Migrate(ctx); err != nil {
		return fmt.Errorf("daemon: reauth: migrate store: %w", err)
	}

	clientID, clientSecret := resolveCredentials(cfg.TidalAuth)
	device, err := auth.New(clientID, clientSecret, authstore.New(st)).StartDeviceAuth(ctx)
	if err != nil {
		return fmt.Errorf("daemon: reauth: start device authorization: %w", err)
	}

	lg.Log().
		Str("url", device.VerificationURIComplete).
		Msg("TIDAL credentials expired — open this link to re-authenticate; the daemon keeps polling")

	return nil
}
