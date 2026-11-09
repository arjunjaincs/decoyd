// Package main — decoyd test subcommand.
// Deploys a temporary decoy, starts a watcher, reads the file, and verifies
// that an alert arrives on a loopback httptest server within 5 seconds.
// Useful for validating the full pipeline without network or real credentials.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/arjunjaincs/decoyd/internal/alert"
	"github.com/arjunjaincs/decoyd/internal/watch"
)

// cmdSelfTest runs a full pipeline self-test:
//  1. Creates a temp decoy file.
//  2. Writes an alert config pointing at a loopback httptest server.
//  3. Starts a file watcher (headless, snapshot-based).
//  4. Reads the decoy file to trigger an event.
//  5. Waits up to 5 s for the alert to arrive.
//  6. Cleans up.
//
// Exit codes: 0=pass, 1=fail (also returns non-nil error).
func cmdSelfTest(dataDir string) error {
	fmt.Println("decoyd self-test: starting …")

	// --- 1. Temp workspace ---
	dir, err := os.MkdirTemp("", "decoyd-selftest-*")
	if err != nil {
		return fmt.Errorf("selftest: create temp dir: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(dir)
		fmt.Println("decoyd self-test: temp workspace cleaned up")
	}()

	decoyPath := filepath.Join(dir, "token.txt")
	if err := os.WriteFile(decoyPath, []byte("selftest-secret"), 0o600); err != nil {
		return fmt.Errorf("selftest: create decoy: %w", err)
	}
	fmt.Println("  decoy file:    ", decoyPath)

	// --- 2. Loopback webhook server ---
	var alertReceived int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&alertReceived, 1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	fmt.Println("  webhook server:", srv.URL)

	// --- 3. Alert config (write into temp dir, not real dataDir) ---
	cfg := alert.AlertConfig{
		Channels: []alert.ChannelConfig{{
			ID:         "selftest-ch",
			Type:       alert.ChannelWebhook,
			WebhookURL: srv.URL,
		}},
		DefaultID: "selftest-ch",
	}
	if err := alert.Save(dir, cfg); err != nil {
		return fmt.Errorf("selftest: write alert config: %w", err)
	}

	// --- 4. Snapshot with the temp decoy ---
	tokens := []watch.DeployedToken{{
		ID:           "selftest-tok",
		Type:         "aws_credentials",
		DeployedPath: decoyPath,
	}}
	if err := watch.WriteDeployedSnapshot(dir, tokens); err != nil {
		return fmt.Errorf("selftest: write snapshot: %w", err)
	}

	// --- 5. Start headless watcher ---
	// Use default config (2 s debounce). The wait loop below polls for 8 s.
	w, err := watch.New(nil, dir)
	if err != nil {
		return fmt.Errorf("selftest: create watcher: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go func() {
		if startErr := w.Start(); startErr != nil {
			fmt.Fprintln(os.Stderr, "decoyd self-test: watcher start error:", startErr)
		}
	}()
	defer w.Stop()

	// Brief pause to let the watcher register inotify watches.
	time.Sleep(300 * time.Millisecond)

	// --- 6. Trigger: read the decoy ---
	fmt.Println("  triggering read …")
	if _, readErr := os.ReadFile(decoyPath); readErr != nil {
		return fmt.Errorf("selftest: read decoy: %w", readErr)
	}

	// --- 7. Wait for alert ---
	fmt.Print("  waiting for alert ")
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&alertReceived) > 0 {
			break
		}
		time.Sleep(200 * time.Millisecond)
		fmt.Print(".")
	}
	fmt.Println()

	_ = ctx // used for timeout context above

	if atomic.LoadInt64(&alertReceived) == 0 {
		return fmt.Errorf("selftest FAILED: no alert received within 5 s")
	}

	fmt.Printf("decoyd self-test: PASSED (%d alert(s) received)\n", atomic.LoadInt64(&alertReceived))
	return nil
}

// selfTestPayloadDump is a helper for JSON decoding in the httptest handler
// (unused in production path; kept for test assertions in cmd_selftest_test.go).
type selfTestPayloadDump struct {
	TokenID string `json:"token_id"`
}

// unmarshalPayload decodes a webhook POST body into selfTestPayloadDump.
func unmarshalPayload(body []byte) (selfTestPayloadDump, error) {
	var d selfTestPayloadDump
	return d, json.Unmarshal(body, &d)
}
