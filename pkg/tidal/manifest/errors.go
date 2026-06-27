package manifest

import (
	"errors"
	"strconv"
)

// ErrEncrypted indicates the manifest declares an encryption type other than
// NONE. tidal-syncer does not implement DRM decryption, so encrypted streams
// cannot be downloaded and are rejected at parse time.
var ErrEncrypted = errors.New("manifest: stream is encrypted")

// ErrInvalidManifest indicates the decoded manifest is structurally invalid,
// for example a DASH manifest with no usable representation or segment
// template, or a BTS manifest carrying no download URLs.
var ErrInvalidManifest = errors.New("manifest: invalid manifest")

// UnknownMimeTypeError is returned by [Parse] when the supplied MIME type
// matches neither the BTS ([MimeBTS]) nor the DASH ([MimeDASH]) manifest
// format.
type UnknownMimeTypeError struct {
	// MimeType is the unrecognized MIME type that was supplied to Parse.
	MimeType string
}

// Error implements the error interface.
func (e *UnknownMimeTypeError) Error() string {
	return "manifest: unknown mime type " + strconv.Quote(e.MimeType)
}
