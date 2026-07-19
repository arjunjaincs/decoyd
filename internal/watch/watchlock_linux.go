//go:build linux

// watchlock_linux.go — Linux-specific lock file and process-liveness helpers.
package watch

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// openExclusive opens path with O_CREATE|O_EXCL (atomic creation).
// Returns the file on success, or os.ErrExist-wrapping error if the file
// already exists.
func openExclusive(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 -- path is always filepath.Join(dataDir, pidFileName)
}

// isProcessAlive reports whether pid is a live process reachable by the
// current user, using signal 0 (existence probe):
//
//   - nil error: process exists and we have permission to signal it → alive
//   - EPERM:     process exists but we can't signal it → alive
//   - ESRCH:     no such process → dead / stale
//   - other:     treat as alive (fail safe)
func isProcessAlive(pid int) bool {
	err := unix.Kill(pid, 0)
	if err == nil {
		return true // exists and we can signal it
	}
	if errors.Is(err, unix.EPERM) {
		return true // exists but owned by another user
	}
	// ESRCH or anything else: not alive.
	return false
}
