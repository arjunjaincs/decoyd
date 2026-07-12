// watchlock.go — cross-platform singleton watcher lock.
//
// Rationale
// ---------
// Without this lock, a headless "decoyd watch" (systemd/scheduled-task) and
// the TUI's auto-started internal watcher can run simultaneously against the
// same deployed files with zero coordination.  Both see the same file event,
// each dispatch their own alert, each track rate-limits and debounce state
// independently — the user gets duplicate notifications and the rate-limiter
// gives the correct answer per-process but not globally.
//
// Mechanism
// ---------
// A file at <dataDir>/watcher.pid is the lock token.  It holds the PID of
// the running watcher as a decimal string.
//
// Acquire:
//  1. If the file does not exist, write our PID and hold the file open
//     (the held-open handle acts as a real OS-level lock on Windows, where
//     a second opener in exclusive mode will fail).
//  2. If the file exists, read the PID.
//     - If PID == our own PID (re-entrancy guard), succeed (no-op).
//     - If isProcessAlive(pid) returns true, return ErrWatcherRunning.
//     - Otherwise (stale lock — process is dead), overwrite and continue.
//
// Release:
//   - Close the held handle (releases the OS lock on Windows).
//   - Remove the file.  Callers (Stop) invoke the returned release func.
//   - On abnormal exit the file is left on disk; next start detects stale
//     and overwrites it (the isProcessAlive check handles this).
//
// Platform notes:
//   - Linux: signal(pid, 0) to check liveness (ESRCH → dead; nil/EPERM → alive).
//   - Windows: conservative (assume alive) — OS file locking via the held
//     open handle is the real enforcement mechanism.

package watch

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const watchLockFile = "watcher.pid"

// ErrWatcherRunning is returned when a second watcher tries to start while
// one is already running.  Callers should surface this as a clear message.
var ErrWatcherRunning = errors.New("watcher already running")

// acquireWatchLock tries to acquire the singleton watcher lock under dataDir.
//
// On success it returns a release function (call it in Stop) and a nil error.
// On failure it returns nil and ErrWatcherRunning (use errors.Is to check).
func acquireWatchLock(dataDir string) (release func(), err error) {
	lockPath := filepath.Join(dataDir, watchLockFile)
	myPID := os.Getpid()

	// ── Try atomic creation (O_CREATE|O_EXCL) ────────────────────────────
	// O_EXCL fails if the file already exists, making this atomic on all
	// supported platforms.  This is the fast path (no lock held by anyone).
	f, openErr := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304
	if openErr == nil {
		// File created exclusively — we own it.  Write our PID and return.
		if _, writeErr := fmt.Fprintf(f, "%d\n", myPID); writeErr != nil {
			_ = f.Close()
			_ = os.Remove(lockPath)
			return nil, fmt.Errorf("write watcher.pid: %w", writeErr)
		}
		release = func() {
			_ = f.Close()
			_ = os.Remove(lockPath)
		}
		return release, nil
	}

	// ── File already exists — check if the holder is alive ────────────────
	raw, readErr := os.ReadFile(lockPath) // #nosec G304
	if readErr != nil {
		// Can't read — assume contested and refuse.
		return nil, fmt.Errorf("%w: could not read %s: %v", ErrWatcherRunning, lockPath, readErr)
	}

	pidStr := strings.TrimSpace(string(raw))
	existingPID, parseErr := strconv.Atoi(pidStr)

	// If parse fails or the process is alive → refuse.
	if parseErr != nil || isProcessAlive(existingPID) {
		ownerHint := pidStr
		if parseErr != nil {
			ownerHint = "(unknown PID)"
		}
		return nil, fmt.Errorf(
			"%w: PID %s holds %s — stop that process first, or delete the file if it is stale",
			ErrWatcherRunning, ownerHint, lockPath,
		)
	}

	// ── Stale lock (dead process) — overwrite ─────────────────────────────
	f, openErr = os.OpenFile(lockPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) // #nosec G304
	if openErr != nil {
		return nil, fmt.Errorf("overwrite stale watcher.pid: %w", openErr)
	}
	if _, writeErr := fmt.Fprintf(f, "%d\n", myPID); writeErr != nil {
		_ = f.Close()
		_ = os.Remove(lockPath)
		return nil, fmt.Errorf("write stale watcher.pid: %w", writeErr)
	}
	release = func() {
		_ = f.Close()
		_ = os.Remove(lockPath)
	}
	return release, nil
}
