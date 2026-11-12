package sanitize_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/arjunjaincs/decoyd/internal/sanitize"
)

// ----------------------------------------------------------------------------
// Table-driven tests
// ----------------------------------------------------------------------------

func TestSanitize_RemovesANSI(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"CSI reset", "\x1b[0m"},
		{"CSI bold red", "\x1b[1;31m"},
		{"CSI move cursor", "\x1b[2;3H"},
		{"OSC window title", "\x1b]0;title\x07"},
		{"ESC+char", "\x1bM"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitize.Sanitize("hello"+tc.input+"world", 0)
			if strings.ContainsRune(got, '\x1b') {
				t.Errorf("Sanitize(%q) still contains ESC: %q", tc.input, got)
			}
			if !strings.Contains(got, "helloworld") {
				t.Errorf("Sanitize removed non-ANSI text: got %q", got)
			}
		})
	}
}

func TestSanitize_RemovesControlChars(t *testing.T) {
	for c := rune(0); c < 0x20; c++ {
		if c == '\t' || c == '\n' || c == '\r' {
			// These are replaced with space, not removed
			continue
		}
		input := "a" + string(c) + "b"
		got := sanitize.Sanitize(input, 0)
		if strings.ContainsRune(got, c) {
			t.Errorf("Sanitize(%q) still contains control char U+%04X: %q", input, c, got)
		}
	}
	// C1 range
	for c := rune(0x80); c <= 0x9F; c++ {
		input := "a" + string(c) + "b"
		got := sanitize.Sanitize(input, 0)
		if strings.ContainsRune(got, c) {
			t.Errorf("Sanitize(%q) still contains C1 control char U+%04X: %q", input, c, got)
		}
	}
}

func TestSanitize_ReplacesNewlinesWithSpace(t *testing.T) {
	got := sanitize.Sanitize("hello\nworld\r\n!", 0)
	if strings.ContainsAny(got, "\n\r") {
		t.Errorf("Sanitize did not replace newlines: %q", got)
	}
	if !strings.Contains(got, "hello") || !strings.Contains(got, "world") {
		t.Errorf("Sanitize removed content: %q", got)
	}
}

func TestSanitize_RemovesBidiOverrides(t *testing.T) {
	bidiChars := []rune{
		'\u200E', '\u200F',
		'\u202A', '\u202B', '\u202C', '\u202D', '\u202E',
		'\u2066', '\u2067', '\u2068', '\u2069',
	}
	for _, r := range bidiChars {
		input := "safe" + string(r) + "text"
		got := sanitize.Sanitize(input, 0)
		if strings.ContainsRune(got, r) {
			t.Errorf("Sanitize(%q) still contains bidi char U+%04X", input, r)
		}
		if got != "safetext" {
			t.Errorf("Sanitize(%q) = %q; want %q", input, got, "safetext")
		}
	}
}

func TestSanitize_RemovesZeroWidthChars(t *testing.T) {
	zwChars := []rune{'\u200B', '\u200C', '\u200D', '\uFEFF'}
	for _, r := range zwChars {
		input := "a" + string(r) + "b"
		got := sanitize.Sanitize(input, 0)
		if strings.ContainsRune(got, r) {
			t.Errorf("Sanitize(%q) still contains zero-width char U+%04X", input, r)
		}
	}
}

func TestSanitize_TruncatesLongStrings(t *testing.T) {
	long := strings.Repeat("x", 600)
	got := sanitize.Sanitize(long, 0) // default MaxLen = 512
	if utf8.RuneCountInString(got) > sanitize.MaxLen {
		t.Errorf("Sanitize did not truncate: len=%d, want <= %d", utf8.RuneCountInString(got), sanitize.MaxLen)
	}
	custom := sanitize.Sanitize(long, 100)
	if utf8.RuneCountInString(custom) > 100 {
		t.Errorf("Sanitize custom maxLen: len=%d, want <= 100", utf8.RuneCountInString(custom))
	}
}

func TestSanitize_SafeInputPassthrough(t *testing.T) {
	safe := "/home/user/.aws/credentials"
	got := sanitize.Sanitize(safe, 0)
	if got != safe {
		t.Errorf("Sanitize(%q) = %q; want passthrough", safe, got)
	}
}

func TestSanitize_AtEveryoneDiscord(t *testing.T) {
	// @everyone in Discord content can ping everyone — it must pass through
	// as plain text (the calling code adds allowed_mentions; we don't mangle it).
	input := "@everyone and @here"
	got := sanitize.Sanitize(input, 0)
	if got != input {
		t.Errorf("Sanitize(%q) = %q; want passthrough (mention neutralization is sink's job)", input, got)
	}
}

