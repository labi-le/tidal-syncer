package tidal

// Quality is a TIDAL audio-quality tier. Its string values are TIDAL's wire
// identifiers, sent in playback requests and returned in playback responses, and
// must match the API verbatim; they are also persisted as-is. A value outside
// the four declared constants is an unrecognized wire tier and ranks lowest.
type Quality string

// The audio-quality tiers TIDAL advertises, highest to lowest. The string values
// are TIDAL's wire identifiers and must not change.
const (
	// QualityHiResLossless is the highest tier (24-bit lossless).
	QualityHiResLossless Quality = "HI_RES_LOSSLESS"
	// QualityLossless is the 16-bit lossless tier: the lossless floor.
	QualityLossless Quality = "LOSSLESS"
	// QualityHigh is a lossy tier (AAC) below the lossless floor.
	QualityHigh Quality = "HIGH"
	// QualityLow is the lowest lossy tier (AAC) below the lossless floor.
	QualityLow Quality = "LOW"
)

// Comparable ranks for the quality tiers; a higher rank is a better tier. An
// unrecognized tier ranks lowest so it never satisfies a skip or floor check.
const (
	rankUnknown  = 0
	rankLow      = 1
	rankHigh     = 2
	rankLossless = 3
	rankHiRes    = 4
)

// Rank maps the tier to a comparable rank where a higher value is a better tier:
// HI_RES_LOSSLESS=4, LOSSLESS=3, HIGH=2, LOW=1. An unrecognized wire tier ranks 0
// so it never compares as good as a recognized one.
func (q Quality) Rank() int {
	switch q {
	case QualityHiResLossless:
		return rankHiRes
	case QualityLossless:
		return rankLossless
	case QualityHigh:
		return rankHigh
	case QualityLow:
		return rankLow
	default:
		return rankUnknown
	}
}

// MeetsLosslessFloor reports whether the tier is at or above the lossless floor:
// it admits exactly the two lossless tiers (LOSSLESS and HI_RES_LOSSLESS). Any
// lossy tier (HIGH, LOW) or an unrecognized tier is below the floor.
func (q Quality) MeetsLosslessFloor() bool {
	return q == QualityLossless || q == QualityHiResLossless
}
