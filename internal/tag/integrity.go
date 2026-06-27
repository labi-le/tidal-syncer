package tag

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/mewkiz/flac"
)

// ErrTruncated reports that a FLAC stream decoded fewer audio samples than its
// StreamInfo header declares, i.e. the file was cut short.
var ErrTruncated = errors.New("tag: truncated FLAC stream")

// IntegrityCheck verifies that the FLAC at path is structurally sound: it has a
// valid "fLaC" signature and StreamInfo header, every audio frame decodes (the
// decoder verifies each frame's CRC), and the decoded sample count is not short
// of the total declared in StreamInfo. Callers run it before marking a
// downloaded track complete; a truncated or corrupt file returns an error.
func IntegrityCheck(path string) error {
	// flac.Open wraps the file in a bufio.Reader that its Close cannot reach,
	// leaking the descriptor; own the *os.File here and hand it to flac.New so
	// the deferred Close below releases it.
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open flac %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	stream, err := flac.New(f)
	if err != nil {
		return fmt.Errorf("parse flac %q: %w", path, err)
	}

	var decoded uint64
	for {
		frame, parseErr := stream.ParseNext()
		if errors.Is(parseErr, io.EOF) {
			break
		}
		if parseErr != nil {
			return fmt.Errorf("decode flac %q: %w", path, parseErr)
		}
		decoded += uint64(frame.BlockSize)
	}

	if want := stream.Info.NSamples; want != 0 && decoded < want {
		return fmt.Errorf("%w: %q decoded %d of %d samples", ErrTruncated, path, decoded, want)
	}

	return nil
}
