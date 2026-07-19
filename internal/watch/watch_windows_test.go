//go:build windows

// watch_windows_test.go — Windows-native integration tests using real fsnotify.
//
// These tests run only on Windows where watch_windows.go is active.
// They test write/rename/delete event detection; pure read-only access is
// NOT tested because it is undetectable on Windows without ETW (documented
// v1 limitation — see package comment in watch_windows.go).
package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/arjunjaincs/decoyd/internal/triglog"
)

// TestWindowsWatcher_Integration_Write deploys a file, starts the watcher,
// writes to the file, and asserts a trigger event appears in triglog.
func TestWindowsWatcher_Integration_Write(t *testing.T) {
	dir := t.TempDir()
	decoyPath := filepath.Join(dir, "credentials")
	if err := os.WriteFile(decoyPath, []byte("fake-aws-key"), 0o600); err != nil {
		t.Fatalf("create decoy file: %v", err)
	}

	snap := []DeployedToken{
		{ID: "winintegtest0000", Type: "aws_credentials", DeployedPath: decoyPath},
	}
	if err := WriteDeployedSnapshot(dir, snap); err != nil {
		t.Fatalf("WriteDeployedSnapshot: %v", err)
	}

	w, err := newPlatformImpl(nil, dir)
	if err != nil {
		t.Fatalf("newPlatformImpl: %v", err)
	}
	ww := w.(*windowsWatcher)
	ww.cfg = WatcherConfig{
		DebounceDuration: 100 * time.Millisecond,
		RateLimit:        100,
	}

	if err := ww.start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { ww.stop() })

	st := ww.status()
	if !st.Running {
		t.Fatal("expected watcher to be running after start()")
	}
	if st.Watching != 1 {
		t.Fatalf("expected 1 watched path, got %d", st.Watching)
	}

	// Trigger: write to the file.
	if err := os.WriteFile(decoyPath, []byte("modified"), 0o600); err != nil {
		t.Fatalf("write trigger: %v", err)
	}

	// Wait up to 3 seconds for debounce + dispatch.
	deadline := time.Now().Add(3 * time.Second)
	var found bool
	for time.Now().Before(deadline) {
		events, err := triglog.Load(dir)
		if err != nil {
			t.Fatalf("triglog.Load: %v", err)
		}
		for _, ev := range events {
			if ev.TokenID == "winintegtest0000" {
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
		t.Error("trigger event did not appear in triglog within 3s after write")
	}
}

// TestWindowsWatcher_Integration_Delete verifies that deleting the decoy file
// fires a trigger event.
func TestWindowsWatcher_Integration_Delete(t *testing.T) {
	dir := t.TempDir()
	decoyPath := filepath.Join(dir, "id_rsa")
	if err := os.WriteFile(decoyPath, []byte("fake-key"), 0o600); err != nil {
		t.Fatalf("create: %v", err)
	}

	snap := []DeployedToken{
		{ID: "windeletetest000", Type: "ssh_key", DeployedPath: decoyPath},
	}
	if err := WriteDeployedSnapshot(dir, snap); err != nil {
		t.Fatal(err)
	}

	w, err := newPlatformImpl(nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	ww := w.(*windowsWatcher)
	ww.cfg = WatcherConfig{
		DebounceDuration: 100 * time.Millisecond,
		RateLimit:        100,
	}
	if err := ww.start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ww.stop() })

	// Trigger: delete the file.
	if err := os.Remove(decoyPath); err != nil {
		t.Fatalf("remove: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	var found bool
	for time.Now().Before(deadline) {
		events, err := triglog.Load(dir)
		if err != nil {
			t.Fatalf("triglog.Load: %v", err)
		}
		for _, ev := range events {
			if ev.TokenID == "windeletetest000" {
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
		t.Error("trigger event did not appear in triglog within 3s after delete")
	}
}

// TestWindowsWatcher_Stop verifies that stop() exits cleanly and sets running=false.
func TestWindowsWatcher_Stop(t *testing.T) {
	dir := t.TempDir()
	snap := []DeployedToken{}
	if err := WriteDeployedSnapshot(dir, snap); err != nil {
		t.Fatal(err)
	}

	w, err := newPlatformImpl(nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	ww := w.(*windowsWatcher)
	if err := ww.start(); err != nil {
		t.Fatal(err)
	}
	if !ww.status().Running {
		t.Fatal("expected running after start")
	}
	ww.stop()
	if ww.status().Running {
		t.Fatal("expected not running after stop")
	}
}
