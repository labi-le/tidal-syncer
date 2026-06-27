// Package manifest parses Tidal playback manifests into typed, downloadable
// stream descriptions.
//
// Tidal returns a base64-encoded manifest whose concrete format is selected by
// a MIME type:
//
//   - application/vnd.tidal.bts: a JSON document listing direct download URLs.
//   - application/dash+xml: an MPEG-DASH MPD describing segmented media.
//
// [Parse] decodes either form into a single concrete [Manifest] value, which
// exposes the format-specific result via [Manifest.BTS] or [Manifest.DASH].
// The package performs no network access, logging, or DRM decryption;
// encrypted manifests are rejected with [ErrEncrypted].
package manifest

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// MIME types Tidal uses to tag the two supported manifest formats.
const (
	// MimeBTS is the MIME type of a BTS manifest: base64-encoded JSON listing
	// direct download URLs.
	MimeBTS = "application/vnd.tidal.bts"
	// MimeDASH is the MIME type of an MPEG-DASH manifest: base64-encoded MPD
	// XML describing segmented media.
	MimeDASH = "application/dash+xml"
)

// encryptionNone is the only encryptionType value tidal-syncer can download;
// any other value means the stream is DRM-protected.
const encryptionNone = "NONE"

// Kind identifies which concrete manifest format a [Manifest] was parsed from.
type Kind string

const (
	// KindBTS marks a [Manifest] parsed from a BTS (direct-URL) manifest.
	KindBTS Kind = "bts"
	// KindDASH marks a [Manifest] parsed from an MPEG-DASH manifest.
	KindDASH Kind = "dash"
)

// Manifest is the parsed result of a Tidal playback manifest. Exactly one of
// the format-specific accessors ([Manifest.BTS], [Manifest.DASH]) reports a
// value, as indicated by [Manifest.Kind]. The zero value is not a valid
// manifest; obtain one via [Parse].
type Manifest struct {
	kind Kind
	bts  BTS
	dash DASH
}

// Kind reports which concrete manifest format this value was parsed from.
func (m Manifest) Kind() Kind { return m.kind }

// BTS returns the BTS result and true when this manifest was parsed from a BTS
// manifest; otherwise it returns the zero [BTS] and false.
func (m Manifest) BTS() (BTS, bool) {
	return m.bts, m.kind == KindBTS
}

// DASH returns the DASH result and true when this manifest was parsed from an
// MPEG-DASH manifest; otherwise it returns the zero [DASH] and false.
func (m Manifest) DASH() (DASH, bool) {
	return m.dash, m.kind == KindDASH
}

// Parse decodes a base64-encoded Tidal manifest of the given MIME type.
//
// It returns an [UnknownMimeTypeError] for an unrecognized mimeType, a wrapped
// error for malformed base64 or malformed JSON/XML, [ErrEncrypted] when the
// manifest declares an encryption type other than NONE, and
// [ErrInvalidManifest] when the decoded document is structurally unusable.
func Parse(mimeType, base64Manifest string) (Manifest, error) {
	switch mimeType {
	case MimeBTS:
		return parseBTS(base64Manifest)
	case MimeDASH:
		return parseDASH(base64Manifest)
	default:
		return Manifest{}, &UnknownMimeTypeError{MimeType: mimeType}
	}
}

// decodeBase64 decodes a standard-encoding base64 manifest payload, wrapping
// the underlying error so callers can still recover it with errors.As.
func decodeBase64(base64Manifest string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(base64Manifest)
	if err != nil {
		return nil, fmt.Errorf("manifest: decode base64: %w", err)
	}

	return raw, nil
}

// BTS is a parsed BTS manifest: a codec MIME type plus the ordered list of
// direct download URLs that make up the stream.
type BTS struct {
	mimeType string
	urls     []string
}

// MimeType reports the codec MIME type declared by the manifest (for example
// "audio/mp4").
func (b BTS) MimeType() string { return b.mimeType }

// URLs returns a copy of the direct download URLs in manifest order.
func (b BTS) URLs() []string {
	out := make([]string, len(b.urls))
	copy(out, b.urls)

	return out
}

// btsPayload is the wire shape of a decoded BTS manifest.
type btsPayload struct {
	MimeType       string   `json:"mimeType"`
	EncryptionType string   `json:"encryptionType"`
	URLs           []string `json:"urls"`
}

// parseBTS decodes and validates a base64-encoded BTS manifest.
func parseBTS(base64Manifest string) (Manifest, error) {
	raw, err := decodeBase64(base64Manifest)
	if err != nil {
		return Manifest{}, err
	}

	var payload btsPayload
	if err = json.Unmarshal(raw, &payload); err != nil {
		return Manifest{}, fmt.Errorf("manifest: decode bts json: %w", err)
	}

	if payload.EncryptionType != encryptionNone {
		return Manifest{}, fmt.Errorf("manifest: encryption %q: %w", payload.EncryptionType, ErrEncrypted)
	}

	if len(payload.URLs) == 0 {
		return Manifest{}, fmt.Errorf("manifest: bts has no urls: %w", ErrInvalidManifest)
	}

	return Manifest{
		kind: KindBTS,
		bts:  BTS{mimeType: payload.MimeType, urls: payload.URLs},
	}, nil
}
