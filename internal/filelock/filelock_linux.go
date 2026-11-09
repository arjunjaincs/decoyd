//go:build linux

package filelock

import (
	"os"

	"golang.org/x/sys/unix"
)

// lockExclusive acquires an exclusive flock on f. Blocks until the lock
// is available.
func lockExclusive(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX)
}

// unlockAndClose releases the flock and closes the file.
func unlockAndClose(f *os.File) error {
	_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
	return f.Close()
}
