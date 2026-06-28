// Package download fetches Tidal tracks to local files. It resolves a track's
// playback manifest through a [PlaybackProvider], downloads the direct (BTS)
// representation over HTTP, and commits each file atomically: bytes are
// streamed into a sibling "<dest>.part" file on the destination's own volume,
// flushed to disk, and renamed into place only after a complete, successful
// download.
//
// The package carries no internal dependency: the destination path and the
// ffmpeg binary path are supplied by the caller. An MPEG-DASH manifest is
// demuxed to FLAC by the injected ffmpeg (see [WithFFmpeg] and dash.go); an
// unrecognized manifest kind is reported with [ErrUnsupportedManifest]. The
// package never logs; every failure is returned as a typed error.
package download

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"syscall"

	"github.com/labi-le/tidal-syncer/pkg/tidal"
	"github.com/labi-le/tidal-syncer/pkg/tidal/manifest"
)

// PartSuffix names the temporary file that receives stream bytes before the
// atomic rename into the final destination path. It is the single source of the
// part-file suffix for every writer that stages through "<dest>.part".
const PartSuffix = ".part"

// tempFileMode is the permission mode of the temporary ".part" file, which is
// renamed into the final FLAC. 0o644 keeps the music library world-readable for
// the host user and media servers regardless of the container UID.
const tempFileMode = 0o644

// Playback is the resolved playback manifest descriptor for a track: the
// manifest's MIME type, its base64-encoded body, and the audio quality TIDAL
// actually granted. Named fields avoid the transposition hazard of two adjacent
// manifest strings returned positionally.
type Playback struct {
	// MimeType selects how ManifestB64 must be parsed, for example
	// "application/dash+xml" or "application/vnd.tidal.bts".
	MimeType string
	// ManifestB64 is the base64-encoded playback manifest body.
	ManifestB64 string
	// GrantedQuality is the audio tier TIDAL actually granted, which may be lower
	// than requested; callers must enforce their own quality floor on it.
	GrantedQuality tidal.Quality
}

// PlaybackProvider resolves the playback manifest for a track at a requested
// quality. The concrete *tidal.Client satisfies this interface; tests supply a
// fake. Implementations must be safe for concurrent use by multiple goroutines.
type PlaybackProvider interface {
	// PlaybackInfo returns the [Playback] descriptor for trackID at the requested
	// quality, or an error when that quality is unavailable. The granted quality
	// may be lower than requested: TIDAL answers with HTTP 200 and a lower tier
	// (for example HIGH/AAC) when the account or track does not support the
	// requested lossless tier, so callers must enforce their own quality floor on
	// Playback.GrantedQuality rather than trust the request.
	PlaybackInfo(ctx context.Context, trackID string, quality tidal.Quality) (Playback, error)
}

// Downloader downloads Tidal tracks to local files. It is safe for concurrent
// use by multiple goroutines provided its [PlaybackProvider] and http.Client
// are. Construct one with [New].
type Downloader struct {
	provider   PlaybackProvider
	httpClient *http.Client
	ffmpegPath string
}

// Option customizes a Downloader at construction time.
type Option func(*Downloader)

// New constructs a Downloader that resolves manifests through provider and
// fetches stream bytes through httpClient. When httpClient is nil,
// [http.DefaultClient] is used. Pass [WithFFmpeg] to enable MPEG-DASH downloads.
func New(provider PlaybackProvider, httpClient *http.Client, opts ...Option) *Downloader {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	downloader := &Downloader{provider: provider, httpClient: httpClient, ffmpegPath: ""}
	for _, opt := range opts {
		opt(downloader)
	}

	return downloader
}

// WithFFmpeg sets the absolute path to the ffmpeg binary used to demux MPEG-DASH
// streams to FLAC. Without it, a DASH manifest download fails with [ErrFFmpeg].
func WithFFmpeg(path string) Option {
	return func(d *Downloader) { d.ffmpegPath = path }
}

// Download fetches trackID to destPath and reports the audio quality actually
// obtained. It requests HI_RES_LOSSLESS and falls back to the LOSSLESS floor
// only when the higher tier is unavailable; it never downloads lossy audio.
//
// The track is written atomically through a sibling "<destPath>.part" file on
// destPath's own volume: stream bytes are flushed with fsync and the temporary
// file is renamed into place only after a complete download. On cancellation or
// any error the partial file is removed and destPath is left untouched.
//
// Download returns [ErrEncryptedSkip] for a DRM-protected stream,
// [ErrUnsupportedManifest] for an unrecognized manifest kind, [ErrFFmpeg] when a
// DASH demux fails, [ErrUnexpectedStatus] for a non-200 stream response,
// [ErrBelowFloor] when TIDAL grants only a sub-lossless tier (so nothing is
// written), and [ErrDiskFull] when the destination volume runs out of space.
func (d *Downloader) Download(ctx context.Context, trackID, destPath string) (tidal.Quality, error) {
	playback, err := d.fetchManifest(ctx, trackID)
	if err != nil {
		return "", err
	}

	parsed, err := manifest.Parse(playback.MimeType, playback.ManifestB64)
	if err != nil {
		if errors.Is(err, manifest.ErrEncrypted) {
			return "", fmt.Errorf("download: track %q: %w", trackID, ErrEncryptedSkip)
		}

		return "", fmt.Errorf("download: parse manifest for track %q: %w", trackID, err)
	}

	produce, err := d.producerFor(ctx, parsed, trackID)
	if err != nil {
		return "", err
	}

	if err = d.writeAtomic(destPath, produce); err != nil {
		return "", err
	}

	return playback.GrantedQuality, nil
}

