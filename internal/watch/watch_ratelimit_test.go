//go:build linux

package watch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arjunjaincs/decoyd/internal/alert"
	"github.com/arjunjaincs/decoyd/internal/triglog"
)

// TestRateLimit_SuppressionAlertSentOnce verifies that:
//  1. Events beyond the rate limit are logged as TriggerRateLimited.
//  2. Exactly ONE suppression alert is sent per token per window.
func TestRateLimit_SuppressionAlertSentOnce(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())

	// Count how many HTTP calls reach the webhook.
	var alertCount int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&alertCount, 1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	// Write alert config pointing at the test server.
	cfg := alert.AlertConfig{
		Channels: []alert.ChannelConfig{{
			Type:       alert.ChannelWebhook,
			WebhookURL: srv.URL,
		}},
	}
	cfg.Channels[0].ID = "ch1"
	cfg.DefaultID = "ch1"
	if err := alert.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}

	// Deploy a decoy file into a temp path.
	decoyPath := filepath.Join(dir, "token.txt")
	if err := os.WriteFile(decoyPath, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Use a rate limit of 2 so we can easily exceed it.
	watchCfg := WatcherConfig{
		RateLimit: 2,
	}
	tok := DeployedToken{
		ID:           "t1",
		Type:         "aws_credentials",
		DeployedPath: decoyPath,
	}

	// Build a minimal linuxWatcher to call dispatch directly (bypassing inotify).
	w := &linuxWatcher{
		dataDir: dir,
		cfg:     watchCfg,
	}

	now := time.Now()
	rateMap := make(map[string]*rateEntry)

	// First 2 dispatches should produce TriggerPending and reach the alert server.
	w.dispatch(rateMap, tok, "access", now)
	w.dispatch(rateMap, tok, "access", now)

	// Next 3 dispatches should be rate-limited.
	// Only the FIRST rate-limited dispatch should trigger a suppression alert.
	w.dispatch(rateMap, tok, "access", now)
	w.dispatch(rateMap, tok, "access", now)
	w.dispatch(rateMap, tok, "access", now)

	// Allow pending HTTP requests to complete.
	time.Sleep(200 * time.Millisecond)

	// Exactly 2 normal + 1 suppression = 3 total alerts.
	got := atomic.LoadInt64(&alertCount)
	if got != 3 {
		t.Errorf("alert count = %d; want 3 (2 normal + 1 suppression)", got)
	}

	// Check triglog: 2 successful events produce 2×pending+sent records; 3
	// rate-limited events produce 3 records each. Count rate_limited records.
	events, err := triglog.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var rateLimitedCount int
	for _, ev := range events {
		if ev.Status == triglog.TriggerRateLimited {
			rateLimitedCount++
		}
	}
	if rateLimitedCount != 3 {
		t.Errorf("rate_limited triglog entries = %d; want 3", rateLimitedCount)
	}

	// A FOURTH suppression dispatch must NOT send another alert.
	w.dispatch(rateMap, tok, "access", now)
	time.Sleep(200 * time.Millisecond)
	got = atomic.LoadInt64(&alertCount)
	if got != 3 {
		t.Errorf("alert count after 4th suppressed = %d; still want 3", got)
	}

	// New window resets alertSent: advance time past windowEnd.
	future := now.Add(2 * time.Hour)
	w.dispatch(rateMap, tok, "access", future)
	w.dispatch(rateMap, tok, "access", future)
	w.dispatch(rateMap, tok, "access", future) // 3rd → rate limited again → new suppression
	time.Sleep(200 * time.Millisecond)
	got = atomic.LoadInt64(&alertCount)
	// 2 new normal + 1 new suppression = 3 more = 6 total.
	if got != 6 {
		t.Errorf("alert count after new window = %d; want 6", got)
	}
}

// Compile-time check that context is used in sendSuppressionAlert.
var _ = context.Background
