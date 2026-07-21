//go:build !windows

package tui

// clipboard_notwindows.go — stubs for non-Windows platforms.
//
// On Linux and macOS the terminal itself handles Ctrl+V paste via the
// bracketed-paste protocol (already enabled by default in bubbletea v1.3.4),
// so the pasted text arrives through the normal rune path without any explicit
// clipboard read.
//
// Ctrl+C copy-to-clipboard is intentionally a no-op on non-Windows because
// xclip/xsel/pbcopy are not guaranteed to be present; users on those platforms
// can select+copy text using their terminal's native selection mechanism.

func readClipboard() string  { return "" }
func writeClipboard(_ string) {}
