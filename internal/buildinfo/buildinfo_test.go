package buildinfo_test

import (
	"testing"

	"github.com/labi-le/tidal-syncer/internal/buildinfo"
)

// TestVersionVarsAddressable proves the three build-time vars exist as
// addressable strings so the Makefile/.goreleaser ldflags targets resolve.
func TestVersionVarsAddressable(t *testing.T) {
	t.Helper()

	// Given: the build-time variables declared in internal/buildinfo/buildinfo.go
	// When:  we take their addresses
	// Then:  the compiler accepts it (the vars are addressable strings)
	_ = &buildinfo.Version
	_ = &buildinfo.CommitHash
	_ = &buildinfo.BuildTime
}
