//go:build windows

// watchlock_windows.go — Windows-specific lock file and process-liveness helpers.
//
// On Windows, O_CREATE|O_EXCL via os.OpenFile maps to CreateFile with
// CREATE_NEW disposition, which fails with ERROR_FILE_EXISTS if the file
// exists — regardless of whether another process has it open.
//
// For process-liveness we use OpenProcess(SYNCHRONIZE, false, pid):
//   - Success: process exists (may be a zombie waiting for parent wait,
//     but still "alive" for our purposes).
//   - ERROR_INVALID_PARAMETER or ERROR_NOT_FOUND: no such process → dead.
//   - ERROR_ACCESS_DENIED: process exists but we can't open it → alive.
package watch

import (
	"os"

	"golang.org/x/sys/windows"
)

// openExclusive opens path with O_CREATE|O_EXCL (atomic creation).
// Returns os.ErrExist-wrapping error if the file already exists.
func openExclusive(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 -- path is always filepath.Join(dataDir, pidFileName)
}

// isProcessAlive reports whether pid is a live process on Windows.
// Uses OpenProcess(SYNCHRONIZE) as a non-destructive existence probe.
func isProcessAlive(pid int) bool {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid)) // #nosec G115 -- pid is always a valid process ID from os.Getpid() or file; fits uint32
	if err != nil {
		// ERROR_INVALID_PARAMETER / ERROR_NOT_FOUND: process gone.
		// Any other error (incl. ERROR_ACCESS_DENIED): process exists.
		if err == windows.ERROR_INVALID_PARAMETER || err == windows.ERROR_NOT_FOUND {
			return false
		}
		// Access denied or other: process exists, treat as alive.
		return true
	}
	_ = windows.CloseHandle(handle)
	return true
}
