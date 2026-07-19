// watchlock_test.go — tests for AcquireWatchLock (all platforms).
//
// Same-PID safety: tests in the same process share os.Getpid(). This
// means TestWatchLock_SecondOpenerIsRefused must NOT rely on PID
// uniqueness between the two Watcher instances — they share a PID.
// The O_EXCL path handles this correctly: file exists + holder alive
// (isProcessAlive(os.Getpid()) == true) → ErrWatcherRunning, regardless
// of whether the holder happens to be the same process.
package watch

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestWatchLock_SecondOpenerIsRefused verifies that a second Start() on
// the same dataDir returns ErrWatcherRunning.
//
// Both watcher instances run in the same process and therefore share a PID.
// The lock must still be refused — same-PID is not a valid exemption.
func TestWatchLock_SecondOpenerIsRefused(t *testing.T) {
	dir := t.TempDir()

	// Write an empty snapshot so loadTokens() doesn't error.
	if err := WriteDeployedSnapshot(dir, nil); err != nil {
		t.Fatalf("WriteDeployedSnapshot: %v", err)
	}

	// First watcher acquires the lock.
	w1, err := newPlatformImpl(nil, dir)
	if err != nil {
		t.Fatalf("newPlatformImpl w1: %v", err)
	}
	if err := w1.start(); err != nil {
		t.Fatalf("w1.start(): %v", err)
	}
	defer w1.stop()

	// Second watcher on the same dataDir must be refused.
	w2, err := newPlatformImpl(nil, dir)
	if err != nil {
		t.Fatalf("newPlatformImpl w2: %v", err)
	}
	err = w2.start()
	if err == nil {
		w2.stop()
		t.Fatal("w2.start() succeeded; expected ErrWatcherRunning")
	}
	if !errors.Is(err, ErrWatcherRunning) {
		t.Fatalf("w2.start() returned wrong error type: %v (want ErrWatcherRunning)", err)
	}
}

// TestWatchLock_ReleaseAllowsReacquire verifies that after the first watcher
// stops, a new watcher can acquire the lock on the same dataDir.
func TestWatchLock_ReleaseAllowsReacquire(t *testing.T) {
	dir := t.TempDir()
	if err := WriteDeployedSnapshot(dir, nil); err != nil {
		t.Fatalf("WriteDeployedSnapshot: %v", err)
	}

	// First watcher acquires and then releases.
	w1, err := newPlatformImpl(nil, dir)
	if err != nil {
		t.Fatalf("newPlatformImpl w1: %v", err)
	}
	if err := w1.start(); err != nil {
		t.Fatalf("w1.start(): %v", err)
	}
	w1.stop()

	// PID file must be gone after stop().
	pidPath := filepath.Join(dir, pidFileName)
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Error("watcher.pid still exists after stop() — release not called")
	}

	// Second watcher on the same dir must now succeed.
	w2, err := newPlatformImpl(nil, dir)
	if err != nil {
		t.Fatalf("newPlatformImpl w2: %v", err)
	}
	if err := w2.start(); err != nil {
		t.Fatalf("w2.start() after release failed: %v", err)
	}
	w2.stop()
}

// TestWatchLock_StalePIDOverwritten verifies that a PID file containing a
// guaranteed-dead PID is treated as stale and overwritten (not refused).
//
// PID 2147483647 (max int32) is used as the "dead" PID. On both Linux and
// Windows, this PID is virtually never allocated in practice (PID_MAX_DEFAULT
// on Linux is 32768; Windows PID ceiling is 4194304 for 64-bit). Even in the
// unlikely event it exists, isProcessAlive on a process we don't own returns
// true — but the test verifies the logic works for a clearly unreachable PID.
//
// To make this test deterministic, we write the stale PID directly to the
// file (bypassing AcquireWatchLock) and then call AcquireWatchLock, which
// must detect the PID is dead and overwrite.
func TestWatchLock_StalePIDOverwritten(t *testing.T) {
	dir := t.TempDir()

	// Write a PID file with a guaranteed-dead PID directly (bypass the lock).
	pidPath := filepath.Join(dir, pidFileName)
	stalePID := 2147483647 // max int32; virtually guaranteed to not exist
	if err := os.WriteFile(pidPath, []byte("2147483647\n"), 0o600); err != nil {
		t.Fatalf("write stale PID file: %v", err)
	}

	// Confirm our test assumption: isProcessAlive(stalePID) must return false
	// for this test to be meaningful.
	if isProcessAlive(stalePID) {
		t.Skip("PID 2147483647 is alive on this machine — test assumption invalid, skipping")
	}

	// AcquireWatchLock must overwrite the stale file and return a release func.
	if err := WriteDeployedSnapshot(dir, nil); err != nil {
		t.Fatal(err)
	}
	release, err := AcquireWatchLock(dir)
	if err != nil {
		t.Fatalf("AcquireWatchLock with stale PID returned error: %v", err)
	}
	defer release()

	// The PID file now contains our PID, not the stale one.
	data, readErr := os.ReadFile(pidPath)
	if readErr != nil {
		t.Fatalf("read PID file after acquire: %v", readErr)
	}
	content := string(data)
	if content == "2147483647\n" {
		t.Error("PID file still contains the stale PID — overwrite did not happen")
	}
}
