package tidal

import (
	"strconv"
	"strings"
)

// Cover image URL construction.
const (
	coverBaseURL = "https://resources.tidal.com/images"
	coverExt     = "jpg"
)

// CoverURL builds the public cover-art URL for a cover UUID at the given square
// size in pixels. TIDAL stores cover art under a path formed by replacing the
// UUID's dashes with slashes; common sizes are 80, 160, 320, 640, and 1280.
func CoverURL(coverUUID string, size int) string {
	path := strings.ReplaceAll(coverUUID, "-", "/")
	dim := strconv.Itoa(size)
	return coverBaseURL + "/" + path + "/" + dim + "x" + dim + "." + coverExt
}
