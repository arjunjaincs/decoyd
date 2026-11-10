//go:build linux

// hybrid_linux.go — hybrid fanotify+inotify watcher coordinator for Linux.
//
// Architecture (O1-O6, T2):
//   - hybridLinuxWatcher wraps a *linuxWatcher (inotify) + *fanotifyWatcher.
//   - On start(), fanotify is probed. If unavailable and Tier1==true → error.
//   - Tokens with successful fanotify marks have inotify open/access suppressed
//     (inotify still handles modify/move/delete and parent-dir watches).
//   - On reload(), fanotify marks are synced alongside inotify watches.
//   - FAN_Q_OVERFLOW: logged; "events lost" alert is sent if alerter available.
//   - Self-test via child-reads the decoy in Tier 1 mode is in cmd_selftest.go.
package watch

import (
	"fmt"
	"log"

	"github.com/arjunjaincs/decoyd/internal/attribution"
	"github.com/arjunjaincs/decoyd/internal/store"
)

// hybridLinuxWatcher wraps linuxWatcher with an optional fanotifyWatcher.
// When fanWatcher is nil, behaviour is identical to linuxWatcher alone.
type hybridLinuxWatcher struct {
	inoWatcher *linuxWatcher
	fanWatcher *fanotifyWatcher // nil when fanotify is unavailable
	tier1      bool
}

// newPlatformImpl replaces the previous direct linuxWatcher constructor.
// It probes fanotify availability; on failure:
//   - Tier1 mode → returns an error
//   - Tier2 mode → returns a plain linuxWatcher (fanotify not used)
func newPlatformImpl(st *store.Store, dataDir string) (watcherImpl, error) {
	return newPlatformImplWithOptions(st, dataDir, WatchOptions{})
}

// newPlatformImplWithOptions is the full constructor used by NewWithOptions.
func newPlatformImplWithOptions(st *store.Store, dataDir string, opts WatchOptions) (watcherImpl, error) {
	ino := &linuxWatcher{
		st:      st,
		dataDir: dataDir,
		cfg:     DefaultWatcherConfig(),
		inoFD:   -1,
		stopR:   -1,
		stopW:   -1,
	}

	fan, err := newFanotifyWatcher()
	if err != nil {
		if opts.Tier1 {
			return nil, fmt.Errorf("decoyd watch --tier1: fanotify unavailable: %w", err)
		}
		log.Printf("decoyd: fanotify init failed (%v); falling back to inotify (Tier 2)", err)
		return ino, nil
	}
	if fan == nil {
		// Graceful unavailability (EPERM, ENODEV, etc.)
		if opts.Tier1 {
			return nil, fmt.Errorf("decoyd watch --tier1: fanotify not available on this system (try running as root with CAP_SYS_ADMIN)")
		}
		log.Printf("decoyd: fanotify not available; running in Tier 2 (inotify only)")
		return ino, nil
	}

	return &hybridLinuxWatcher{
		inoWatcher: ino,
		fanWatcher: fan,
		tier1:      opts.Tier1,
	}, nil
}

// start initialises both inotify and fanotify, then starts the fanotify
// event loop goroutine. The inotify event loop runs inside linuxWatcher.start().
func (h *hybridLinuxWatcher) start() error {
	// Start the inotify watcher first (loads tokens, sets up watches).
	if err := h.inoWatcher.start(); err != nil {
		return err
	}

	// Load token list to add fanotify marks.
	tokens, err := h.inoWatcher.loadTokens()
	if err != nil {
		h.inoWatcher.stop()
		return fmt.Errorf("hybrid start: load tokens: %w", err)
	}

	// Add fanotify marks for all deployed tokens.
	for _, tok := range tokens {
		if tok.DeployedPath == "" {
			continue
		}
		h.fanWatcher.addMark(tok)
	}

	// Report any failed marks.
	for id, reason := range h.fanWatcher.failedTokens {
		log.Printf("fanotify: token %s fell back to inotify: %s", id, reason)
	}

	// Start the fanotify event loop in a background goroutine.
	go h.runFanotifyDispatch()

	return nil
}

// runFanotifyDispatch runs the fanotify event loop and dispatches events.
// Attribution is built in this goroutine (not the kernel event loop).
func (h *hybridLinuxWatcher) runFanotifyDispatch() {
	h.fanWatcher.runFanotifyLoop(func(tok DeployedToken, pid int32) {
		// Build attribution in dispatch goroutine (O7: not in event loop).
		var attr *attribution.Attribution
		if pid > 0 {
			attr = BuildAttribution(pid, DefaultProcRoot)
		}

		// Delegate to linuxWatcher's alert dispatch pipeline.
		// Pass attribution via the extended dispatch helper.
		h.inoWatcher.dispatchFanotifyEvent(tok, pid, attr)
	})
}

// stop signals both event loops to exit.
func (h *hybridLinuxWatcher) stop() {
	h.fanWatcher.signalStop()
	h.inoWatcher.stop()
	h.fanWatcher.close()
}

// status returns the status from the inotify watcher, augmented with Tier info.
func (h *hybridLinuxWatcher) status() WatcherStatus {
	st := h.inoWatcher.status()
	st.Tier = "tier1"
	st.FanotifyFailed = h.fanWatcher.failedTokens
	return st
}

// reload syncs both inotify and fanotify watches with the current token list.
func (h *hybridLinuxWatcher) reload() error {
	if err := h.inoWatcher.reload(); err != nil {
		return err
	}

	// Re-sync fanotify marks.
	tokens, err := h.inoWatcher.loadTokens()
	if err != nil {
		return fmt.Errorf("hybrid reload: load tokens: %w", err)
	}

	// Build set of current token paths.
	newByID := make(map[string]DeployedToken, len(tokens))
	for _, t := range tokens {
		if t.DeployedPath != "" {
			newByID[t.ID] = t
		}
	}

	// Remove marks for tokens no longer deployed.
	for dev, inoMap := range h.fanWatcher.tokenMap {
		for ino, tok := range inoMap {
			if _, still := newByID[tok.ID]; !still {
				h.fanWatcher.removeMark(tok)
				// removeMark deletes from tokenMap; iterate by copy is safe enough
				// since we only delete after iterating — Go map iteration is defined.
				_ = dev
				_ = ino
			}
		}
	}

	// Add marks for newly deployed tokens.
	// Build set of already-marked token IDs.
	markedIDs := make(map[string]bool)
	for _, inoMap := range h.fanWatcher.tokenMap {
		for _, tok := range inoMap {
			markedIDs[tok.ID] = true
		}
	}
	for _, tok := range tokens {
		if tok.DeployedPath == "" || markedIDs[tok.ID] {
			continue
		}
		if !h.fanWatcher.addMark(tok) {
			reason := h.fanWatcher.failedTokens[tok.ID]
			log.Printf("fanotify: reload: token %s fell back to inotify: %s", tok.ID, reason)
		}
	}

	return nil
}
