//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// cmdInstall registers a Windows Task Scheduler task that runs
// 'decoyd watch' at user log-on without requiring administrator privileges.
//
// Neither schtasks.exe /SC ONLOGON nor Register-ScheduledTask reliably create
// logon-triggered tasks without elevation. This implementation uses the
// raw Task Scheduler COM API (ITaskService) via PowerShell, which allows
// registering a user-specific logon trigger (TASK_LOGON_INTERACTIVE_TOKEN)
// without admin rights.
func cmdInstall(dataDir string) error {
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("resolve symlinks: %w", err)
	}

	// Build a PowerShell script that creates a user-specific logon task via
	// the raw Task Scheduler COM API. This works without admin elevation.
	//
	// Key constants used:
	//   TASK_CREATE_OR_UPDATE (6): create or overwrite the task
	//   TASK_ACTION_EXEC (0): executable action
	//   TASK_TRIGGER_LOGON (9): fires at logon
	//   TASK_LOGON_INTERACTIVE_TOKEN (3): run as the current interactive user
	//   TASK_INSTANCES_IGNORE_NEW (3): ignore new instances if already running
	psLines := []string{
		`$execPath = '` + strings.ReplaceAll(execPath, `'`, `''`) + `'`,
		`$userId   = [System.Security.Principal.WindowsIdentity]::GetCurrent().Name`,
		`$ts = New-Object -ComObject Schedule.Service`,
		`$ts.Connect()`,
		`$folder = $ts.GetFolder('\')`,
		`$task = $ts.NewTask(0)`,
		`$task.RegistrationInfo.Description = 'Monitors deployed decoy token files and fires alerts on access'`,
		`$task.Settings.DisallowStartIfOnBatteries = $false`,
		`$task.Settings.StopIfGoingOnBatteries = $false`,
		`$task.Settings.ExecutionTimeLimit = 'PT0S'`,
		`$task.Settings.MultipleInstances = 3`,
		`$trigger = $task.Triggers.Create(9)`,
		`$trigger.UserId = $userId`,
		`$action = $task.Actions.Create(0)`,
		`$action.Path = $execPath`,
		`$action.Arguments = 'watch'`,
		`$folder.RegisterTaskDefinition('DecoydWatch', $task, 6, $userId, $null, 3) | Out-Null`,
	}
	psScript := strings.Join(psLines, "; ")

	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", psScript) // #nosec G204 -- fixed arguments, no user input
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("register scheduled task: %w", err)
	}

	fmt.Println("Task Scheduler task registered: DecoydWatch")
	fmt.Println("It will run 'decoyd watch' automatically at next logon.")
	fmt.Println()
	fmt.Println("To start now:    schtasks /Run /TN DecoydWatch")
	fmt.Println("To stop:         schtasks /End /TN DecoydWatch")
	fmt.Println("To remove:       schtasks /Delete /TN DecoydWatch /F")
	fmt.Println()
	fmt.Println("NOTE: The TUI also starts the watcher automatically when you open it.")
	fmt.Println("      Use 'decoyd install' only if you want persistent background monitoring")
	fmt.Println("      without keeping the TUI open (e.g. after a reboot).")
	fmt.Println()
	fmt.Println("NOTE: read-only file access is NOT detected on Windows (v1 limitation).")
	fmt.Println("      Only Write, Rename, and Remove events trigger alerts.")
	return nil
}
