package tidal

import (
	"regexp"
	"strings"
)

// redactionMask replaces any secret value removed by [Redact].
const redactionMask = "[REDACTED]"

// bearerTokenRe matches an OAuth bearer token, e.g. "Bearer eyJ...". The token
// charset covers base64url plus the JWT segment separator.
var bearerTokenRe = regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/-]+=*`)

// signedParamRe matches the value of a signature-bearing query parameter found
// in pre-signed download URLs (CloudFront, S3/X-Amz, Akamai, generic tokens).
var signedParamRe = regexp.MustCompile(
	`(?i)\b(?:sig|signature|x-amz-signature|x-amz-credential|x-amz-security-token` +
		`|token|access_token|policy|key-pair-id|hdnea|hdnts)=[^&\s"']+`)

// Redact returns a copy of s that is safe to log. It replaces every OAuth
// bearer token with a fixed mask and masks the value of any signature-bearing
// query parameter, while leaving non-secret text untouched. It operates on raw
// text such as log lines, URLs, or header dumps and never returns the original
// secret.
func Redact(s string) string {
	s = bearerTokenRe.ReplaceAllString(s, "Bearer "+redactionMask)
	return signedParamRe.ReplaceAllStringFunc(s, maskParamValue)
}

// maskParamValue keeps the "key=" prefix of a matched query parameter and
// replaces its value with the mask.
func maskParamValue(match string) string {
	eq := strings.IndexByte(match, '=')
	if eq < 0 {
		return match
	}
	return match[:eq+1] + redactionMask
}
