//go:build windows

package watch

// Windows read-detection limitation:
//
// On Windows, fsnotify only reliably surfaces Write, Rename, and Remove events.
// True read-detection (detecting OPEN/ACCESS without a write) is not available
// without kernel-level hooks (e.g. minifilter driver), which are out of scope
// for Decoyd v1.
//
// Two operating modes — same as the Linux inotify implementation:
//
//	TUI-embedded (st != nil): bbolt already open by TUI. Token list from bbolt.
//	Headless (st == nil):     NO bbolt access. Token list from deployed_tokens.json.
//	                          Triggers written to triggers.jsonl.

import (
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/arjunjaincs/decoyd/internal/store"
	"github.com/arjunjaincs/decoyd/internal/tokens"
	"github.com/arjunjaincs/decoyd/internal/triglog"
)

// Watcher watches deployed decoy-token files on Windows using fsnotify.
type Watcher struct {
	mu      sync.RWMutex
	watched map[string]tokens.Token // absolute path → token
	dirs    map[string]bool

	st      *store.Store // nil in headless mode
	dataDir string
	cfg     WatchConfig

	debouncer *Debouncer
	rateLim   *RateLimiter

	fsw         *fsnotify.Watcher
	stopCh      chan struct{}
	lockRelease func() // releases the watcher.pid singleton lock

	startedAt time.Time
	running   bool
	wg        sync.WaitGroup

	trigMu   sync.Mutex
	triggers []triglog.TriggerEvent
}

// New creates a Watcher. Pass st=nil for headless mode (no bbolt access).
func New(st *store.Store, dataDir string) (*Watcher, error) {
	cfg, _ := LoadWatchConfig(dataDir)
	w := &Watcher{
		watched:   make(map[string]tokens.Token),
		dirs:      make(map[string]bool),
		st:        st,
		dataDir:   dataDir,
		cfg:       cfg,
		debouncer: NewDebouncer(time.Duration(cfg.DebounceSeconds) * time.Second),
		rateLim:   NewRateLimiter(cfg.RateLimitPerHour),
	}
	past, _ := triglog.Load(dataDir)
	if len(past) > 50 {
		past = past[:50]
	}
	w.triggers = past
	return w, nil
}

// Start initialises fsnotify and launches the event loop.
// Returns ErrWatcherRunning if another watcher instance is already running
// (detected via watcher.pid singleton lock).
func (w *Watcher) Start() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.running {
		return nil
	}

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("fsnotify init: %w", err)
	}
	w.fsw = fsw
	w.stopCh = make(chan struct{})

	// Acquire singleton lock before touching any watched files.
	// This prevents a TUI-embedded watcher and a headless 'decoyd watch'
	// from running concurrently (duplicate alerts, split rate-limiter state).
	release, lockErr := acquireWatchLock(w.dataDir)
	if lockErr != nil {
		_ = w.fsw.Close()
		return lockErr // caller checks errors.Is(err, ErrWatcherRunning)
	}
	w.lockRelease = release

	if w.st != nil {
		toks, _ := w.st.ListTokens()
		for _, tok := range toks {
			if tok.DeployedPath != "" {
				w.addWatchLocked(tok)
			}
		}
	} else {
		snaps, _ := ReadDeployedSnapshot(w.dataDir)
		for _, s := range snaps {
			tok := tokens.Token{
				ID:             s.ID,
				Type:           s.Type,
				DeployedPath:   s.DeployedPath,
				AlertChannelID: s.AlertChannelID,
			}
			w.addWatchLocked(tok)
		}
	}

	w.running = true
	w.startedAt = time.Now()
	w.wg.Add(1)
	go w.loop()
	return nil
}

// Stop signals the event loop to exit and waits for it.
func (w *Watcher) Stop() {
	w.mu.Lock()
	if !w.running {
		w.mu.Unlock()
		return
	}
	w.running = false
	close(w.stopCh)
	release := w.lockRelease
	w.lockRelease = nil
	w.mu.Unlock()

	w.debouncer.Stop()
	w.wg.Wait()
	_ = w.fsw.Close()
	if release != nil {
		release() // remove watcher.pid
	}
}

