package tag

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	// lrcExtension is the file extension of a synced-lyric sidecar.
	lrcExtension = ".lrc"
	// lrcFileMode is the permission mode of a written .lrc sidecar. 0o644 matches
	// the world-readable music library so the sidecar is readable alongside its FLAC.
	lrcFileMode = 0o644
)

// WriteLRC writes lrc as a synced-lyric sidecar next to audioPath, reusing the
// same basename with a ".lrc" extension (e.g. "track.flac" -> "track.lrc"). Any
// existing sidecar at that path is overwritten.
func WriteLRC(audioPath, lrc string) error {
	sidecar := audioPath[:len(audioPath)-len(filepath.Ext(audioPath))] + lrcExtension
	if err := os.WriteFile(sidecar, []byte(lrc), lrcFileMode); err != nil {
		return fmt.Errorf("write lrc sidecar %q: %w", sidecar, err)
	}

	return nil
}