func TestSanitize_SlackMention(t *testing.T) {
	// <!channel> in Slack content — must pass through (sink uses plain_text type).
	input := "<!channel> alert"
	got := sanitize.Sanitize(input, 0)
	if got != input {
		t.Errorf("Sanitize(%q) = %q; want passthrough (plain_text type neutralizes it)", input, got)
	}
}

func TestSanitize_BoldMarkdown(t *testing.T) {
	input := "*bold* text"
	got := sanitize.Sanitize(input, 0)
	if got != input {
		t.Errorf("Sanitize(%q) = %q; want passthrough (markdown escaping is sink's job)", input, got)
	}
}

func TestSanitize_MixedAttack(t *testing.T) {
	// Payload mixing ANSI, bidi, C0, newline, and normal text.
	input := "\x1b[31mred\x1b[0m\u202Ehidden\u202Cvisible\x00\ntest"
	got := sanitize.Sanitize(input, 0)
	if strings.ContainsRune(got, '\x1b') {
		t.Errorf("ANSI escape not stripped: %q", got)
	}
	if strings.ContainsRune(got, '\u202E') || strings.ContainsRune(got, '\u202C') {
		t.Errorf("Bidi override not stripped: %q", got)
	}
	if strings.ContainsRune(got, '\x00') {
		t.Errorf("Null byte not stripped: %q", got)
	}
	if strings.ContainsAny(got, "\n\r") {
		t.Errorf("Newline not replaced: %q", got)
	}
	// Legitimate characters must survive.
	if !strings.Contains(got, "red") || !strings.Contains(got, "visible") || !strings.Contains(got, "test") {
		t.Errorf("Safe content stripped: %q", got)
	}
}

func TestSanitize_EmptyString(t *testing.T) {
	if got := sanitize.Sanitize("", 0); got != "" {
		t.Errorf("Sanitize(\"\") = %q; want \"\"", got)
	}
}

func TestSanitize_OnlyControlChars(t *testing.T) {
	input := "\x00\x01\x02\x03\x1b[31m"
	got := sanitize.Sanitize(input, 0)
	if got != "" {
		t.Errorf("Sanitize(all-control) = %q; want \"\"", got)
	}
}

// ----------------------------------------------------------------------------
// Fuzz target
// ----------------------------------------------------------------------------

// FuzzSanitize verifies that sanitize.Sanitize never produces output containing
// ANSI escapes, C0/C1 control chars, newlines, bidi overrides, zero-width
// chars, and that the output length is bounded.
func FuzzSanitize(f *testing.F) {
	// Seed corpus
	f.Add("normal text")
	f.Add("\x1b[31mred\x1b[0m")
	f.Add("\u202Ebidi")
	f.Add("\x00\x01\x1f")
	f.Add("@everyone <!channel> *bold*")
	f.Add(strings.Repeat("x", 1000))

	bidiSet := map[rune]bool{
		'\u200E': true, '\u200F': true,
		'\u202A': true, '\u202B': true, '\u202C': true, '\u202D': true, '\u202E': true,
		'\u2066': true, '\u2067': true, '\u2068': true, '\u2069': true,
	}
	zwSet := map[rune]bool{
		'\u200B': true, '\u200C': true, '\u200D': true, '\uFEFF': true,
	}

	f.Fuzz(func(t *testing.T, s string) {
		got := sanitize.Sanitize(s, sanitize.MaxLen)

		// No ESC character.
		if strings.ContainsRune(got, '\x1b') {
			t.Errorf("output contains ESC for input %q: %q", s, got)
		}
		// No C0 (except space=0x20, which is fine).
		for _, r := range got {
			if r < 0x20 {
				t.Errorf("output contains C0 control U+%04X for input %q: %q", r, s, got)
			}
			if r >= 0x80 && r <= 0x9F {
				t.Errorf("output contains C1 control U+%04X for input %q: %q", r, s, got)
			}
			if bidiSet[r] {
				t.Errorf("output contains bidi override U+%04X for input %q: %q", r, s, got)
			}
			if zwSet[r] {
				t.Errorf("output contains zero-width char U+%04X for input %q: %q", r, s, got)
			}
		}
		// No newlines.
		if strings.ContainsAny(got, "\n\r") {
			t.Errorf("output contains newline for input %q: %q", s, got)
		}
		// Length bounded.
		if utf8.RuneCountInString(got) > sanitize.MaxLen {
			t.Errorf("output exceeds MaxLen: got %d runes for input %q", utf8.RuneCountInString(got), s)
		}
	})
}
