// Package sanitize provides input sanitization for Decoyd alert payloads and
// TUI renderers.
//
// All external strings (token paths, labels, process names, cmdlines, details)
// MUST be sanitized before being embedded in an AlertPayload or displayed in
// the TUI. Sanitization removes:
//   - ANSI escape sequences (CSI, OSC, etc.)
//   - C0 control characters (U+0000–U+001F, except TAB converted to space)
//   - C1 control characters (U+0080–U+009F)
//   - Newline / carriage-return characters (U+000A, U+000D) → replaced with space
//   - Unicode bidirectional override characters (U+200F, U+202A–U+202E,
//     U+2066–U+2069, U+200E)
//   - Zero-width characters (U+200B, U+200C, U+200D, U+FEFF)
//
// The result is then truncated to maxLen Unicode code points.
//
// Rule 13 (AGENTS.md): "Sanitize before building any AlertPayload."
package sanitize

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// ansiEscape matches ANSI/VT100 escape sequences:
//   - CSI sequences: ESC [ ... final-byte
//   - OSC sequences: ESC ] ... ST  (ST = ESC\ or BEL)
//   - Other two-char ESC sequences
var ansiEscape = regexp.MustCompile(
	`\x1b(?:` +
		`\[[0-9;?]*[A-Za-z]` + // CSI sequences
		`|\][^\x07\x1b]*(?:\x07|\x1b\\)` + // OSC sequences
		`|[^[]` + // other ESC+char (not CSI)
		`)`)

// bidiAndZeroWidth is the set of Unicode code points that are bidi overrides
// or zero-width/formatting characters that must be stripped.
var bidiAndZeroWidth = map[rune]bool{
	'\u200B': true, // ZERO WIDTH SPACE
	'\u200C': true, // ZERO WIDTH NON-JOINER
	'\u200D': true, // ZERO WIDTH JOINER
	'\u200E': true, // LEFT-TO-RIGHT MARK
	'\u200F': true, // RIGHT-TO-LEFT MARK
	'\u202A': true, // LEFT-TO-RIGHT EMBEDDING
	'\u202B': true, // RIGHT-TO-LEFT EMBEDDING
	'\u202C': true, // POP DIRECTIONAL FORMATTING
	'\u202D': true, // LEFT-TO-RIGHT OVERRIDE
	'\u202E': true, // RIGHT-TO-LEFT OVERRIDE
	'\u2066': true, // LEFT-TO-RIGHT ISOLATE
	'\u2067': true, // RIGHT-TO-LEFT ISOLATE
	'\u2068': true, // FIRST STRONG ISOLATE
	'\u2069': true, // POP DIRECTIONAL ISOLATE
	'\uFEFF': true, // ZERO WIDTH NO-BREAK SPACE (BOM)
}

// MaxLen is the default maximum string length (code points) for sanitized
// alert payload fields. Individual callers may pass a different limit.
const MaxLen = 512

// Sanitize removes control characters, ANSI escapes, bidi overrides, and
// zero-width characters from s, then truncates to maxLen Unicode code points.
// If maxLen <= 0, MaxLen is used.
func Sanitize(s string, maxLen int) string {
	if maxLen <= 0 {
		maxLen = MaxLen
	}

	// 1. Strip ANSI escape sequences first (before rune-level scan).
	s = ansiEscape.ReplaceAllString(s, "")

	// 2. Rune-level pass: strip control chars, bidi overrides, zero-width chars.
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case bidiAndZeroWidth[r]:
			// strip silently
		case r == '\t', r == '\n', r == '\r':
			// Replace whitespace control chars with a plain space
			b.WriteByte(' ')
		case r < 0x20:
			// Other C0 control chars: strip
		case r >= 0x80 && r <= 0x9F:
			// C1 control chars: strip
		case !utf8.ValidRune(r):
			// Invalid rune: strip
		default:
			b.WriteRune(r)
		}
	}
	s = b.String()

	// 3. Truncate to maxLen code points.
	s = truncateCodePoints(s, maxLen)

	return s
}

// truncateCodePoints returns s truncated to at most n Unicode code points.
func truncateCodePoints(s string, n int) string {
	count := 0
	for i := range s {
		if count >= n {
			return s[:i]
		}
		count++
	}
	return s
}
