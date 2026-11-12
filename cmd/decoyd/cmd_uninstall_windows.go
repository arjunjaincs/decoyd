//go:build windows

package main

import (
	"fmt"
	"os/exec"
)

// cmdUninstall removes the DecoydWatch Task Scheduler task.
// It is the complement of cmdInstall. It does NOT remove the data directory
// or any deployed token files; those are the user's responsibility.
func cmdUninstall(dataDir string) error {
	psScript := `Unregister-ScheduledTask -TaskName 'DecoydWatch' -Confirm:$false`
	encoded := encodedPSCommand(psScript)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand", encoded) // #nosec G204 -- fixed
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("unregister task: %w\n%s", err, string(out))
	}
	fmt.Println("Task Scheduler task 'DecoydWatch' removed.")
	fmt.Println("Note: data directory and deployed token files were not removed.")
	return nil
}
