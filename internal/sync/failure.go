package sync

import (
	"errors"
	"net/http"

	"github.com/labi-le/tidal-syncer/pkg/tidal"
	"github.com/labi-le/tidal-syncer/pkg/tidal/download"
)

// permanentFailure reports whether cause can never be resolved by re-running at
// the same requested quality: the track or album is delisted (HTTP 404), its
// stream is DRM-encrypted, its manifest is an unsupported kind, or TIDAL grants
// only a sub-lossless tier. Such tracks are skipped on later cycles and
// re-attempted only when quality.request is raised or --retry-failed is set.
//
// Every other cause (disk full, ffmpeg failure, expired stream URL, transient
// 5xx) is transient and retried on the next run.
func permanentFailure(cause error) bool {
	switch {
	case errors.Is(cause, download.ErrBelowFloor),
		errors.Is(cause, download.ErrEncryptedSkip),
		errors.Is(cause, download.ErrUnsupportedManifest),
		errors.Is(cause, tidal.ErrTrackUnavailable):
		return true
	}

	var apiErr *tidal.APIError
	if errors.As(cause, &apiErr) {
		return apiErr.Status == http.StatusNotFound
	}

	return false
}
