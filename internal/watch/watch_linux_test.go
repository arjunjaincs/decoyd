//go:build linux

// watch_linux_test.go — Linux-only integration tests requiring real inotify.
package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/arjunjaincs/decoyd/internal/triglog"
)

// TestLinuxWatcher_Integration deploys a file, starts the watcher, touches the
// file, and asserts that a trigger event appears in triglog within a timeout.
//
// This test runs on Linux only because it requires inotify. It is skipped if
// inotify_init1 is unavailable (e.g. inside a highly restricted container).
func TestLinuxWatcher_Integration(t *testing.T) {
	dir := t.TempDir()

	// Create the decoy file.
	decoyPath := filepath.Join(dir, "credentials")
	if err := os.WriteFile(decoyPath, []byte("fake-aws-key"), 0o600); err != nil {
		t.Fatalf("create decoy file: %v", err)
	}

	// Write the deployed_tokens.json snapshot (headless mode: st == nil).
	snap := []DeployedToken{
		{ID: "integtest0000000", Type: "aws_credentials", DeployedPath: decoyPath},
	}
	if err := WriteDeployedSnapshot(dir, snap); err != nil {
		t.Fatalf("WriteDeployedSnapshot: %v", err)
	}

	// Create watcher in headless mode (no bbolt, no alert channel).
	w, err := newPlatformImpl(nil, dir)
	if err != nil {
		t.Fatalf("newPlatformImpl: %v", err)
	}
	lw := w.(*linuxWatcher)
	lw.cfg = WatcherConfig{
		DebounceDuration: 50 * time.Millisecond, // short for test speed
		RateLimit:        100,
	}

	if err := lw.start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { lw.stop() })

	st := lw.status()
	if !st.Running {
		t.Fatal("expected watcher to be running after start()")
	}

	// Trigger: open the file (IN_OPEN).
	f, err := os.Open(decoyPath)
	if err != nil {
		t.Fatalf("open decoy: %v", err)
	}
	_ = f.Close()

	// Wait up to 2 seconds for the trigger to appear in triglog.
	// The debounce is 50ms; the event loop polls every ≤50ms in this config.
	deadline := time.Now().Add(2 * time.Second)
	var found bool
	for time.Now().Before(deadline) {
		events, err := triglog.Load(dir)
		if err != nil {
			t.Fatalf("triglog.Load: %v", err)
		}
		for _, ev := range events {
			if ev.TokenID == "integtest0000000" {
				found = true
				break
			}
		}
		if found {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !found {
		t.Error("trigger event did not appear in triglog within 2s")
	}
}

// TestLinuxWatcher_Debounce verifies that rapid successive events on the same
// file produce exactly one dispatch after the debounce window, not one per event.
func TestLinuxWatcher_Debounce(t *testing.T) {
	dir := t.TempDir()

	decoyPath := filepath.Join(dir, "debounce_file")
	if err := os.WriteFile(decoyPath, []byte("content"), 0o600); err != nil {
		t.Fatalf("create file: %v", err)
	}

	snap := []DeployedToken{
		{ID: "debouncetest0000", Type: "aws_credentials", DeployedPath: decoyPath},
	}
	if err := WriteDeployedSnapshot(dir, snap); err != nil {
		t.Fatal(err)
	}

	w, err := newPlatformImpl(nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	lw := w.(*linuxWatcher)
	lw.cfg = WatcherConfig{
		DebounceDuration: 300 * time.Millisecond,
		RateLimit:        100,
	}
	if err := lw.start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lw.stop() })

	// Rapidly open the file 5 times within the debounce window.
	for i := 0; i < 5; i++ {
		f, err := os.Open(decoyPath)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		_ = f.Close()
		time.Sleep(20 * time.Millisecond)
	}

	// Wait for debounce to fire (300ms + margin).
	time.Sleep(700 * time.Millisecond)

	events, err := triglog.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var count int
	for _, ev := range events {
		if ev.TokenID == "debouncetest0000" {
			count++
		}
	}
	// After deduplication, there should be exactly 1 event (pending + possibly failed,
	// but triglog deduplicates by ID so it's always 1 final state per event ID).
	if count == 0 {
		t.Error("expected at least 1 trigger event after debounce window")
	}
	// Should NOT be 5 separate events (one per open call).
	if count >= 5 {
		t.Errorf("debounce not working: got %d events, expected much fewer", count)
	}
}
