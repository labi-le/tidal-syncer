package sync

import (
	"errors"
	"fmt"
	"testing"

	"github.com/labi-le/tidal-syncer/pkg/tidal"
	"github.com/labi-le/tidal-syncer/pkg/tidal/download"
)

func Test_permanentFailure_classifies_causes(t *testing.T) {
	cases := []struct {
		name  string
		cause error
		want  bool
	}{
		{"album 404 wrapped raw APIError", fmt.Errorf("fetch album 78638948: %w", &tidal.APIError{Status: 404}), true},
		{"track unavailable wrapping 401", fmt.Errorf("%w: %w", tidal.ErrTrackUnavailable, &tidal.APIError{Status: 401}), true},
		{"below lossless floor", fmt.Errorf("download track 9060229: %w", download.ErrBelowFloor), true},
		{"encrypted stream skipped", download.ErrEncryptedSkip, true},
		{"unsupported manifest", download.ErrUnsupportedManifest, true},
		{"disk full is transient", download.ErrDiskFull, false},
		{"ffmpeg is transient", download.ErrFFmpeg, false},
		{"unexpected status is transient", download.ErrUnexpectedStatus, false},
		{"server 500 is transient", &tidal.APIError{Status: 500}, false},
		{"generic error is transient", errors.New("boom"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := permanentFailure(tc.cause); got != tc.want {
				t.Errorf("permanentFailure(%v) = %v, want %v", tc.cause, got, tc.want)
			}
		})
	}
}
