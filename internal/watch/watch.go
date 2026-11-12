// Package watch will contain the file watcher and detection engine (Phase 4).
// This file exposes the Watcher API surface used by cmd/decoyd/cmd_watch.go.
// The full platform-specific implementations (inotify on Linux, fsnotify on
// Windows) are built in Phase 4 and live in separate build-tagged files.
package watch

import (
	"github.com/arjunjaincs/decoyd/internal/store"
)

// WatcherStatus is a snapshot of watcher state returned by Watcher.Status.
type WatcherStatus struct {
	// Running is true when the watcher event loop is active.
	Running bool
	// Watching is the number of files currently under watch.
	Watching int
	// Tier is "tier1" when fanotify is active, "tier2" for inotify-only. Empty string = tier2.
	Tier string
	// FanotifyFailed maps token IDs to failure reasons for tokens that
	// could not be marked with fanotify and fell back to inotify.
	FanotifyFailed map[string]string
}

// WatchOptions controls optional watcher behaviour.
// Fields not applicable to the current platform are silently ignored.
type WatchOptions struct {
	// Tier1 requires fanotify-based detection (Linux only, O6).
	// If true and fanotify is unavailable, Start returns an error.
	// If false and fanotify is unavailable, falls back to inotify silently.
	Tier1 bool
}

// Watcher watches deployed decoy-token files and fires alerts on access.
// The concrete implementation is platform-specific (Phase 4).
//
// Two operating modes:
//
//	TUI-embedded (st != nil): watcher runs inside the TUI process, which
//	  already owns the bbolt write lock. Token list is read from bbolt.
//
//	Headless (st == nil): watcher runs as "decoyd watch" (systemd/Task
//	  Scheduler managed). Token list is read from deployed_tokens.json.
//	  No bbolt access.
type Watcher struct {
	impl watcherImpl
}

// New creates a Watcher. Pass st=nil for headless mode (no bbolt access).
// dataDir is the OS data directory used for config and the snapshot file.
func New(st *store.Store, dataDir string) (*Watcher, error) {
	return NewWithOptions(st, dataDir, WatchOptions{})
}

// NewWithOptions creates a Watcher with explicit options.
// Use opts.Tier1 = true to require fanotify-based detection (Linux only).
func NewWithOptions(st *store.Store, dataDir string, opts WatchOptions) (*Watcher, error) {
	impl, err := newImplWithOptions(st, dataDir, opts)
	if err != nil {
		return nil, err
	}
	return &Watcher{impl: impl}, nil
}

// Start initialises the underlying watcher and begins the event loop.
func (w *Watcher) Start() error { return w.impl.start() }

// Stop signals the event loop to exit and waits for it to finish.
func (w *Watcher) Stop() { w.impl.stop() }

// Status returns a snapshot of the current watcher state.
func (w *Watcher) Status() WatcherStatus { return w.impl.status() }

// Reload re-reads the token list and synchronises the set of watched files,
// adding newly deployed tokens and removing purged ones. Idempotent and
// thread-safe. The TUI calls it after deploy and after remove/purge.
// Returns nil if the watcher is not running (no-op in that case).
func (w *Watcher) Reload() error { return w.impl.reload() }
