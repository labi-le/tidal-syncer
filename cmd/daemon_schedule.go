package main

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/labi-le/tidal-syncer/internal/config"
	"github.com/labi-le/tidal-syncer/internal/metrics"
)

var daemonNowFn = time.Now

func runPollingDaemon(
	ctx context.Context,
	lg *zerolog.Logger,
	polling config.DurationRange,
	cycle func(context.Context) error,
	rec *metrics.Metrics,
) error {
	for ctx.Err() == nil {
		if err := runDaemonCycle(ctx, lg, cycle, rec); err != nil {
			return err
		}
		if err := daemonWaitFn(ctx, daemonDelayFn(polling)); err != nil {
			return err
		}
	}

	return ctx.Err()
}

func runTimeWindowDaemon(
	ctx context.Context,
	lg *zerolog.Logger,
	window config.DaemonTimeWindow,
	cycle func(context.Context) error,
	rec *metrics.Metrics,
) error {
	for ctx.Err() == nil {
		now := daemonNowFn()
		inside, err := windowContainsNow(now, window)
		if err != nil {
			return err
		}
		if !inside {
			next, err := nextTimeWindowStart(now, window)
			if err != nil {
				return err
			}
			if err = daemonWaitFn(ctx, next.Sub(now)); err != nil {
				return err
			}

			continue
		}
		if err = runDaemonCycle(ctx, lg, cycle, rec); err != nil {
			return err
		}
		if err = daemonWaitFn(ctx, daemonDelayFn(window.DelayRange())); err != nil {
			return err
		}
	}

	return ctx.Err()
}

func windowContainsNow(now time.Time, window config.DaemonTimeWindow) (bool, error) {
	startMinute, endMinute, err := parsedWindowMinutes(window)
	if err != nil {
		return false, err
	}
	currentMinute := now.Hour()*60 + now.Minute()
	if startMinute < endMinute {
		return currentMinute >= startMinute && currentMinute < endMinute, nil
	}

	return currentMinute >= startMinute || currentMinute < endMinute, nil
}

func nextTimeWindowStart(now time.Time, window config.DaemonTimeWindow) (time.Time, error) {
	inside, err := windowContainsNow(now, window)
	if err != nil {
		return time.Time{}, err
	}
	if inside {
		return now, nil
	}
	startMinute, _, err := parsedWindowMinutes(window)
	if err != nil {
		return time.Time{}, err
	}
	candidate := time.Date(
		now.Year(),
		now.Month(),
		now.Day(),
		startMinute/60,
		startMinute%60,
		0,
		0,
		now.Location(),
	)
	if candidate.After(now) {
		return candidate, nil
	}

	return candidate.Add(24 * time.Hour), nil
}

func parsedWindowMinutes(window config.DaemonTimeWindow) (int, int, error) {
	start, err := time.Parse("15:04", window.Start)
	if err != nil {
		return 0, 0, fmt.Errorf("parse daemon window start: %w", err)
	}
	end, err := time.Parse("15:04", window.End)
	if err != nil {
		return 0, 0, fmt.Errorf("parse daemon window end: %w", err)
	}

	return start.Hour()*60 + start.Minute(), end.Hour()*60 + end.Minute(), nil
}
