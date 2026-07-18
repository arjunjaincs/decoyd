// cmd_watch.go — headless watcher for both platforms.
// IMPORTANT: this command MUST NOT open decoyd.db.  See store.Open() and
// the package comment in internal/watch/watch.go for the cross-process
// locking rationale.

package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/arjunjaincs/decoyd/internal/watch"
)

// cmdWatch starts the headless watcher and blocks until SIGINT or SIGTERM.
// st is intentionally absent: the watcher runs with nil st (headless mode),
// reading token paths from deployed_tokens.json and writing triggers to
// triggers.jsonl.  It does not open decoyd.db.
func cmdWatch(dataDir string) error {
	w, err := watch.New(nil, dataDir) // nil = headless, no bbolt
	if err != nil {
		return fmt.Errorf("watch init: %w", err)
	}
	if err := w.Start(); err != nil {
		return fmt.Errorf("watch start: %w", err)
	}

	status := w.Status()
	fmt.Printf("decoyd watch started — monitoring %d tokens\n", status.Watching)
	fmt.Println("Press Ctrl+C to stop.")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	fmt.Println("\nstopping watcher…")
	w.Stop()
	fmt.Println("stopped.")
	return nil
}
