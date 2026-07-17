// Package sync orchestrates a full TIDAL-to-local synchronization run: it
// enumerates the desired track set from the configured scope, downloads and
// tags each track concurrently under a rate limit, and records per-track state in
// the store. It returns the enumerated favorites as snapshot items so the caller
// can reconcile removals against the prior snapshot and then persist the new one.
// It depends only on the narrow ports declared in ports.go, so the concrete TIDAL
// client, downloader and cover fetcher are injected and independently testable.
package sync

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"

	"github.com/labi-le/tidal-syncer/internal/config"
	"github.com/labi-le/tidal-syncer/internal/namer"
	"github.com/labi-le/tidal-syncer/internal/store"
	"github.com/labi-le/tidal-syncer/internal/tag"
	"github.com/labi-le/tidal-syncer/pkg/tidal"
)

const (
	// minWorkers is the floor on concurrent download workers.
	minWorkers = 1
	// defaultLimiterBurst is the burst of the fallback unlimited rate limiter.
	defaultLimiterBurst = 1
	// SnapshotKindTracks names the favorites snapshot the engine enumerates and the
	// removal reconciler diffs; the cmd layer persists the run under this same kind.
	SnapshotKindTracks = "tracks"
	// componentField labels every log line emitted by the engine.
	componentField = "sync"
)

// Params bundles the engine's injected dependencies and configuration.
type Params struct {
	Client      TidalClient
	Downloader  Downloader
	Covers      CoverFetcher
	Store       *store.Store
	Config      config.Config
	Logger      zerolog.Logger
	Limiter     *rate.Limiter
	RetryFailed bool
	// DelayFn and Wait override the worker-jitter hooks so tests can inject
	// deterministic timing; both default to the package jitter functions.
	DelayFn func(config.DurationRange) time.Duration
	Wait    func(context.Context, time.Duration) error
}

// Engine orchestrates one or more synchronization runs over its injected ports.
type Engine struct {
	client      TidalClient
	downloader  Downloader
	covers      CoverFetcher
	store       *store.Store
	config      config.Config
	template    *namer.Template
	logger      zerolog.Logger
	limiter     *rate.Limiter
	albums      *albumCache
	retryFailed bool
	delay       func(config.DurationRange) time.Duration
	wait        func(context.Context, time.Duration) error
}

// NewEngine builds an Engine from p, defaulting the rate limiter to an unlimited
// one when none is supplied.
func NewEngine(p Params) *Engine {
	limiter := p.Limiter
	if limiter == nil {
		limiter = rate.NewLimiter(rate.Inf, defaultLimiterBurst)
	}
	delay := p.DelayFn
	if delay == nil {
		delay = randomWorkerDelay
	}
	wait := p.Wait
	if wait == nil {
		wait = waitForDelay
	}

	return &Engine{
		client:      p.Client,
		downloader:  p.Downloader,
		covers:      p.Covers,
		store:       p.Store,
		config:      p.Config,
		template:    namer.Compile(p.Config.PathTemplate),
		logger:      p.Logger.With().Str("component", componentField).Logger(),
		limiter:     limiter,
		albums:      newAlbumCache(),
		retryFailed: p.RetryFailed,
		delay:       delay,
		wait:        wait,
	}
}

// SyncOnce performs a single synchronization pass: it validates the token,
// enumerates the desired tracks, and downloads them concurrently. It returns the
// run Summary together with the enumerated favorites as snapshot items; the
// caller reconciles removals against the prior snapshot and then persists this
// set as the new snapshot. Per-track failures are recorded in the returned
// Summary rather than aborting the run; only enumeration, token or cancellation
// errors are returned.
func (e *Engine) SyncOnce(ctx context.Context) (Summary, []store.SnapshotItem, error) {
	start := time.Now()

	if _, err := e.client.UserID(ctx); err != nil {
		return Summary{}, nil, fmt.Errorf("sync: validate token: %w", err)
	}

	if err := e.backfillGenres(ctx); err != nil {
		e.logger.Warn().Err(err).Msg("genre backfill incomplete")
	}

	tracks, dates, err := e.enumerate(ctx)
	if err != nil {
		return Summary{}, nil, err
	}

	c := &counters{}
	if err = e.downloadAll(ctx, tracks, c); err != nil {
		return Summary{}, nil, err
	}

	summary := c.snapshot(time.Since(start))
	summary.emit(e.logger)

	return summary, snapshotItems(tracks, dates), nil
}

// backfillGenres fills the genre column for already-downloaded tracks recorded
// before the column existed, reading each file's GENRE comments. It is
// best-effort and self-limiting: a track whose file is missing or carries no
// genre is left for a later run, and the underlying query returns nothing once
// every done track is populated, so calling it on every cycle is cheap.
func (e *Engine) backfillGenres(ctx context.Context) error {
	gaps, err := e.store.TracksMissingGenre(ctx)
	if err != nil {
		return fmt.Errorf("sync: list tracks missing genre: %w", err)
	}
	for _, gap := range gaps {
		genres, readErr := tag.ReadGenre(gap.Path)
		if readErr != nil {
			e.logger.Debug().Err(readErr).Str("path", gap.Path).Msg("genre backfill: read tags")

			continue
		}
		joined := strings.Join(genres, genreSeparator)
		if joined == "" {
			continue
		}
		if err = e.store.SetTrackGenre(ctx, gap.TidalID, joined); err != nil {
			return fmt.Errorf("sync: backfill genre for track %s: %w", gap.TidalID, err)
		}
	}

	return nil
}

// downloadAll processes tracks concurrently, bounded by the configured worker
// count, waiting on the rate limiter before each track.
func (e *Engine) downloadAll(ctx context.Context, tracks []tidal.Track, c *counters) error {
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(max(minWorkers, e.config.Concurrency))

	for _, track := range tracks {
		group.Go(func() error {
			if err := e.limiter.Wait(groupCtx); err != nil {
				return fmt.Errorf("sync: rate limit: %w", err)
			}
			if err := e.wait(groupCtx, e.delay(e.config.Jitter.Worker)); err != nil {
				return fmt.Errorf("sync: worker jitter: %w", err)
			}
			e.processTrack(groupCtx, track, c)

			return nil
		})
	}

	if err := group.Wait(); err != nil {
		return fmt.Errorf("sync: download workers: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("sync: cancelled: %w", err)
	}

	return nil
}

// snapshotItems projects the enumerated tracks into favorites-snapshot items for
// the caller to diff for removals and persist as the next run's baseline. The
// favorite-add date is carried through for tracks that came from the favorites
// listing; tracks reached only via albums or playlists carry an empty date.
func snapshotItems(tracks []tidal.Track, dates map[int]string) []store.SnapshotItem {
	items := make([]store.SnapshotItem, 0, len(tracks))
	for _, track := range tracks {
		items = append(items, store.SnapshotItem{
			TidalID: strconv.Itoa(track.ID),
			Name:    track.Title,
			AddedAt: dates[track.ID],
		})
	}

	return items
}
