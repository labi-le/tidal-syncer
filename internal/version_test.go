package internal_test

import (
	"testing"

	"github.com/labi-le/tidal-syncer/internal"
)

// TestVersionVarsAddressable proves the three build-time vars exist as
// addressable strings so the Makefile/.goreleaser ldflags targets resolve.
func TestVersionVarsAddressable(t *testing.T) {
	t.Helper()

	// Given: the build-time variables declared in internal/version.go
	// When:  we take their addresses
	// Then:  the compiler accepts it (the vars are addressable strings)
	_ = &internal.Version
	_ = &internal.CommitHash
	_ = &internal.BuildTime
}
