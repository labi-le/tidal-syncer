package sync

import (
	"testing"

	"github.com/labi-le/tidal-syncer/internal/config"
	"github.com/labi-le/tidal-syncer/internal/store"
	"github.com/labi-le/tidal-syncer/pkg/tidal"
)

func Test_Engine_skipPermanentFailure(t *testing.T) {
	cases := []struct {
		name        string
		record      store.Track
		request     tidal.Quality
		retryFailed bool
		want        bool
	}{
		{
			name:    "permanent failure at same requested tier is skipped",
			record:  store.Track{Status: store.StatusFailed, Permanent: true, RequestedQuality: string(tidal.QualityLossless)},
			request: tidal.QualityLossless,
			want:    true,
		},
		{
			name:    "transient failure is retried",
			record:  store.Track{Status: store.StatusFailed, Permanent: false, RequestedQuality: string(tidal.QualityLossless)},
			request: tidal.QualityLossless,
			want:    false,
		},
		{
			name:    "permanent failure is retried when the requested tier is raised",
			record:  store.Track{Status: store.StatusFailed, Permanent: true, RequestedQuality: string(tidal.QualityLossless)},
			request: tidal.QualityHiResLossless,
			want:    false,
		},
		{
			name:        "permanent failure is retried under retry-failed",
			record:      store.Track{Status: store.StatusFailed, Permanent: true, RequestedQuality: string(tidal.QualityLossless)},
			request:     tidal.QualityLossless,
			retryFailed: true,
			want:        false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := &Engine{
				config:      config.Config{Quality: config.Quality{Request: tc.request}},
				retryFailed: tc.retryFailed,
			}
			if got := e.skipPermanentFailure(tc.record); got != tc.want {
				t.Errorf("skipPermanentFailure() = %v, want %v", got, tc.want)
			}
		})
	}
}
