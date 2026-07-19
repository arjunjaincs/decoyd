//go:build windows

package tui

import (
	"os"

	"golang.org/x/sys/windows"
)

// hasVTSupport returns true when the host terminal is capable of rendering
// ANSI / VT sequences and Unicode characters. Three detection paths in order:
//
//  1. WT_SESSION / WT_PROFILE_ID — Windows Terminal injects these env vars
//     into every child process it hosts, including cmd.exe and PowerShell
//     subshells. WT interprets VT at the emulator layer regardless of the
//     console-mode flag on the child's handle.
//
//  2. ENABLE_VIRTUAL_TERMINAL_PROCESSING already set — some hosts (VSCode
//     integrated terminal, ConEmu, PowerShell 7+) enable it proactively.
//
//  3. Try to enable EVTP ourselves — on Windows 10 v1511+ SetConsoleMode
//     with the EVTP flag succeeds; on older console hosts it fails. If it
//     succeeds we restore the original mode (bubbletea sets it correctly
//     during Init) and return true. This is the correct path for standalone
//     PowerShell 5.1, cmd.exe, and any host that supports VT but hasn't
//     pre-enabled it. If it fails, we genuinely cannot render VT → ASCII.
func hasVTSupport() bool {
	// 1. Windows Terminal.
	if os.Getenv("WT_SESSION") != "" || os.Getenv("WT_PROFILE_ID") != "" {
		return true
	}

	handle := windows.Handle(windows.Stdout)
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		// stdout is redirected (pipe / file) — no VT.
		return false
	}

	const enableVTP = uint32(0x0004)

	// 2. Already enabled.
	if mode&enableVTP != 0 {
		return true
	}

	// 3. Probe: try enabling it, then restore.
	// SetConsoleMode fails on pre-1511 Windows or non-console handles.
	if err := windows.SetConsoleMode(handle, mode|enableVTP); err != nil {
		return false
	}
	// Restore — bubbletea calls SetConsoleMode again during p.Run().
	_ = windows.SetConsoleMode(handle, mode)
	return true
}
