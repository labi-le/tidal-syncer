package download

import (
	"errors"
	"fmt"

	"github.com/labi-le/tidal-syncer/pkg/tidal/manifest"
)

// ErrEncryptedSkip indicates the track's stream is DRM-encrypted and was
// skipped. tidal-syncer does not implement decryption, so a caller should
// record the track as skipped and continue. It wraps [manifest.ErrEncrypted],
// so errors.Is reports true for both sentinels.
var ErrEncryptedSkip = fmt.Errorf("encrypted stream skipped: %w", manifest.ErrEncrypted)

// ErrBelowFloor indicates TIDAL granted an audio tier below the LOSSLESS floor
// (for example HIGH/AAC) for every quality tidal-syncer requested, so nothing
// was downloaded: writing a lossy stream into a .flac file would corrupt the
// library. A caller should mark the track failed and continue; the account most
// likely lacks a HiFi (lossless) subscription.
var ErrBelowFloor = errors.New("granted quality below lossless floor")

// ErrDiskFull indicates the destination volume ran out of space mid-download.
// The partial file has been removed; a caller should mark the track failed and
// may continue with other tracks.
var ErrDiskFull = errors.New("no space left on device")

// ErrUnsupportedManifest indicates the playback manifest is of a kind this
// package does not know how to download (neither BTS nor MPEG-DASH).
var ErrUnsupportedManifest = errors.New("unsupported manifest format")

// ErrUnexpectedStatus indicates a stream URL responded with a non-200 HTTP
// status, so the download was abandoned before any bytes were committed.
var ErrUnexpectedStatus = errors.New("unexpected http status")

// ErrFFmpeg indicates the MPEG-DASH demux failed: ffmpeg was not configured,
// could not be executed, or exited non-zero. The partial file has been removed;
// a caller should mark the track failed and may continue with other tracks.
var ErrFFmpeg = errors.New("ffmpeg demux failed")
