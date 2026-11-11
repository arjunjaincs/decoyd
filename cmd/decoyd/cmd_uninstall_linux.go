//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// cmdUninstall stops and removes the decoyd systemd user service.
// It is the complement of cmdInstall. It does NOT remove the data directory
// or any deployed token files; those are the user's responsibility.
func cmdUninstall(dataDir string) error {
	unitDir := filepath.Join(os.Getenv("HOME"), ".config", "systemd", "user")
	unitPath := filepath.Join(unitDir, "decoyd.service")

	// Stop and disable the service. These are best-effort — if systemd is not
	// running (e.g. in WSL without systemd), the unit file removal still proceeds.
	for _, args := range [][]string{
		{"--user", "stop", "decoyd.service"},
		{"--user", "disable", "decoyd.service"},
	} {
		cmd := exec.Command("systemctl", args...) // #nosec G204 -- fixed arguments
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			// Not fatal — log and continue. The service may already be stopped
			// or systemd may not be running (e.g. WSL1).
			fmt.Printf("warning: systemctl %v: %v\n", args, err)
		}
	}

	// Remove the unit file.
	if err := os.Remove(unitPath); err != nil { // #nosec G703 -- unitPath is the internal systemd unit path, not user-controlled taint
		if os.IsNotExist(err) {
			fmt.Println("No unit file found at", unitPath, "(already removed?)")
		} else {
			return fmt.Errorf("remove unit file: %w", err)
		}
	} else {
		fmt.Println("Removed unit file:", unitPath)
	}

	// Reload systemd so the removed unit is gone from its internal state.
	cmd := exec.Command("systemctl", "--user", "daemon-reload") // #nosec G204 -- fixed systemctl --user daemon-reload arguments
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Println("warning: daemon-reload failed:", err)
	}

	fmt.Println("decoyd service uninstalled.")
	fmt.Println("Note: data directory and deployed token files were not removed.")
	return nil
}
