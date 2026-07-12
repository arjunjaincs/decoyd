package watch_test

// watch_lock_test.go — singleton watcher lock tests (Windows-native, no CGO).
//
// Tests in this file are separate from watch_test.go so they can be read as
// a complete, focused story: acquire → second acquire fails → release →
// acquire succeeds again.

import (
	"errors"
	"testing"

	"github.com/arjunjaincs/decoyd/internal/watch"
)

// TestWatchLock_SecondOpenerIsRefused verifies that two watcher instances
// with the same dataDir cannot both call Start() successfully.
// This is the core requirement for the singleton lock.
func TestWatchLock_SecondOpenerIsRefused(t *testing.T) {
	dir := t.TempDir()

	// First watcher — should start cleanly.
	w1, err := watch.New(nil, dir)
	if err != nil {
		t.Fatalf("New (w1): %v", err)
	}
	if err := w1.Start(); err != nil {
		t.Fatalf("w1.Start: %v", err)
	}
	defer w1.Stop()

	// Second watcher in the same dir — must be refused.
	w2, err := watch.New(nil, dir)
	if err != nil {
		t.Fatalf("New (w2): %v", err)
	}
	err = w2.Start()
	if err == nil {
		w2.Stop()
		t.Fatal("expected w2.Start to fail with ErrWatcherRunning, got nil")
	}
	if !errors.Is(err, watch.ErrWatcherRunning) {
		t.Errorf("expected ErrWatcherRunning, got: %v", err)
	}
	t.Logf("second opener correctly refused: %v", err)
}

// TestWatchLock_ReleaseAllowsReacquire verifies that stopping the first watcher
// removes the lock file so a new watcher can acquire it.
func TestWatchLock_ReleaseAllowsReacquire(t *testing.T) {
	dir := t.TempDir()

	w1, _ := watch.New(nil, dir)
	if err := w1.Start(); err != nil {
		t.Fatalf("w1.Start: %v", err)
	}

	// Stop w1 — must remove watcher.pid.
	w1.Stop()

	// A new watcher should now succeed.
	w2, _ := watch.New(nil, dir)
	if err := w2.Start(); err != nil {
		t.Fatalf("w2.Start after release: %v", err)
	}
	w2.Stop()
}

// TestWatchLock_StalePIDOverwritten verifies that a stale watcher.pid (PID
// that is no longer alive) is silently overwritten rather than causing an
// error.
func TestWatchLock_StalePIDOverwritten(t *testing.T) {
	dir := t.TempDir()

	// Write a PID file with a PID that is guaranteed never to be alive.
	// PID 2147483647 (math.MaxInt32) is safe — Linux and Windows both
	// cap PIDs well below this value so it is always a dead PID.
	watch.WriteTestPIDFile(t, dir, 2147483647)

	w, _ := watch.New(nil, dir)
	if err := w.Start(); err != nil {
		t.Fatalf("Start with stale PID should succeed, got: %v", err)
	}
	w.Stop()
}
