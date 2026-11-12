//go:build linux

// hybrid_linux.go — hybrid fanotify+inotify watcher coordinator for Linux.
//
// Architecture (O1-O6, T2):
//   - hybridLinuxWatcher wraps a *linuxWatcher (inotify) + *fanotifyWatcher.
//   - On start(), fanotify is probed. If unavailable and Tier1==true -> error.
//   - Tokens with successful fanotify marks have inotify open/access suppressed
//     (inotify still handles modify/move/delete and parent-dir watches).
//   - On reload(), fanotify marks are synced alongside inotify watches.
//   - FAN_Q_OVERFLOW: logged; does not abort watcher.
package watch

import (
	"fmt"
	"log"

	"github.com/arjunjaincs/decoyd/internal/attribution"
	"github.com/arjunjaincs/decoyd/internal/store"
)

// hybridLinuxWatcher wraps linuxWatcher with an optional fanotifyWatcher.
type hybridLinuxWatcher struct {
	inoWatcher *linuxWatcher
	fanWatcher *fanotifyWatcher // non-nil when Tier 1 fanotify initialised
	tier1      bool
}

func newPlatformImpl(st *store.Store, dataDir string) (watcherImpl, error) {
	return newPlatformImplWithOptions(st, dataDir, WatchOptions{})
}

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
		if opts.Tier1 {
			return nil, fmt.Errorf("decoyd watch --tier1: fanotify not available on this system (try running as root with CAP_SYS_ADMIN)")
		}
		log.Printf("decoyd: fanotify not available; running in Tier 2 (inotify only)")
		return ino, nil
	}

	h := &hybridLinuxWatcher{
		inoWatcher: ino,
		fanWatcher: fan,
		tier1:      opts.Tier1,
	}

	// Wire hooks before linuxWatcher.start() launches eventLoop so suppression
	// state matches active fanotify marks from the first event onward (O2).
	ino.onTokensLoaded = func(tokens []DeployedToken) {
		h.syncFanotifyMarks(tokens)
	}
	ino.onFileCreate = func(tok DeployedToken) {
		ok := h.fanWatcher.addMark(tok)
		h.inoWatcher.setFanotifySuppressed(tok.ID, ok)
	}

	return h, nil
}

func (h *hybridLinuxWatcher) syncFanotifyMarks(tokens []DeployedToken) {
	for _, tok := range tokens {
		if tok.DeployedPath == "" {
			continue
		}
		ok := h.fanWatcher.addMark(tok)
		h.inoWatcher.setFanotifySuppressed(tok.ID, ok)
	}
	for id, reason := range h.fanWatcher.snapshotFailed() {
		log.Printf("fanotify: token %s fell back to inotify: %s", id, reason)
	}
}

func (h *hybridLinuxWatcher) start() error {
	if err := h.inoWatcher.start(); err != nil {
		h.fanWatcher.close()
		return err
	}
	go h.runFanotifyDispatch()
	return nil
}

func (h *hybridLinuxWatcher) runFanotifyDispatch() {
	h.fanWatcher.runFanotifyLoopWithOverflow(func(tok DeployedToken, pid int32) {
		var attr *attribution.Attribution
		if pid > 0 {
			attr = BuildAttribution(pid, DefaultProcRoot)
		}
		h.inoWatcher.dispatchFanotifyEvent(tok, pid, attr)
	}, h.inoWatcher.safeDispatchFanotifyOverflow)
}

func (h *hybridLinuxWatcher) stop() {
	h.fanWatcher.signalStop()
	if h.fanWatcher.done != nil {
		<-h.fanWatcher.done
	}
	h.inoWatcher.stop()
	h.fanWatcher.close()
}

func (h *hybridLinuxWatcher) status() WatcherStatus {
	st := h.inoWatcher.status()
	st.Tier = "tier1"
	st.FanotifyFailed = h.fanWatcher.snapshotFailed()
	return st
}

func (h *hybridLinuxWatcher) reload() error {
	if err := h.inoWatcher.reload(); err != nil {
		return err
	}

	tokens, err := h.inoWatcher.loadTokens()
	if err != nil {
		return fmt.Errorf("hybrid reload: load tokens: %w", err)
	}

	newByID := make(map[string]DeployedToken, len(tokens))
	for _, t := range tokens {
		if t.DeployedPath != "" {
			newByID[t.ID] = t
		}
	}

	for _, oldTok := range h.fanWatcher.snapshotMarkedTokens() {
		if _, still := newByID[oldTok.ID]; !still {
			h.fanWatcher.removeMark(oldTok)
			h.inoWatcher.setFanotifySuppressed(oldTok.ID, false)
		}
	}

	markedIDs := make(map[string]bool)
	for _, tok := range h.fanWatcher.snapshotMarkedTokens() {
		markedIDs[tok.ID] = true
	}
	for _, tok := range tokens {
		if tok.DeployedPath == "" || markedIDs[tok.ID] {
			continue
		}
		ok := h.fanWatcher.addMark(tok)
		h.inoWatcher.setFanotifySuppressed(tok.ID, ok)
		if !ok {
			failed := h.fanWatcher.snapshotFailed()
			log.Printf("fanotify: reload: token %s fell back to inotify: %s", tok.ID, failed[tok.ID])
		}
	}

	return nil
}
