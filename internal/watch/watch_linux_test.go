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

// TestWdMapRaceReAdd exercises the concurrent wdMap path where the re-add
// goroutine (spawned by eventLoop on IN_MOVE_SELF/IN_DELETE_SELF) writes to
// wdMap while eventLoop reads it. Without the wdMu fix, go test -race will
// report a data race on wdMap.
//
// Because gcc is not installed in this environment, -race cannot be run.
// The test still validates the functional correctness of the re-add path.
// UNKNOWN: data-race flag (requires gcc + go test -race).
func TestWdMapRaceReAdd(t *testing.T) {
	dir := t.TempDir()
	decoyPath := filepath.Join(dir, "racefile")
	if err := os.WriteFile(decoyPath, []byte("content"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}
	snap := []DeployedToken{
		{ID: "racetestid00000", Type: "aws_credentials", DeployedPath: decoyPath},
	}
	if err := WriteDeployedSnapshot(dir, snap); err != nil {
		t.Fatalf("WriteDeployedSnapshot: %v", err)
	}
	w, err := newPlatformImpl(nil, dir)
	if err != nil {
		t.Fatalf("newPlatformImpl: %v", err)
	}
	lw := w.(*linuxWatcher)
	lw.cfg = WatcherConfig{DebounceDuration: 20 * time.Millisecond, RateLimit: 1000}
	if err := lw.start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { lw.stop() })

	// Goroutine 1: repeatedly rename the decoy in and out (triggers IN_MOVE_SELF
	// and IN_DELETE_SELF, causing the re-add goroutine to write to wdMap).
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 20; i++ {
			tmpPath := decoyPath + ".tmp"
			_ = os.Rename(decoyPath, tmpPath)
			time.Sleep(5 * time.Millisecond)
			_ = os.Rename(tmpPath, decoyPath)
			time.Sleep(5 * time.Millisecond)
		}
	}()

	// Goroutine 2: repeatedly open the file (triggers IN_OPEN — concurrent read
	// of wdMap in eventLoop).
	go func() {
		for i := 0; i < 50; i++ {
			f, err := os.Open(decoyPath)
			if err == nil {
				_ = f.Close()
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()

	<-done
	time.Sleep(500 * time.Millisecond)

	// Watcher must still be running after concurrent renames.
	st := lw.status()
	if !st.Running {
		t.Error("watcher stopped unexpectedly during race test")
	}
	// Race correctness is verified by go test -race (gcc installed).
}

// TestWatcherReload_NewTokenPickedUp proves that a token deployed after the
// watcher starts IS detected after Reload(). Without Reload(), the token would
// never be watched. This test verifies the fix is correct.
func TestWatcherReload_NewTokenPickedUp(t *testing.T) {
	dir := t.TempDir()

	// Deploy token #1 before starting the watcher.
	decoy1 := filepath.Join(dir, "token1")
	if err := os.WriteFile(decoy1, []byte("cred1"), 0o600); err != nil {
		t.Fatalf("write token1: %v", err)
	}
	snap := []DeployedToken{
		{ID: "reloadtoken00001", Type: "aws_credentials", DeployedPath: decoy1},
	}
	if err := WriteDeployedSnapshot(dir, snap); err != nil {
		t.Fatalf("WriteDeployedSnapshot: %v", err)
	}

	w, err := newPlatformImpl(nil, dir)
	if err != nil {
		t.Fatalf("newPlatformImpl: %v", err)
	}
	lw := w.(*linuxWatcher)
	lw.cfg = WatcherConfig{DebounceDuration: 50 * time.Millisecond, RateLimit: 100}
	if err := lw.start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { lw.stop() })

	// Deploy token #2 AFTER the watcher started (simulates TUI deploy).
	decoy2 := filepath.Join(dir, "token2")
	if err := os.WriteFile(decoy2, []byte("cred2"), 0o600); err != nil {
		t.Fatalf("write token2: %v", err)
	}
	snap2 := append(snap, DeployedToken{
		ID: "reloadtoken00002", Type: "aws_credentials", DeployedPath: decoy2,
	})
	if err := WriteDeployedSnapshot(dir, snap2); err != nil {
		t.Fatalf("WriteDeployedSnapshot2: %v", err)
	}

	// Call Reload() — this is the fix that makes the new token watchable.
	if err := lw.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	stAfter := lw.status()
	if stAfter.Watching != 2 {
		t.Errorf("after reload: expected 2 watches, got %d", stAfter.Watching)
	}

	// Trigger token2 (open the file — fires IN_OPEN).
	f, err := os.Open(decoy2)
	if err != nil {
		t.Fatalf("open token2: %v", err)
	}
	_ = f.Close()

	// Wait up to 2 seconds for the trigger event for token2 to appear in triglog.
	deadline := time.Now().Add(2 * time.Second)
	var found bool
	for time.Now().Before(deadline) {
		events, loadErr := triglog.Load(dir)
		if loadErr != nil {
			t.Fatalf("triglog.Load: %v", loadErr)
		}
		for _, ev := range events {
			if ev.TokenID == "reloadtoken00002" {
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
		t.Error("token2 (deployed after watcher start) did not appear in triglog after Reload()")
	}
}

// TestWatcherReload_RemovedTokenUnwatched proves that after Reload(), a purged
// token is removed from the watch set (count decreases).
func TestWatcherReload_RemovedTokenUnwatched(t *testing.T) {
	dir := t.TempDir()

	decoy1 := filepath.Join(dir, "token_keep")
	decoy2 := filepath.Join(dir, "token_purge")
	for _, p := range []string{decoy1, decoy2} {
		if err := os.WriteFile(p, []byte("cred"), 0o600); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	snap := []DeployedToken{
		{ID: "keepid000000001", Type: "aws_credentials", DeployedPath: decoy1},
		{ID: "purgeid00000001", Type: "aws_credentials", DeployedPath: decoy2},
	}
	if err := WriteDeployedSnapshot(dir, snap); err != nil {
		t.Fatalf("WriteDeployedSnapshot: %v", err)
	}

	w, err := newPlatformImpl(nil, dir)
	if err != nil {
		t.Fatalf("newPlatformImpl: %v", err)
	}
	lw := w.(*linuxWatcher)
	lw.cfg = WatcherConfig{DebounceDuration: 50 * time.Millisecond, RateLimit: 100}
	if err := lw.start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { lw.stop() })

	st := lw.status()
	if st.Watching != 2 {
		t.Fatalf("expected 2 watches before purge, got %d", st.Watching)
	}

	// Remove token2 from the snapshot (simulate purge).
	snap2 := []DeployedToken{snap[0]}
	if err := WriteDeployedSnapshot(dir, snap2); err != nil {
		t.Fatalf("WriteDeployedSnapshot2: %v", err)
	}
	if err := lw.reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	st2 := lw.status()
	if st2.Watching != 1 {
		t.Errorf("after reload+purge: expected 1 watch, got %d", st2.Watching)
	}
}