// producerFor returns the part-file producer for a parsed manifest: BTS streams
// direct URLs, DASH fetches segments and demuxes them to FLAC through ffmpeg.
func (d *Downloader) producerFor(
	ctx context.Context, parsed manifest.Manifest, trackID string,
) (func(*os.File) error, error) {
	switch parsed.Kind() {
	case manifest.KindBTS:
		bts, _ := parsed.BTS()
		urls := bts.URLs()

		return func(part *os.File) error { return d.streamURLs(ctx, urls, part) }, nil
	case manifest.KindDASH:
		dash, _ := parsed.DASH()
		urls := dash.SegmentURLs()

		return func(part *os.File) error { return d.demuxDASH(ctx, urls, part) }, nil
	default:
		return nil, fmt.Errorf("download: track %q kind %q: %w", trackID, parsed.Kind(), ErrUnsupportedManifest)
	}
}

// fetchManifest resolves the playback manifest for trackID, trying the quality
// ladder highest-first and returning the first tier TIDAL grants at or above the
// lossless floor. It descends the ladder when a tier is unavailable, rejects a
// granted tier below the floor with [ErrBelowFloor] (TIDAL answers HTTP 200 with
// a lossy stream when an account lacks lossless), and aborts if ctx is cancelled.
func (d *Downloader) fetchManifest(ctx context.Context, trackID string) (Playback, error) {
	var lastErr error
	var subFloor tidal.Quality
	for _, quality := range [...]tidal.Quality{tidal.QualityHiResLossless, tidal.QualityLossless} {
		playback, err := d.provider.PlaybackInfo(ctx, trackID, quality)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return Playback{}, fmt.Errorf("download: playback info for track %q: %w", trackID, ctxErr)
			}
			lastErr = err

			continue
		}
		if playback.GrantedQuality.MeetsLosslessFloor() {
			return playback, nil
		}
		subFloor = playback.GrantedQuality
	}

	if subFloor != "" {
		return Playback{}, fmt.Errorf(
			"download: track %q: granted quality %q is below floor %q; account may not support lossless: %w",
			trackID, subFloor, tidal.QualityLossless, ErrBelowFloor)
	}

	return Playback{}, fmt.Errorf("download: no available quality for track %q: %w", trackID, lastErr)
}

// writeAtomic opens a temporary "<destPath>.part" file on destPath's own
// volume, lets produce fill it, fsyncs it, and renames it into place. The
// temporary file is removed on any error, so destPath is only ever created from
// a complete download.
func (d *Downloader) writeAtomic(destPath string, produce func(part *os.File) error) error {
	partPath := destPath + PartSuffix

	part, err := os.OpenFile(partPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, tempFileMode)
	if err != nil {
		return fmt.Errorf("download: create part file %q: %w", partPath, err)
	}

	committed := false
	defer func() {
		_ = part.Close()
		if !committed {
			_ = os.Remove(partPath)
		}
	}()

	if err = produce(part); err != nil {
		return err
	}

	if err = part.Sync(); err != nil {
		return classifyIOErr("sync part file "+partPath, err)
	}

	if err = os.Rename(partPath, destPath); err != nil {
		return fmt.Errorf("download: rename part file %q to %q: %w", partPath, destPath, err)
	}

	committed = true

	return nil
}

// streamURLs streams each URL in order into dst, stopping at the first error.
func (d *Downloader) streamURLs(ctx context.Context, urls []string, dst io.Writer) error {
	for _, rawURL := range urls {
		if err := d.streamURL(ctx, rawURL, dst); err != nil {
			return err
		}
	}

	return nil
}

// streamURL performs a single GET and copies the response body into dst. It
// reports [ErrUnexpectedStatus] for a non-200 response, the context error on
// cancellation, and [ErrDiskFull] when the destination volume is full.
func (d *Downloader) streamURL(ctx context.Context, rawURL string, dst io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("download: new request %q: %w", rawURL, err)
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("download: get %q: %w", rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: get %q status %d: %w", rawURL, resp.StatusCode, ErrUnexpectedStatus)
	}

	if _, err = io.Copy(dst, resp.Body); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("download: stream %q: %w", rawURL, ctxErr)
		}

		return classifyIOErr("stream "+rawURL, err)
	}

	return nil
}

// classifyIOErr maps a streaming-copy or fsync error to a typed download error.
// A no-space-left condition becomes [ErrDiskFull] (joined with the wrapped
// cause) so a caller can mark the track failed and continue; any other error is
// wrapped verbatim.
func classifyIOErr(op string, err error) error {
	wrapped := fmt.Errorf("download: %s: %w", op, err)
	if errors.Is(err, syscall.ENOSPC) {
		return errors.Join(ErrDiskFull, wrapped)
	}

	return wrapped
}
