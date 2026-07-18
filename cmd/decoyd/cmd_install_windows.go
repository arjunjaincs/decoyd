//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// cmdInstall registers a Windows Task Scheduler task that runs
// 'decoyd watch' at user log-on. It uses the built-in schtasks.exe and
// does NOT require administrator privileges (runs as the current user).
func cmdInstall(dataDir string) error {
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("resolve symlinks: %w", err)
	}

	// The task runs 'decoyd.exe watch' at logon, with a 30s delay.
	// /SC ONLOGON — triggers on user log-on.
	// /DELAY 0:30 — 30-second start delay to let other services settle.
	// /RU "" — runs as the current logged-on user.
	// /F — force overwrite if the task already exists.
	args := []string{
		"/Create",
		"/TN", "Decoyd\\DecoydWatch",
		"/TR", fmt.Sprintf(`"%s" watch`, execPath),
		"/SC", "ONLOGON",
		"/DELAY", "0:30",
		"/RU", "",
		"/F",
	}
	cmd := exec.Command("schtasks.exe", args...) // #nosec G204 -- fixed arguments, no user input
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("schtasks create: %w", err)
	}

	fmt.Println("Task Scheduler task registered: Decoyd\\DecoydWatch")
	fmt.Println("It will run 'decoyd watch' automatically at next logon.")
	fmt.Println()
	fmt.Println("To start now:    schtasks /Run /TN Decoyd\\DecoydWatch")
	fmt.Println("To stop:         schtasks /End /TN Decoyd\\DecoydWatch")
	fmt.Println("To remove:       schtasks /Delete /TN Decoyd\\DecoydWatch /F")
	fmt.Println()
	fmt.Println("NOTE: read-only file access is NOT detected on Windows (v1.1 limitation).")
	fmt.Println("      Only Write, Rename, and Remove events trigger alerts.")
	return nil
}
