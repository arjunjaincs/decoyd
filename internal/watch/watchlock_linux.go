//go:build linux

package watch

import (
	"errors"
	"os"
	"syscall"
)

// isProcessAlive returns true if a process with the given PID is running.
// On Linux we send signal 0 to the PID: this requires no privileges and
// returns an error only if the PID does not exist or we have no permission.
// "No permission" (EPERM) means the process EXISTS but we can't signal it —
// that still means it's alive.
func isProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true // signal delivered — process is alive
	}
	if errors.Is(err, syscall.EPERM) {
		return true // process exists, we just lack permission to signal it
	}
	return false // ESRCH or other — process is gone
}
