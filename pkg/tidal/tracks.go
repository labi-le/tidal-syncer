package tidal

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// playbackInfoSuffix is appended to a track path to request its post-paywall
// playback manifest.
const playbackInfoSuffix = "/playbackinfopostpaywall"

// Playback-info query parameters and their fixed values for a full-quality
// streaming request. The country code is added by [Client.Do].
const (
	audioQualityParam      = "audioquality"
	playbackModeParam      = "playbackmode"
	assetPresentationParam = "assetpresentation"
	playbackModeStream     = "STREAM"
	assetPresentationFull  = "FULL"
)

// unavailableStatuses are the HTTP statuses that mean a track is present in the
// library but cannot be downloaded: removed or taken down (404), or blocked for
// the account's region or auth scope (401). They are mapped to
// [ErrTrackUnavailable] so callers skip rather than abort.
var unavailableStatuses = map[int]struct{}{
	http.StatusNotFound:     {},
	http.StatusUnauthorized: {},
}

// Track fetches a single track's metadata by id. A track that has been taken
// down or is unavailable in the account's region is reported as
// [ErrTrackUnavailable], with the underlying [*APIError] still recoverable via
// errors.As, so the caller can skip it.
func (c *Client) Track(ctx context.Context, id string) (Track, error) {
	var track Track
	if err := c.getJSON(ctx, tracksPath+id, nil, &track); err != nil {
		return Track{}, asUnavailable(err)
	}
	return track, nil
}

// PlaybackInfo fetches the playback manifest descriptor for a track at the
// requested audio quality. It requests the STREAM playback mode and FULL asset
// presentation. An unavailable track is reported as [ErrTrackUnavailable].
func (c *Client) PlaybackInfo(ctx context.Context, id string, quality Quality) (PlaybackInfo, error) {
	query := url.Values{
		audioQualityParam:      {string(quality)},
		playbackModeParam:      {playbackModeStream},
		assetPresentationParam: {assetPresentationFull},
	}
	var info PlaybackInfo
	if err := c.getJSON(ctx, tracksPath+id+playbackInfoSuffix, query, &info); err != nil {
		return PlaybackInfo{}, asUnavailable(err)
	}
	return info, nil
}

// asUnavailable converts an [*APIError] carrying an unavailable-track status into
// an error that satisfies errors.Is(err, ErrTrackUnavailable) while preserving
// the original APIError in the chain. Any other error is returned unchanged.
func asUnavailable(err error) error {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	if _, ok := unavailableStatuses[apiErr.Status]; !ok {
		return err
	}
	return fmt.Errorf("%w: %w", ErrTrackUnavailable, apiErr)
}
