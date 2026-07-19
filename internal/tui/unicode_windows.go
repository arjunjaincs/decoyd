//go:build windows

package tui

import (
	"os"

	"golang.org/x/sys/windows"
)

// hasVTSupport returns true when the process is running inside a terminal
// that understands ANSI/VT escape sequences and can render Unicode glyphs.
//
// Two detection paths — first match wins:
//
//  1. WT_SESSION / WT_PROFILE_ID env vars: Windows Terminal always sets these
//     for every child process it hosts, including cmd.exe subshells. WT
//     interprets VT sequences at the terminal-host level, independently of
//     whether the child process has ENABLE_VIRTUAL_TERMINAL_PROCESSING set in
//     its own console mode. So we can trust these vars unconditionally.
//
//  2. ENABLE_VIRTUAL_TERMINAL_PROCESSING on stdout: fallback for non-WT
//     terminals that do set the flag (VSCode, PowerShell 7+, ConEmu, etc.).
//
// cmd.exe opened directly (double-click, plain "cmd") sets neither, so it
// correctly falls through to false → ASCII fallback glyphs.
func hasVTSupport() bool {
	// Windows Terminal: WT_SESSION is always present; WT_PROFILE_ID is a
	// belt-and-suspenders check for older WT builds that set one but not both.
	if os.Getenv("WT_SESSION") != "" || os.Getenv("WT_PROFILE_ID") != "" {
		return true
	}

	// GetConsoleMode on the stdout handle covers everything else.
	handle := windows.Handle(windows.Stdout)
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		// stdout is redirected to a file or pipe — assume no VT.
		return false
	}
	const enableVirtualTerminalProcessing = 0x0004
	return mode&enableVirtualTerminalProcessing != 0
}
