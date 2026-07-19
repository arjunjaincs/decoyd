// watchlock.go — singleton watcher lock using a PID file.
//
// # Design
//
// A single file <dataDir>/watcher.pid coordinates mutual exclusion between
// multiple decoyd processes (TUI + headless watcher).
//
// AcquireWatchLock attempts to create the PID file with O_CREATE|O_EXCL:
//
//   - If the file does not exist: creation succeeds, the current PID is
//     written, and a release function is returned.
//   - If the file exists and the holder is alive: ErrWatcherRunning is
//     returned with a message naming the PID and path.
//   - If the file exists but the holder is dead (stale lock): the file is
//     overwritten and the lock is taken.
//
// On Windows the held-open file handle (FILE_SHARE_NONE) acts as an
// additional guard: a second O_EXCL open fails immediately without
// checking PID liveness, so isProcessAlive is a secondary check used
// only when the O_EXCL open succeeds (i.e. we read a file that is NOT
// currently held open by another process).
//
// # Same-PID safety
//
// Two Watcher instances in the same process share the same OS PID.
// isProcessAlive(os.Getpid()) always returns true, so a second Start()
// in the same process is correctly refused by the O_EXCL path (file
// already exists + holder alive = ErrWatcherRunning), not incorrectly
// treated as "same PID is fine".  There is no special-case for same PID.
//
// # Wire-up
//
// Both linuxWatcher.start() and windowsWatcher.start() call AcquireWatchLock
// at the top before any inotify/fsnotify initialisation.  The returned
// release func is called by stop().
package watch

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const pidFileName = "watcher.pid"

// ErrWatcherRunning is returned by AcquireWatchLock when another live
// process already holds the lock.
var ErrWatcherRunning = errors.New("watcher already running")

// AcquireWatchLock attempts to acquire the singleton watcher lock.
//
// On success it returns a release function that removes the PID file.
// The caller MUST call release() when the watcher stops (typically deferred).
//
// On failure it returns ErrWatcherRunning (wrapping the human-readable
// message) or another error if the filesystem operation itself failed.
func AcquireWatchLock(dataDir string) (release func(), err error) {
	path := filepath.Join(dataDir, pidFileName)
	pid := os.Getpid()

	// --- attempt O_CREATE|O_EXCL: succeeds only if file does not exist ---
	f, createErr := openExclusive(path)
	if createErr == nil {
		// File did not exist — we own it.
		if werr := writePID(f, pid); werr != nil {
			_ = f.Close()
			_ = os.Remove(path)
			return nil, fmt.Errorf("write PID file: %w", werr)
		}
		_ = f.Close()
		return makeRelease(path), nil
	}

	if !isExistError(createErr) {
		// Unexpected filesystem error.
		return nil, fmt.Errorf("acquire watch lock: %w", createErr)
	}

	// --- file exists: read it and check liveness ---
	holderPID, readErr := readPID(path)
	if readErr != nil {
		// Can't read the file — treat as stale and overwrite.
		return overwriteLock(path, pid)
	}

	if !isProcessAlive(holderPID) {
		// Holder is dead — stale lock, overwrite.
		return overwriteLock(path, pid)
	}

	// Holder is alive — refuse.
	return nil, fmt.Errorf("%w: PID %d holds %s — stop that process first, or delete the file if it is stale",
		ErrWatcherRunning, holderPID, path)
}

// makeRelease returns a function that removes the PID file.
func makeRelease(path string) func() {
	return func() {
		_ = os.Remove(path)
	}
}

// overwriteLock writes the current PID to path (truncate) and returns release.
func overwriteLock(path string, pid int) (func(), error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) // #nosec G304 -- path is always filepath.Join(dataDir, pidFileName)
	if err != nil {
		return nil, fmt.Errorf("overwrite stale lock: %w", err)
	}
	defer f.Close()
	if err := writePID(f, pid); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("write PID to stale lock: %w", err)
	}
	return makeRelease(path), nil
}

// writePID writes pid as a decimal string to f.
func writePID(f *os.File, pid int) error {
	_, err := fmt.Fprintf(f, "%d\n", pid)
	return err
}

// readPID reads the PID from path, returning an error if parsing fails.
func readPID(path string) (int, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- path is always filepath.Join(dataDir, pidFileName)
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(s)
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("invalid PID in lock file: %q", s)
	}
	return pid, nil
}

// isExistError returns true when err indicates the file already exists.
func isExistError(err error) bool {
	return errors.Is(err, os.ErrExist)
}
