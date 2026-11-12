//go:build linux

// watch_parentdir_test.go — B1 parent-dir inotify watch tests.
// These 5 tests verify that newly-deployed token files are detected via
// parent-directory IN_CREATE/IN_MOVED_TO watches without requiring Reload().
package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/arjunjaincs/decoyd/internal/triglog"
)

// helper: create a watcher with fast debounce for test speed.
func makeTestWatcher(t *testing.T, dir string) *linuxWatcher {
	t.Helper()
	w, err := newPlatformImpl(nil, dir)
	if err != nil {
		t.Fatalf("newPlatformImpl: %v", err)
	}
	lw := w.(*linuxWatcher)
	lw.cfg = WatcherConfig{DebounceDuration: 50 * time.Millisecond, RateLimit: 100}
	return lw
}

// helper: wait up to deadline for a tokenID to appear in triglog.
func waitForTriglog(t *testing.T, dir, tokenID string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		events, err := triglog.Load(dir)
		if err != nil {
			t.Fatalf("triglog.Load: %v", err)
		}
		for _, ev := range events {
			if ev.TokenID == tokenID {
				return true
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

// (a) TestWatch_NewTokenPickedUpByParentDirWatch
// Start watcher with ONE token file; then deploy a SECOND token file into the
// same watched directory AFTER the watcher starts (without calling Reload()).
// Assert that within 2s the watcher detected the new file and fires an alert.
func TestWatch_NewTokenPickedUpByParentDirWatch(t *testing.T) {
	dir := t.TempDir()
	tokenDir := filepath.Join(dir, "tokens")
	if err := os.MkdirAll(tokenDir, 0o700); err != nil {
		t.Fatal(err)
	}

	// Deploy token #1 before starting the watcher.
	decoy1 := filepath.Join(tokenDir, "creds1.txt")
	if err := os.WriteFile(decoy1, []byte("secret1"), 0o600); err != nil {
		t.Fatal(err)
	}
	snap := []DeployedToken{
		{ID: "parentdir0000001", Type: "aws_credentials", DeployedPath: decoy1},
		// Pre-declare token #2 in snapshot so watcher knows the directory to watch.
		{ID: "parentdir0000002", Type: "aws_credentials",
			DeployedPath: filepath.Join(tokenDir, "creds2.txt")},
	}
	if err := WriteDeployedSnapshot(dir, snap); err != nil {
		t.Fatal(err)
	}

	lw := makeTestWatcher(t, dir)
	if err := lw.start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { lw.stop() })

	// Let inotify watches register.
	time.Sleep(200 * time.Millisecond)

	// Deploy token #2 AFTER watcher started — no Reload() call.
	decoy2 := filepath.Join(tokenDir, "creds2.txt")
	if err := os.WriteFile(decoy2, []byte("secret2"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Wait for the watcher to pick up the new file and add a watch.
	time.Sleep(300 * time.Millisecond)

	// Trigger the new file.
	f, err := os.Open(decoy2)
	if err != nil {
		t.Fatalf("open decoy2: %v", err)
	}
	_ = f.Close()

	if !waitForTriglog(t, dir, "parentdir0000002", 2*time.Second) {
		t.Error("token deployed after watcher start was not detected via parent-dir watch within 2s")
	}
}

// (b) TestWatch_HeadlessParentDirWatch
// Same as (a) but watcher created in headless mode (st == nil, snapshot-based).
// This is the same code path but verifies headless mode specifically.
func TestWatch_HeadlessParentDirWatch(t *testing.T) {
	dir := t.TempDir()

	decoy := filepath.Join(dir, "headless_tok.txt")
	snap := []DeployedToken{
		{ID: "headlessdir0001", Type: "aws_credentials", DeployedPath: decoy},
	}
	if err := WriteDeployedSnapshot(dir, snap); err != nil {
		t.Fatal(err)
	}

	// headless mode: st == nil
	lw := makeTestWatcher(t, dir)
	if err := lw.start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { lw.stop() })

	time.Sleep(200 * time.Millisecond)

	// Create the file AFTER watcher start (file didn't exist at start time).
	if err := os.WriteFile(decoy, []byte("headless-secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Wait for parent-dir watch to detect and add a file watch.
	time.Sleep(300 * time.Millisecond)

	f, err := os.Open(decoy)
	if err != nil {
		t.Fatalf("open decoy: %v", err)
	}
	_ = f.Close()

	if !waitForTriglog(t, dir, "headlessdir0001", 2*time.Second) {
		t.Error("headless watcher did not detect file created after start via parent-dir watch")
	}
}

// (c) TestWatch_DeleteRecreateTriggerAlert
// Deploy token, start watcher. Delete the token file. Recreate it (same path,
// different content). Assert alert fires within 2s of recreation.
func TestWatch_DeleteRecreateTriggerAlert(t *testing.T) {
	dir := t.TempDir()

	decoy := filepath.Join(dir, "deleteme.txt")
	if err := os.WriteFile(decoy, []byte("original-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	snap := []DeployedToken{
		{ID: "deleterecreate01", Type: "aws_credentials", DeployedPath: decoy},
	}
	if err := WriteDeployedSnapshot(dir, snap); err != nil {
		t.Fatal(err)
	}

	lw := makeTestWatcher(t, dir)
	if err := lw.start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { lw.stop() })

	time.Sleep(200 * time.Millisecond)

	// Delete the token file.
	if err := os.Remove(decoy); err != nil {
		t.Fatal(err)
	}

	time.Sleep(100 * time.Millisecond)

	// Recreate it (same path, different content — simulates atomic deploy).
	if err := os.WriteFile(decoy, []byte("new-secret-after-recreate"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Wait for parent-dir watch to pick up the new file.
	time.Sleep(300 * time.Millisecond)

	// Trigger the recreated file.
	f, err := os.Open(decoy)
	if err != nil {
		t.Fatalf("open recreated decoy: %v", err)
	}
	_ = f.Close()

	if !waitForTriglog(t, dir, "deleterecreate01", 2*time.Second) {
		t.Error("recreated token file was not detected after delete+recreate within 2s")
	}
}

// (d) TestWatch_AtomicRenameDetectedWithin1s
// Deploy token to path P. Write a tmp file, rename (os.Rename) it to P.
// Assert alert fires within 1s.
func TestWatch_AtomicRenameDetectedWithin1s(t *testing.T) {
	dir := t.TempDir()

	decoy := filepath.Join(dir, "atomic_tok.txt")
	if err := os.WriteFile(decoy, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	snap := []DeployedToken{
		{ID: "atomicrenameid01", Type: "aws_credentials", DeployedPath: decoy},
	}
	if err := WriteDeployedSnapshot(dir, snap); err != nil {
		t.Fatal(err)
	}

	lw := makeTestWatcher(t, dir)
	if err := lw.start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { lw.stop() })

	time.Sleep(200 * time.Millisecond)

	// Atomic replace via tmp+rename.
	tmpPath := decoy + ".tmp"
	if err := os.WriteFile(tmpPath, []byte("new-secret-atomic"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmpPath, decoy); err != nil {
		t.Fatal(err)
	}

	// Wait for parent-dir watch to detect IN_MOVED_TO and add file watch.
	time.Sleep(300 * time.Millisecond)

	// Trigger the renamed file.
	f, err := os.Open(decoy)
	if err != nil {
		t.Fatalf("open renamed decoy: %v", err)
	}
	_ = f.Close()

	if !waitForTriglog(t, dir, "atomicrenameid01", 1*time.Second) {
		t.Error("atomic rename (IN_MOVED_TO) was not detected within 1s")
	}
}

// (e) TestWatch_RemovedTokenNoLongerAlerts
// Deploy token, start watcher. Remove the file from the snapshot (simulating
// purge). After purge, write to the path and assert NO new alert fires within 1s.
func TestWatch_RemovedTokenNoLongerAlerts(t *testing.T) {
	dir := t.TempDir()

	decoy := filepath.Join(dir, "purge_me.txt")
	if err := os.WriteFile(decoy, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	snap := []DeployedToken{
		{ID: "purgedtokenid001", Type: "aws_credentials", DeployedPath: decoy},
	}
	if err := WriteDeployedSnapshot(dir, snap); err != nil {
		t.Fatal(err)
	}

	lw := makeTestWatcher(t, dir)
	if err := lw.start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { lw.stop() })

	time.Sleep(200 * time.Millisecond)

	// Remove the token from the snapshot and reload (simulate purge).
	if err := WriteDeployedSnapshot(dir, []DeployedToken{}); err != nil {
		t.Fatal(err)
	}
	if err := lw.reload(); err != nil {
		t.Fatalf("reload after purge: %v", err)
	}

	// Also delete the file and recreate (to trigger IN_CREATE in dir watch).
	if err := os.Remove(decoy); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(decoy, []byte("new-content"), 0o600); err != nil {
		t.Fatal(err)
	}

	time.Sleep(300 * time.Millisecond)

	// Open the file — should NOT trigger an alert since token was purged.
	f, err := os.Open(decoy)
	if err == nil {
		_ = f.Close()
	}

	time.Sleep(300 * time.Millisecond)

	// Verify no NEW alert for the purged token.
	events, err := triglog.Load(dir)
	if err != nil {
		t.Fatalf("triglog.Load: %v", err)
	}
	for _, ev := range events {
		if ev.TokenID == "purgedtokenid001" {
			t.Errorf("purged token still generated alert event: %+v", ev)
		}
	}
}
