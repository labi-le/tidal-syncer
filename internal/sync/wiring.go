package sync

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/labi-le/tidal-syncer/internal/config"
	"github.com/labi-le/tidal-syncer/pkg/tidal"
	"github.com/labi-le/tidal-syncer/pkg/tidal/download"
)

const (
	// coverSize is the square edge length, in pixels, of fetched cover art.
	coverSize = 1280
	// maxCoverBytes caps a cover download to guard against oversized responses.
	maxCoverBytes int64 = 8 << 20
	// ffmpegEnv overrides the ffmpeg binary path used to demux DASH streams.
	ffmpegEnv = "TIDAL_FFMPEG"
	// defaultFFmpegPath is the bundled ffmpeg location used when ffmpegEnv is unset.
	defaultFFmpegPath = "/usr/local/bin/ffmpeg"
)

// ErrCoverStatus reports a non-200 response while fetching cover art.
var ErrCoverStatus = errors.New("sync: unexpected cover status")

// HTTPCoverFetcher fetches album cover art over HTTP from TIDAL's image CDN.
type HTTPCoverFetcher struct {
	client *http.Client
}

// NewCoverFetcher builds an HTTPCoverFetcher, defaulting to http.DefaultClient
// when client is nil.
func NewCoverFetcher(client *http.Client) *HTTPCoverFetcher {
	if client == nil {
		client = http.DefaultClient
	}

	return &HTTPCoverFetcher{client: client}
}

// Cover fetches the cover image for uuid, reading at most maxCoverBytes.
func (f *HTTPCoverFetcher) Cover(ctx context.Context, uuid string) ([]byte, error) {
	coverURL := tidal.CoverURL(uuid, coverSize)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, coverURL, nil)
	if err != nil {
		return nil, fmt.Errorf("cover: request %q: %w", uuid, err)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cover: get %q: %w", uuid, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cover: get %q status %d: %w", uuid, resp.StatusCode, ErrCoverStatus)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxCoverBytes))
	if err != nil {
		return nil, fmt.Errorf("cover: read %q: %w", uuid, err)
	}

	return data, nil
}

// NewDownloader builds a download.Downloader bound to provider and httpClient,
// requesting the configured quality tier down to the configured floor and
// resolving the ffmpeg path from the environment with a bundled fallback.
func NewDownloader(
	provider download.PlaybackProvider, httpClient *http.Client, quality config.Quality,
) *download.Downloader {
	return download.New(
		provider,
		httpClient,
		download.WithFFmpeg(cmp.Or(os.Getenv(ffmpegEnv), defaultFFmpegPath)),
		download.WithQuality(quality.Request),
		download.WithFloor(quality.Floor),
	)
}

// PlaybackProvider adapts *tidal.Client to download.PlaybackProvider, mapping the
// client's PlaybackInfo DTO into the download.Playback descriptor the downloader
// consumes. It lives here beside NewDownloader and NewCoverFetcher, the wiring
// layer that is permitted to reference the concrete wire client; the engine
// itself depends only on ports.go.
type PlaybackProvider struct {
	client *tidal.Client
}

// NewPlaybackProvider builds the PlaybackProvider that resolves manifests through
// client.
func NewPlaybackProvider(client *tidal.Client) PlaybackProvider {
	return PlaybackProvider{client: client}
}

// PlaybackInfo resolves the playback manifest for trackID at quality, returning
// the download.Playback descriptor whose granted quality the downloader enforces
// against its lossless floor.
func (p PlaybackProvider) PlaybackInfo(
	ctx context.Context, trackID string, quality tidal.Quality,
) (download.Playback, error) {
	info, err := p.client.PlaybackInfo(ctx, trackID, quality)
	if err != nil {
		return download.Playback{}, fmt.Errorf("sync: playback info for track %q: %w", trackID, err)
	}

	return download.Playback{
		MimeType:       info.ManifestMimeType,
		ManifestB64:    info.Manifest,
		GrantedQuality: info.AudioQuality,
	}, nil
}
