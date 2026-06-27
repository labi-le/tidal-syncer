package download

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
)

// segmentAttempts is how many times a single DASH segment is fetched before the
// track is failed on a persistent network error.
const segmentAttempts = 3

// demuxDASH fetches the ordered DASH segment URLs into a scratch container,
// demuxes that container to FLAC with ffmpeg, and writes the FLAC into dst (the
// open part file). The scratch file is always removed.
func (d *Downloader) demuxDASH(ctx context.Context, urls []string, dst *os.File) error {
	if d.ffmpegPath == "" {
		return fmt.Errorf("download: dash needs ffmpeg, none configured: %w", ErrFFmpeg)
	}

	scratch, err := os.CreateTemp("", "tidal-dash-*.mp4")
	if err != nil {
		return fmt.Errorf("download: create dash scratch file: %w", err)
	}
	scratchPath := scratch.Name()
	defer func() { _ = os.Remove(scratchPath) }()

	if err = d.assembleSegments(ctx, urls, scratch); err != nil {
		_ = scratch.Close()

		return err
	}
	if err = scratch.Close(); err != nil {
		return fmt.Errorf("download: close dash scratch file %q: %w", scratchPath, err)
	}

	return d.ffmpegDemux(ctx, scratchPath, dst)
}

// assembleSegments fetches each segment URL in order and appends its bytes to
// dst, so the concatenation reproduces the original fragmented-MP4 stream.
func (d *Downloader) assembleSegments(ctx context.Context, urls []string, dst io.Writer) error {
	for _, segURL := range urls {
		if err := d.fetchSegment(ctx, segURL, dst); err != nil {
			return err
		}
	}

	return nil
}

// fetchSegment fetches one segment, retrying a persistent network failure up to
// segmentAttempts times before failing the track. A cancelled context aborts
// immediately without consuming retries. Bytes are written only once a segment
// has been read in full, so a retry never corrupts the concatenation.
func (d *Downloader) fetchSegment(ctx context.Context, segURL string, dst io.Writer) error {
	var lastErr error
	for range segmentAttempts {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("download: dash segment %q: %w", segURL, ctxErr)
		}

		body, err := d.getSegment(ctx, segURL)
		if err != nil {
			lastErr = err

			continue
		}

		if _, err = dst.Write(body); err != nil {
			return classifyIOErr("write dash segment "+segURL, err)
		}

		return nil
	}

	return fmt.Errorf("download: dash segment %q after %d attempts: %w", segURL, segmentAttempts, lastErr)
}

// getSegment performs a single GET and returns the full segment body. It reports
// [ErrUnexpectedStatus] for a non-200 response.
func (d *Downloader) getSegment(ctx context.Context, segURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, segURL, nil)
	if err != nil {
		return nil, fmt.Errorf("download: new request %q: %w", segURL, err)
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download: get %q: %w", segURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download: get %q status %d: %w", segURL, resp.StatusCode, ErrUnexpectedStatus)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("download: read %q: %w", segURL, err)
	}

	return body, nil
}

// ffmpegDemux runs the injected ffmpeg to copy the audio stream of the scratch
// container at srcPath into a FLAC stream written to dst. It copies the stream
// (no lossy re-encode), captures stderr for diagnostics, and reports [ErrFFmpeg]
// on any failure. exec.CommandContext kills the child if ctx is cancelled.
func (d *Downloader) ffmpegDemux(ctx context.Context, srcPath string, dst io.Writer) error {
	args := []string{
		"-nostdin", "-hide_banner", "-loglevel", "error",
		"-i", srcPath,
		"-map", "0:a:0",
		"-c:a", "copy",
		"-f", "flac",
		"pipe:1",
	}

	//nolint:gosec // G204: ffmpeg path is injected by the caller; args are fixed literals plus a self-created temp path, never user input
	cmd := exec.CommandContext(ctx, d.ffmpegPath, args...)
	cmd.Stdout = dst

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("download: ffmpeg demux cancelled: %w", ctxErr)
		}

		return fmt.Errorf("download: ffmpeg demux (stderr: %s): %w: %w",
			strings.TrimSpace(stderr.String()), ErrFFmpeg, err)
	}

	return nil
}
