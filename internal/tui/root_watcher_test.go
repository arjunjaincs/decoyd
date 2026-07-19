// root_watcher_test.go — tests for the TUI-embedded watcher auto-start wiring.
package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/arjunjaincs/decoyd/internal/watch"
)

// TestStartWatcherCmd_NilStoreReturnsNilWatcher verifies that when the store
// is nil (test mode), startWatcherCmd returns a watcherStartedMsg with nil
// watcher rather than panicking or blocking.
func TestStartWatcherCmd_NilStoreReturnsNilWatcher(t *testing.T) {
	root := newTestRoot(false) // nil store
	cmd := root.startWatcherCmd()
	if cmd == nil {
		// nil store path returns nil cmd — no watcher attempted
		return
	}
	msg := cmd()
	wsm, ok := msg.(watcherStartedMsg)
	if !ok {
		t.Fatalf("expected watcherStartedMsg, got %T", msg)
	}
	if wsm.w != nil {
		t.Errorf("expected nil watcher with nil store, got non-nil")
	}
}

// TestStartWatcherCmd_AlreadyRunningIsNoop verifies that if m.watcher is
// already set, startWatcherCmd returns nil (no-op) and does NOT attempt to
// start a second watcher.
func TestStartWatcherCmd_AlreadyRunningIsNoop(t *testing.T) {
	root := newTestRoot(false)
	// Simulate a watcher already set (use a real started watcher so we can
	// stop it cleanly; the lock uses a temp dataDir to avoid conflicts).
	dir := t.TempDir()
	w, err := watch.New(nil, dir)
	if err != nil {
		t.Fatalf("watch.New: %v", err)
	}
	if err := w.Start(); err != nil {
		t.Fatalf("w.Start: %v", err)
	}
	t.Cleanup(w.Stop)

	root.watcher = w
	cmd := root.startWatcherCmd()
	if cmd != nil {
		t.Errorf("startWatcherCmd should return nil when watcher already set; got non-nil cmd")
	}
}

// TestWatcherStartedMsg_StoresWatcher verifies that watcherStartedMsg with a
// non-nil watcher is correctly stored in m.watcher and propagated to the
// status screen.
func TestWatcherStartedMsg_StoresWatcher(t *testing.T) {
	root := newTestRoot(false)
	dir := t.TempDir()
	w, err := watch.New(nil, dir)
	if err != nil {
		t.Fatalf("watch.New: %v", err)
	}
	if err := w.Start(); err != nil {
		t.Fatalf("w.Start: %v", err)
	}
	t.Cleanup(w.Stop)

	updated, _ := root.Update(watcherStartedMsg{w: w})
	m := updated.(RootModel)
	if m.watcher != w {
		t.Errorf("m.watcher not set after watcherStartedMsg")
	}
}

// TestWatcherStartedMsg_NilWatcherDoesNotPanic verifies that a watcherStartedMsg
// with nil watcher (graceful-degrade case) is handled without panic.
func TestWatcherStartedMsg_NilWatcherDoesNotPanic(t *testing.T) {
	root := newTestRoot(false)
	updated, _ := root.Update(watcherStartedMsg{w: nil})
	m := updated.(RootModel)
	if m.watcher != nil {
		t.Errorf("m.watcher should be nil after nil watcherStartedMsg")
	}
}

// TestWatcherLockGraceDegrade_ErrWatcherRunning verifies that when the watcher
// lock is already held (same dataDir, live process), a second Start returns
// ErrWatcherRunning — which is the signal startWatcherCmd uses to degrade gracefully.
func TestWatcherLockGraceDegrade_ErrWatcherRunning(t *testing.T) {
	dir := t.TempDir()

	// Acquire the lock by starting the first watcher.
	w1, err := watch.New(nil, dir)
	if err != nil {
		t.Fatalf("watch.New w1: %v", err)
	}
	if err := w1.Start(); err != nil {
		t.Fatalf("w1.Start: %v", err)
	}
	defer w1.Stop()

	// Try to start a second watcher in the same dir.
	// This must fail with ErrWatcherRunning because w1 holds the lock and
	// the current process PID is alive (isProcessAlive returns true).
	w2, err2 := watch.New(nil, dir)
	if err2 != nil {
		t.Fatalf("watch.New w2: %v", err2)
	}
	startErr := w2.Start()
	if startErr == nil {
		w2.Stop()
		t.Errorf("expected ErrWatcherRunning, got nil")
	}
}


// TestQuitStopsWatcher verifies that a ctrl+c key stops the embedded watcher
// before returning tea.Quit, so the PID file is cleaned up.
func TestQuitStopsWatcher(t *testing.T) {
	dir := t.TempDir()
	w, err := watch.New(nil, dir)
	if err != nil {
		t.Fatalf("watch.New: %v", err)
	}
	if err := w.Start(); err != nil {
		t.Fatalf("w.Start: %v", err)
	}
	// Do NOT defer Stop — the ctrl+c handler should stop it.

	root := newTestRoot(false)
	root.dataDir = dir
	root.watcher = w

	_, cmd := root.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	// After ctrl+c, watcher.Stop() should have been called — Status().Running == false.
	if w.Status().Running {
		w.Stop() // cleanup
		t.Errorf("expected watcher to be stopped after ctrl+c")
	}
	if cmd == nil {
		t.Errorf("expected tea.Quit cmd after ctrl+c")
	}
}
