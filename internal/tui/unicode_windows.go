//go:build windows

package tui

import (
	"golang.org/x/sys/windows"
)

// hasVTSupport returns true when the Windows console has
// ENABLE_VIRTUAL_TERMINAL_PROCESSING active on stdout, meaning the terminal
// understands ANSI escape sequences and can render Unicode box-drawing chars.
//
// cmd.exe opened directly (double-click or plain "cmd") typically does NOT have
// VT enabled unless the user has done so manually. Windows Terminal, VSCode,
// the Antigravity IDE terminal, and PowerShell 7+ DO have it enabled.
func hasVTSupport() bool {
	// GetConsoleMode on stdout handle.
	handle := windows.Handle(windows.Stdout)
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		// If we can't query the mode (e.g. stdout is redirected to a file),
		// assume no VT support to stay safe.
		return false
	}
	const enableVirtualTerminalProcessing = 0x0004
	return mode&enableVirtualTerminalProcessing != 0
}
