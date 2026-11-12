package main

import (
	"encoding/base64"
	"testing"
	"unicode/utf16"
)

// ----------------------------------------------------------------------------
// quoteExecStart tests
// ----------------------------------------------------------------------------

func TestQuoteExecStart_SimplePath(t *testing.T) {
	got := quoteExecStart("/usr/local/bin/decoyd")
	want := `"/usr/local/bin/decoyd"`
	if got != want {
		t.Errorf("quoteExecStart(%q) = %q; want %q", "/usr/local/bin/decoyd", got, want)
	}
}

func TestQuoteExecStart_PathWithSpaces(t *testing.T) {
	got := quoteExecStart("/home/user/my program/decoyd")
	want := `"/home/user/my program/decoyd"`
	if got != want {
		t.Errorf("quoteExecStart(%q) = %q; want %q", "/home/user/my program/decoyd", got, want)
	}
}

func TestQuoteExecStart_PathWithPercent(t *testing.T) {
	// systemd expands %h (home), %u (user) etc. — must be doubled.
	got := quoteExecStart("/home/%huser/decoyd")
	want := `"/home/%%huser/decoyd"`
	if got != want {
		t.Errorf("quoteExecStart(%q) = %q; want %q", "/home/%huser/decoyd", got, want)
	}
}

func TestQuoteExecStart_PathWithDollar(t *testing.T) {
	// systemd variable references $HOME etc. — must be doubled.
	got := quoteExecStart("/home/$HOME/decoyd")
	want := `"/home/$$HOME/decoyd"`
	if got != want {
		t.Errorf("quoteExecStart(%q) = %q; want %q", "/home/$HOME/decoyd", got, want)
	}
}

func TestQuoteExecStart_PathWithBackslash(t *testing.T) {
	// Backslash in path (unusual on Linux but must be handled).
	got := quoteExecStart("/path\\with\\backslash")
	want := `"/path\\with\\backslash"`
	if got != want {
		t.Errorf("quoteExecStart(%q) = %q; want %q", "/path\\with\\backslash", got, want)
	}
}

func TestQuoteExecStart_PathWithDoubleQuote(t *testing.T) {
	got := quoteExecStart(`/path/to/"quoted"/decoyd`)
	want := `"/path/to/\"quoted\"/decoyd"`
	if got != want {
		t.Errorf("quoteExecStart(%q) = %q; want %q", `/path/to/"quoted"/decoyd`, got, want)
	}
}

func TestQuoteExecStart_AllSpecialChars(t *testing.T) {
	// A path with all special chars together.
	got := quoteExecStart(`/a%b$c\d"e`)
	want := `"/a%%b$$c\\d\"e"`
	if got != want {
		t.Errorf("quoteExecStart(%q) = %q; want %q", `/a%b$c\d"e`, got, want)
	}
}

// ----------------------------------------------------------------------------
// encodeUTF16LE / encodedPSCommand tests
// ----------------------------------------------------------------------------

func TestEncodeUTF16LE_ASCII(t *testing.T) {
	// ASCII "AB" → 0x41 0x00 0x42 0x00
	got := encodeUTF16LE("AB")
	want := []byte{0x41, 0x00, 0x42, 0x00}
	if string(got) != string(want) {
		t.Errorf("encodeUTF16LE(\"AB\") = %v; want %v", got, want)
	}
}

func TestEncodeUTF16LE_Unicode(t *testing.T) {
	// U+2019 RIGHT SINGLE QUOTATION MARK (the "smart quote" that breaks PowerShell escaping)
	// U+2019 = 0x2019 → LE bytes: 0x19 0x20
	got := encodeUTF16LE("\u2019")
	if len(got) != 2 {
		t.Fatalf("encodeUTF16LE(U+2019) len = %d; want 2", len(got))
	}
	if got[0] != 0x19 || got[1] != 0x20 {
		t.Errorf("encodeUTF16LE(U+2019) = [%02x %02x]; want [19 20]", got[0], got[1])
	}
}

func TestEncodedPSCommand_RoundTrip(t *testing.T) {
	script := "Write-Host 'hello\u2019world'"
	encoded := encodedPSCommand(script)
	if len(encoded) == 0 {
		t.Fatal("encodedPSCommand returned empty string")
	}
	// Must be valid base64.
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("encodedPSCommand produced invalid base64: %v", err)
	}
	if len(raw)%2 != 0 {
		t.Error("decoded bytes not aligned to 2 (not valid UTF-16LE)")
	}
	// Re-decode UTF-16LE to string.
	u16 := make([]uint16, len(raw)/2)
	for i := range u16 {
		u16[i] = uint16(raw[i*2]) | uint16(raw[i*2+1])<<8
	}
	runes := utf16.Decode(u16)
	got := string(runes)
	if got != script {
		t.Errorf("round-trip failed: got %q; want %q", got, script)
	}
}
