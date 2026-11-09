// Package filelock provides advisory file locking for Decoyd.
//
// Lock acquires an exclusive advisory lock on the named lock file, creating
// it if necessary. The lock is released by calling the returned Unlock
// function or when the process exits.
//
// Platform implementations:
//   - Linux: fcntl(2) flock via golang.org/x/sys/unix.Flock
//   - Windows: LockFileEx via golang.org/x/sys/windows
//
// The lock file is separate from the data file so the data file can be
// opened with O_APPEND or O_TRUNC while the lock is held.
package filelock

import (
	"fmt"
	"os"
)

// LockFile holds an open lock file descriptor. Call Unlock to release.
type LockFile struct {
	f *os.File
}

// Lock acquires an exclusive advisory lock on path+".lock", creating it
// if it does not exist. Returns a LockFile whose Unlock method releases
// the lock and closes the file.
func Lock(path string) (*LockFile, error) {
	lockPath := path + ".lock"
	// #nosec G304 -- lockPath is always a derivative of an internal data-dir path
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("filelock open %s: %w", lockPath, err)
	}
	if err := lockExclusive(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("filelock acquire %s: %w", lockPath, err)
	}
	return &LockFile{f: f}, nil
}

// Unlock releases the advisory lock and closes the lock file.
// It is safe to call Unlock more than once; subsequent calls are no-ops.
func (lf *LockFile) Unlock() {
	if lf == nil || lf.f == nil {
		return
	}
	_ = unlockAndClose(lf.f)
	lf.f = nil
}
