package config

import (
	"math/rand/v2"
	"time"
)

func (r DurationRange) Random() time.Duration {
	if r.Max <= r.Min {
		return r.Min
	}

	span := r.Max - r.Min + time.Nanosecond

	return r.Min + time.Duration(rand.Uint64N(uint64(span)))
}