// Status returns a snapshot of the watcher state.
func (w *Watcher) Status() WatcherStatus {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return WatcherStatus{
		Running:   w.running,
		StartedAt: w.startedAt,
		Watching:  len(w.watched),
	}
}

// RecentTriggers returns up to n recent trigger events (newest-first).
func (w *Watcher) RecentTriggers(n int) []triglog.TriggerEvent {
	w.trigMu.Lock()
	defer w.trigMu.Unlock()
	out := make([]triglog.TriggerEvent, len(w.triggers))
	copy(out, w.triggers)
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// AddToken adds a watch for a newly deployed token.
func (w *Watcher) AddToken(tok tokens.Token) {
	if tok.DeployedPath == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.running {
		return
	}
	w.addWatchLocked(tok)
}

func (w *Watcher) addWatchLocked(tok tokens.Token) {
	absPath := filepath.Clean(tok.DeployedPath)
	if _, exists := w.watched[absPath]; exists {
		return
	}
	w.watched[absPath] = tok
	dir := filepath.Dir(absPath)
	if !w.dirs[dir] {
		if err := w.fsw.Add(dir); err == nil {
			w.dirs[dir] = true
		}
	}
}

func (w *Watcher) loop() {
	defer w.wg.Done()
	for {
		select {
		case <-w.stopCh:
			return
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			w.handleEvent(ev)
		case <-w.fsw.Errors:
			// structured logging would go here
		}
	}
}

func (w *Watcher) handleEvent(ev fsnotify.Event) {
	absPath := filepath.Clean(ev.Name)
	evtType := fsnotifyOpToEventType(ev.Op)
	if evtType == "" {
		return
	}
	w.mu.RLock()
	tok, ok := w.watched[absPath]
	w.mu.RUnlock()
	if !ok {
		return
	}
	path := absPath
	tokCopy := tok
	w.debouncer.Trigger(path, func() {
		w.fire(tokCopy, path, evtType)
	})
}

func (w *Watcher) fire(tok tokens.Token, path, evtType string) {
	now := time.Now().UTC()

	te := triglog.TriggerEvent{
		ID:          newEventID(),
		TokenID:     tok.ID,
		TokenType:   tok.Type,
		Path:        path,
		TriggeredAt: now,
		EventType:   evtType,
		Status:      triglog.TriggerPending,
	}

	// ── Rate limit ────────────────────────────────────────────────────────
	if !w.rateLim.Allow(tok.ID) {
		te.Status = triglog.TriggerRateLimited
		_ = triglog.Append(w.dataDir, te)
		w.cacheTrigger(te)
		return
	}

	// ── Quiet hours ───────────────────────────────────────────────────────
	if InQuietHours(w.cfg, now) {
		te.Status = triglog.TriggerQuietHours
		_ = triglog.Append(w.dataDir, te)
		w.cacheTrigger(te)
		return
	}

	// ── DURABILITY: write pending BEFORE sending ──────────────────────────
	_ = triglog.Append(w.dataDir, te)

	// ── Dispatch alert ────────────────────────────────────────────────────
	status, alertErr := sendAlert(w.dataDir, tok, te)

	// ── Append final status ───────────────────────────────────────────────
	te.Status = status
	te.AlertError = alertErr
	_ = triglog.Append(w.dataDir, te)
	w.cacheTrigger(te)

	if status == triglog.TriggerSent {
		markTokenTriggered(w.st, tok, now)
	}
}

func (w *Watcher) cacheTrigger(te triglog.TriggerEvent) {
	w.trigMu.Lock()
	w.triggers = append([]triglog.TriggerEvent{te}, w.triggers...)
	if len(w.triggers) > 50 {
		w.triggers = w.triggers[:50]
	}
	w.trigMu.Unlock()
}

func fsnotifyOpToEventType(op fsnotify.Op) string {
	switch {
	case op&fsnotify.Write != 0:
		return "write"
	case op&fsnotify.Rename != 0:
		return "rename"
	case op&fsnotify.Remove != 0:
		return "delete"
	case op&fsnotify.Create != 0:
		return "create"
	}
	return ""
}
