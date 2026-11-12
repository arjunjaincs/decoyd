//go:build windows

package filelock

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockExclusive acquires an exclusive LockFileEx on f. Blocks until
// the lock is available.
func lockExclusive(f *os.File) error {
	var ol windows.Overlapped
	return windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &ol)
}

// unlockAndClose releases the LockFileEx and closes the file.
func unlockAndClose(f *os.File) error {
	var ol windows.Overlapped
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &ol)
	return f.Close()
}
