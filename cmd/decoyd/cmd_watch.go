// cmd_watch.go — headless watcher for both platforms.
// IMPORTANT: this command MUST NOT open decoyd.db.  See store.Open() and
// the package comment in internal/watch/watch.go for the cross-process
// locking rationale.

package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/arjunjaincs/decoyd/internal/watch"
)

// cmdWatch starts the headless watcher and blocks until SIGINT or SIGTERM.
// st is intentionally absent: the watcher runs with nil st (headless mode),
// reading token paths from deployed_tokens.json and writing triggers to
// triggers.jsonl.  It does not open decoyd.db.
//
// When tier1 is true, requires fanotify-based detection (Linux only).
// Errors if fanotify is unavailable — no silent downgrade (O6).
func cmdWatch(dataDir string, tier1 bool) error {
	opts := watch.WatchOptions{Tier1: tier1}
	w, err := watch.NewWithOptions(nil, dataDir, opts) // nil = headless, no bbolt
	if err != nil {
		return err // watch.NewWithOptions errors are already user-facing
	}
	if err := w.Start(); err != nil {
		return err // ErrWatcherRunning etc. are already user-facing
	}

	status := w.Status()
	fmt.Printf("decoyd watch started — monitoring %d tokens\n", status.Watching)
	fmt.Println("Press Ctrl+C to stop.")

	// Poll deployed_tokens.json for mtime/size changes every 5 seconds.
	// When the TUI deploys or removes a token it updates the snapshot file;
	// this goroutine detects the change and calls Reload() so the headless
	// watcher picks up the new token set without restarting.
	snapshotPath := filepath.Join(dataDir, "deployed_tokens.json")
	go watchSnapshotForReload(w, snapshotPath)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	fmt.Println("\nstopping watcher…")
	w.Stop()
	fmt.Println("stopped.")
	return nil
}

// watchSnapshotForReload polls the deployed_tokens.json file every 5 seconds.
// If its mtime or size changes it calls w.Reload() to sync the inotify watch
// set. Errors from Reload are logged to stderr and do not stop the watcher.
func watchSnapshotForReload(w *watch.Watcher, snapshotPath string) {
	var lastMod time.Time
	var lastSize int64

	// Seed initial state so the first tick doesn't reload unnecessarily.
	if fi, err := os.Stat(snapshotPath); err == nil { // #nosec G703 -- snapshotPath is inside validated operator/user dataDir
		lastMod = fi.ModTime()
		lastSize = fi.Size()
	}

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		fi, err := os.Stat(snapshotPath) // #nosec G703 -- snapshotPath is inside validated operator/user dataDir
		if err != nil {
			continue // file may not exist yet
		}
		if fi.ModTime().Equal(lastMod) && fi.Size() == lastSize {
			continue // no change
		}
		lastMod = fi.ModTime()
		lastSize = fi.Size()

		if reloadErr := w.Reload(); reloadErr != nil {
			fmt.Fprintf(os.Stderr, "decoyd watch: reload error: %v\n", reloadErr)
		} else {
			st := w.Status()
			fmt.Printf("decoyd watch: reloaded — monitoring %d tokens\n", st.Watching)
		}
	}
}
