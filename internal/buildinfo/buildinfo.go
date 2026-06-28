// Package buildinfo holds build-time metadata injected via ldflags.
package buildinfo

// Version is the semantic version tag, injected at build time via -ldflags.
var Version string

// CommitHash is the short git commit hash, injected at build time via -ldflags.
var CommitHash string

// BuildTime is the RFC3339 build timestamp, injected at build time via -ldflags.
var BuildTime string
