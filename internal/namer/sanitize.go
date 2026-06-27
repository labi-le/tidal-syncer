package namer

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const (
	// maxComponentBytes is the largest single path component we emit. 255 is
	// the most restrictive common per-name byte limit (ext4, APFS, NTFS all
	// allow at least this many bytes per component).
	maxComponentBytes = 255
	// maxComLPTIndex is the highest COMn / LPTn device index Windows reserves.
	maxComLPTIndex = 9
	// replacement substitutes any character illegal in a path component.
	replacement = '_'
	// placeholder is returned when sanitization would otherwise yield "".
	placeholder = "_"
	// reservedSuffix is appended to a Windows reserved device name.
	reservedSuffix = "_"
	// trailingCutset is trimmed from the right of every component.
	trailingCutset = ". "
	// illegalChars are the characters never allowed inside a path component.
	illegalChars = `/\:*?"<>|`
)

// reservedNames is the set of upper-cased Windows reserved device names. A
// component whose stem matches one (case-insensitively) is illegal on Windows.
var reservedNames = buildReservedNames()

// Sanitize converts an arbitrary string into one safe path component. It maps
// illegal and control characters to '_', folds to NFC, strips trailing dots
// and spaces, suffixes Windows reserved device names, and truncates to
// maxComponentBytes on a rune boundary. The result is never empty and never
// contains a path separator, so a field value can never escape the tree.
func Sanitize(component string) string {
	cleaned := strings.Map(sanitizeRune, component)
	cleaned = norm.NFC.String(cleaned)
	cleaned = strings.TrimRight(cleaned, trailingCutset)
	cleaned = suffixIfReserved(cleaned)
	cleaned = truncateToBytes(cleaned, maxComponentBytes)
	cleaned = strings.TrimRight(cleaned, trailingCutset)
	if cleaned == "" {
		return placeholder
	}
	return cleaned
}

// sanitizeRune maps a single illegal or control rune to replacement, leaving
// every other rune untouched.
func sanitizeRune(r rune) rune {
	if unicode.IsControl(r) || strings.ContainsRune(illegalChars, r) {
		return replacement
	}
	return r
}

// suffixIfReserved appends reservedSuffix when the stem (the text before the
// first dot) is a Windows reserved device name, preserving any extension.
func suffixIfReserved(name string) string {
	stem, _, _ := strings.Cut(name, ".")
	if _, reserved := reservedNames[strings.ToUpper(stem)]; reserved {
		return stem + reservedSuffix + name[len(stem):]
	}
	return name
}

// truncateToBytes returns the longest prefix of s that is at most maxBytes
// bytes and ends on a UTF-8 rune boundary.
func truncateToBytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// buildReservedNames constructs the upper-cased Windows reserved device-name
// set: CON, PRN, AUX, NUL plus COM1-9 and LPT1-9.
func buildReservedNames() map[string]struct{} {
	base := []string{"CON", "PRN", "AUX", "NUL"}
	set := make(map[string]struct{}, len(base)+maxComLPTIndex*2)
	for _, name := range base {
		set[name] = struct{}{}
	}
	for i := 1; i <= maxComLPTIndex; i++ {
		set["COM"+strconv.Itoa(i)] = struct{}{}
		set["LPT"+strconv.Itoa(i)] = struct{}{}
	}
	return set
}
