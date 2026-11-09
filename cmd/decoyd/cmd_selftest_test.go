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
