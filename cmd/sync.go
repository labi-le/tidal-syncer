package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"github.com/labi-le/tidal-syncer/internal/authstore"
	"github.com/labi-le/tidal-syncer/internal/config"
	"github.com/labi-le/tidal-syncer/internal/lock"
	"github.com/labi-le/tidal-syncer/internal/store"
	synceng "github.com/labi-le/tidal-syncer/internal/sync"
	"github.com/labi-le/tidal-syncer/pkg/tidal"
	"github.com/labi-le/tidal-syncer/pkg/tidal/auth"
	"github.com/labi-le/tidal-syncer/pkg/tidal/download"
)

const (
	// onceFlag names the --once flag that selects the single-cycle run mode.
	onceFlag = "once"
	// lockFileName is the file, under the data directory, whose flock serializes
	// sync runs across processes.
	lockFileName = "lock"
)

const (
	// syncDialTimeout bounds establishing a TCP connection to TIDAL.
	syncDialTimeout = 10 * time.Second
	// syncTLSHandshakeTimeout bounds the TLS handshake.
	syncTLSHandshakeTimeout = 10 * time.Second
	// syncResponseHeaderTimeout bounds waiting for response headers; it does not
	// cap the streaming body, so large track downloads are not truncated.
	syncResponseHeaderTimeout = 30 * time.Second
	// syncIdleConnTimeout bounds how long an idle keep-alive connection lingers.
	syncIdleConnTimeout = 90 * time.Second
	// syncMaxIdleConns caps pooled idle keep-alive connections.
	syncMaxIdleConns = 16
)

// errAnotherSyncRunning is returned when the data-directory lock is already held
// by another sync process, so this invocation exits without doing any work.
var errAnotherSyncRunning = errors.New("another sync is already running")

// errOnceOnly is returned when --once is disabled; the sync command only runs a
// single cycle and defers scheduled syncing to the daemon command.
var errOnceOnly = errors.New("sync runs a single cycle; use the daemon command for scheduled syncing")

// newSyncCmd builds the `sync` subcommand that runs one full synchronization
// cycle guarded by a cross-process file lock and exits.
func newSyncCmd(configPath *string, verbose *bool, lg *zerolog.Logger) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync TIDAL library to local storage",
		RunE: func(cmd *cobra.Command, _ []string) error {
			once, err := cmd.Flags().GetBool(onceFlag)
			if err != nil {
				return fmt.Errorf("sync: read --%s flag: %w", onceFlag, err)
			}
			if !once {
				return errOnceOnly
			}

			return runSync(cmd.Context(), *configPath, *verbose, *lg)
		},
	}
	cmd.Flags().Bool(onceFlag, true, "run a single sync cycle and exit")

	return cmd
}

// runSync loads configuration, opens and migrates the store, then acquires the
// data-directory lock before running one synchronization cycle. A contended lock
// is reported as a friendly, non-fatal condition: the run exits non-zero without
// touching the network. The lock is held for the whole cycle and released on
// return.
func runSync(ctx context.Context, configPath string, verbose bool, logger zerolog.Logger) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("sync: %w", err)
	}

	logger, err = leveledLogger(logger, cfg.Log.Level, verbose)
	if err != nil {
		return fmt.Errorf("sync: %w", err)
	}

	st, err := store.Open(cfg.Paths.Data)
	if err != nil {
		return fmt.Errorf("sync: open store: %w", err)
	}
	defer func() { _ = st.Close() }()

	if err = st.Migrate(ctx); err != nil {
		return fmt.Errorf("sync: migrate store: %w", err)
	}

	release, err := (&lock.FileLock{}).TryAcquire(filepath.Join(cfg.Paths.Data, lockFileName))
	if err != nil {
		if errors.Is(err, lock.ErrLocked) {
			logger.Warn().Msg("another sync is already running; exiting")

			return errAnotherSyncRunning
		}

		return fmt.Errorf("sync: acquire lock: %w", err)
	}
	defer func() { _ = release() }()

	swept, err := download.SweepStale(cfg.Paths.Music)
	if err != nil {
		return fmt.Errorf("sync: sweep stale downloads: %w", err)
	}
	logger.Info().Int("swept", swept).Str("music", cfg.Paths.Music).Msg("swept stale .part files")

	return executeSync(ctx, cfg, st, logger)
}

// executeSync wires the TIDAL client, download engine and playlist exporter, runs
// one sync cycle, exports playlists when in scope, and logs the resulting
// summary. It runs with the data-directory lock held.
func executeSync(ctx context.Context, cfg config.Config, st *store.Store, logger zerolog.Logger) error {
	authClient := auth.New(cfg.TidalAuth.ClientID, cfg.TidalAuth.ClientSecret, authstore.New(st))
	tidalClient := tidal.New(auth.NewTokenSource(authClient))
	httpClient := newSyncHTTPClient()

	engine := synceng.NewEngine(synceng.Params{
		Client:     tidalClient,
		Downloader: synceng.NewDownloader(synceng.NewPlaybackProvider(tidalClient), httpClient),
		Covers:     synceng.NewCoverFetcher(httpClient),
		Store:      st,
		Config:     cfg,
		Logger:     logger,
		Limiter:    nil,
	})

	summary, current, err := engine.SyncOnce(ctx)
	if err != nil {
		return fmt.Errorf("sync: run cycle: %w", err)
	}

	remover := synceng.NewRemover(synceng.RemoverParams{Store: st, Config: cfg, Logger: logger})
	if err = remover.Reconcile(ctx, current); err != nil {
		return fmt.Errorf("sync: reconcile removals: %w", err)
	}

	if err = st.ReplaceSnapshot(ctx, synceng.SnapshotKindTracks, current); err != nil {
		return fmt.Errorf("sync: refresh snapshot: %w", err)
	}

	if err = exportPlaylists(ctx, cfg, tidalClient, logger); err != nil {
		return err
	}

	logger.Info().
		Int("downloaded", summary.Downloaded).
		Int("skipped", summary.Skipped).
		Int("failed", summary.Failed).
		Dur("duration", summary.Duration).
		Msg("sync finished")

	return nil
}

// exportPlaylists writes one .m3u8 per favorite playlist when the configured
// scope includes playlists; otherwise it is a no-op so a tracks-only sync does
// not perform spurious playlist API calls.
func exportPlaylists(ctx context.Context, cfg config.Config, client *tidal.Client, logger zerolog.Logger) error {
	if !cfg.Scope.All && !cfg.Scope.Favorites.Playlists {
		logger.Debug().Msg("playlist export skipped: playlists not in scope")

		return nil
	}

	if err := synceng.NewPlaylistWriter(client, cfg, logger).WritePlaylists(ctx); err != nil {
		return fmt.Errorf("sync: export playlists: %w", err)
	}

	return nil
}

// newSyncHTTPClient builds the HTTP client shared by the downloader and cover
// fetcher. It tunes connection, TLS and response-header timeouts but sets no
// overall client timeout, so streaming track downloads are bounded only by the
// caller's context.
func newSyncHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: syncDialTimeout}).DialContext,
			ForceAttemptHTTP2:     true,
			TLSHandshakeTimeout:   syncTLSHandshakeTimeout,
			ResponseHeaderTimeout: syncResponseHeaderTimeout,
			MaxIdleConns:          syncMaxIdleConns,
			IdleConnTimeout:       syncIdleConnTimeout,
		},
	}
}
