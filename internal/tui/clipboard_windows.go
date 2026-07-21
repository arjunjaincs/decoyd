//go:build windows

package tui

// clipboard_windows.go — Windows clipboard read/write via WinAPI.
//
// readClipboard: used for Ctrl+V in legacy conhost (PowerShell 5.1, cmd.exe)
// where the terminal does NOT translate Ctrl+V into bracketed-paste sequences.
// Modern hosts (Windows Terminal, VSCode terminal) already handle paste via the
// bracketed-paste protocol that bubbletea enables by default, so on those hosts
// the text arrives through the normal rune path without this function.
//
// writeClipboard: used for Ctrl+C when a text field is active so the user can
// copy the current field value to the clipboard without quitting the program.

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32dll   = windows.NewLazySystemDLL("user32.dll")
	kernel32dll = windows.NewLazySystemDLL("kernel32.dll")

	procOpenClipboard    = user32dll.NewProc("OpenClipboard")
	procCloseClipboard   = user32dll.NewProc("CloseClipboard")
	procGetClipboardData = user32dll.NewProc("GetClipboardData")
	procEmptyClipboard   = user32dll.NewProc("EmptyClipboard")
	procSetClipboardData = user32dll.NewProc("SetClipboardData")
	procGlobalAlloc      = kernel32dll.NewProc("GlobalAlloc")
	procGlobalLock       = kernel32dll.NewProc("GlobalLock")
	procGlobalUnlock     = kernel32dll.NewProc("GlobalUnlock")
)

const (
	cfUnicodeText = 13     // CF_UNICODETEXT
	gmemMoveable  = 0x0002 // GMEM_MOVEABLE
)

// readClipboard returns the current clipboard text as a UTF-8 string.
// Returns "" on any failure (clipboard locked, no text data, etc.).
func readClipboard() string {
	r, _, _ := procOpenClipboard.Call(0)
	if r == 0 {
		return ""
	}
	defer procCloseClipboard.Call()

	h, _, _ := procGetClipboardData.Call(cfUnicodeText)
	if h == 0 {
		return ""
	}

	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return ""
	}
	defer procGlobalUnlock.Call(h)

	// p is a pointer to a null-terminated UTF-16 string.
	// Scan for the null terminator (capped at 1 MiB of UTF-16 = 524,288 chars).
	const maxChars = 1 << 19
	buf := (*[maxChars]uint16)(unsafe.Pointer(p))
	n := 0
	for n < maxChars && buf[n] != 0 {
		n++
	}
	// windows.UTF16ToString expects a null-terminated slice.
	return windows.UTF16ToString(buf[:n+1])
}

// writeClipboard places text on the Windows clipboard as CF_UNICODETEXT.
// Silently ignores failures — clipboard write is best-effort.
func writeClipboard(text string) {
	utf16, err := windows.UTF16FromString(text)
	if err != nil {
		return
	}
	byteLen := uintptr(len(utf16) * 2) // each UTF-16 unit = 2 bytes

	hMem, _, _ := procGlobalAlloc.Call(gmemMoveable, byteLen)
	if hMem == 0 {
		return
	}

	ptr, _, _ := procGlobalLock.Call(hMem)
	if ptr == 0 {
		return
	}
	copy((*[1 << 19]uint16)(unsafe.Pointer(ptr))[:len(utf16)], utf16)
	procGlobalUnlock.Call(hMem)

	r, _, _ := procOpenClipboard.Call(0)
	if r == 0 {
		return
	}
	defer procCloseClipboard.Call()

	procEmptyClipboard.Call()
	// SetClipboardData takes ownership of hMem on success;
	// if it fails, hMem would leak — acceptable for a best-effort path.
	procSetClipboardData.Call(cfUnicodeText, hMem)
}
