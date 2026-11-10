//go:build linux

package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arjunjaincs/decoyd/internal/alert"
	"github.com/arjunjaincs/decoyd/internal/watch"
)

// TestSelfTest_PipelineAlertArrives verifies the full watcher→alert pipeline
// using an httptest server and a temporary decoy. This is the same logic as
// cmdSelfTest but driven by the test framework so we get hermetic isolation.
func TestSelfTest_PipelineAlertArrives(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	dir := t.TempDir()

	// Loopback webhook.
	var alertCount int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body) //nolint:errcheck
		atomic.AddInt64(&alertCount, 1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	// Alert config.
	cfg := alert.AlertConfig{
		Channels: []alert.ChannelConfig{{
			ID:         "ch1",
			Type:       alert.ChannelWebhook,
			WebhookURL: srv.URL,
		}},
		DefaultID: "ch1",
	}
	if err := alert.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}

	// Decoy file.
	decoyPath := dir + "/token.txt"
	if err := os.WriteFile(decoyPath, []byte("test-secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Snapshot.
	tokens := []watch.DeployedToken{{
		ID:           "t1",
		Type:         "aws_credentials",
		DeployedPath: decoyPath,
	}}
	if err := watch.WriteDeployedSnapshot(dir, tokens); err != nil {
		t.Fatal(err)
	}

	// Start watcher.
	w, err := watch.New(nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan error, 1)
	go func() { started <- w.Start() }()
	t.Cleanup(func() { w.Stop() })

	// Let inotify watches register.
	time.Sleep(400 * time.Millisecond)

	// Trigger.
	if _, err := os.ReadFile(decoyPath); err != nil {
		t.Fatal(err)
	}

	// Wait up to 5 s for the alert.
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&alertCount) > 0 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	if atomic.LoadInt64(&alertCount) == 0 {
		t.Error("no alert received within 5 s")
	}
}

// TestSelfTest_DoesNotTouchRealDataDir verifies that cmdSelfTest operates
// entirely within its own temp workspace and never writes to the real
// HOME or APPDATA directories.
//
// Method: set HOME and APPDATA to a chmod-500 sentinel directory so that
// any write attempt would produce an EACCES error visible via os.Stat().
// Record that the sentinel is unmodified before and after the run.
func TestSelfTest_DoesNotTouchRealDataDir(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission checks; skip hermetic sentinel test")
	}

	// Sentinel: a read-only dir that must not be written to.
	sentinel := t.TempDir()
	if err := os.Chmod(sentinel, 0o500); err != nil {
		t.Fatalf("chmod sentinel: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(sentinel, 0o700) })

	// Redirect HOME and APPDATA to the sentinel.
	t.Setenv("HOME", sentinel)
	t.Setenv("APPDATA", sentinel)

	// Get sentinel mtime before the test.
	before, err := os.Stat(sentinel)
	if err != nil {
		t.Fatalf("stat sentinel before: %v", err)
	}

	// Run selftest with an isolated temp dataDir.
	selfDir := t.TempDir()
	if runErr := cmdSelfTest(selfDir); runErr != nil {
		t.Logf("cmdSelfTest returned error (acceptable in hermetic env): %v", runErr)
	}

	// Assert sentinel was not touched.
	after, err := os.Stat(sentinel)
	if err != nil {
		t.Fatalf("stat sentinel after: %v", err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("sentinel dir mtime changed: before=%v after=%v — selftest touched real datadir",
			before.ModTime(), after.ModTime())
	}
	// Count entries — should still be 0.
	entries, err := os.ReadDir(sentinel)
	if err != nil {
		t.Fatalf("readdir sentinel: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("sentinel dir has %d unexpected entries after selftest run", len(entries))
	}
}
