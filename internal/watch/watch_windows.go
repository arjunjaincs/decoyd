//go:build windows

// watch_windows.go — fsnotify-based file watcher for Windows.
//
// # Windows read-only detection limitation (v1 known limitation, not a bug)
//
// On Windows, pure read-only file access (e.g. an attacker opening a credential
// file with GENERIC_READ only, without modifying it) is NOT detectable via the
// ReadDirectoryChangesW API that fsnotify uses. ReadDirectoryChangesW only
// surfaces filesystem metadata changes (write, rename, delete, create, chmod),
// not read-only opens or accesses.
//
// Detecting read-only opens on Windows requires ETW (Event Tracing for Windows)
// or a kernel minifilter driver — neither of which is available to a
// user-space application without elevated privileges or a signed kernel driver.
//
// This is a documented v1 limitation. The watcher still catches writes, renames,
// and deletes (e.g. an attacker who reads then modifies the file, or tools that
// touch the file on open). The decoyd install command documents this limitation
// in its output (see cmd/decoyd/cmd_install_windows.go).
//
// Architecture:
//   - One fsnotify.Watcher watching the parent directory of each deployed file.
//   - Events are filtered to the specific token filename only.
//   - Write / Rename / Remove events are forwarded as "write" / "rename" / "delete".
//   - A done channel provides clean shutdown (watcher.Close() unblocks the event loop).
package watch

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/arjunjaincs/decoyd/internal/alert"
	"github.com/arjunjaincs/decoyd/internal/store"
	"github.com/arjunjaincs/decoyd/internal/triglog"
)

// ----------------------------------------------------------------------------
// windowsWatcher
// ----------------------------------------------------------------------------

type windowsWatcher struct {
	st      *store.Store
	dataDir string
	cfg     WatcherConfig

	mu      sync.Mutex
	running bool
	count   int
	release func() // releases the watcher.pid lock; nil when not running

	watcher *fsnotify.Watcher
	done    chan struct{}
}

func newPlatformImpl(st *store.Store, dataDir string) (watcherImpl, error) {
	return &windowsWatcher{
		st:      st,
		dataDir: dataDir,
		cfg:     DefaultWatcherConfig(),
	}, nil
}

// ----------------------------------------------------------------------------
// start
// ----------------------------------------------------------------------------

func (w *windowsWatcher) start() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.running {
		return fmt.Errorf("watcher already running")
	}

	// Acquire singleton lock before any watcher initialisation.
	relFn, lockErr := AcquireWatchLock(w.dataDir)
	if lockErr != nil {
		return lockErr
	}

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		relFn()
		return fmt.Errorf("fsnotify.NewWatcher: %w", err)
	}

	// Load token list.
	tokens, err := w.loadTokens()
	if err != nil {
		_ = fsw.Close()
		return fmt.Errorf("load tokens: %w", err)
	}

	// Build path → DeployedToken map and add parent directories to watcher.
	// We watch the parent directory (not the file directly) because fsnotify
	// on Windows watches directories; events are filtered to the token filename.
	pathMap := make(map[string]DeployedToken) // absolute file path → token
	dirsSeen := make(map[string]bool)
	for _, tok := range tokens {
		if tok.DeployedPath == "" {
			continue
		}
		abs := filepath.Clean(tok.DeployedPath)
		pathMap[abs] = tok
		dir := filepath.Dir(abs)
		if !dirsSeen[dir] {
			if err := fsw.Add(dir); err != nil {
				// Directory may not exist — skip silently.
				continue
			}
			dirsSeen[dir] = true
		}
	}

	w.watcher = fsw
	w.done = make(chan struct{})
	w.count = len(pathMap)
	w.release = relFn
	w.running = true

	go w.eventLoop(fsw, pathMap)
	return nil
}

// ----------------------------------------------------------------------------
// stop
// ----------------------------------------------------------------------------

func (w *windowsWatcher) stop() {
	w.mu.Lock()
	if !w.running {
		w.mu.Unlock()
		return
	}
	fsw := w.watcher
	done := w.done
	w.mu.Unlock()

	// Closing the fsnotify watcher unblocks the event loop's select.
	_ = fsw.Close()
	<-done

	w.mu.Lock()
	if w.release != nil {
		w.release()
		w.release = nil
	}
	w.running = false
	w.count = 0
	w.watcher = nil
	w.mu.Unlock()
}

// ----------------------------------------------------------------------------
// status
// ----------------------------------------------------------------------------

func (w *windowsWatcher) status() WatcherStatus {
	w.mu.Lock()
	defer w.mu.Unlock()
	return WatcherStatus{Running: w.running, Watching: w.count}
}

// ----------------------------------------------------------------------------
// eventLoop
// ----------------------------------------------------------------------------

func (w *windowsWatcher) eventLoop(fsw *fsnotify.Watcher, pathMap map[string]DeployedToken) {
	defer close(w.done)

	// Top-level panic recovery: if something unexpected panics outside the
	// per-dispatch recovery below (e.g., in the select/ticker code), log it to
	// stderr so the crash is visible even when stderr is redirected to a file.
	// The event loop exits after logging, which closes w.done and allows
	// stop() to unblock. The watcher.pid lock is released by stop().
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "decoyd watch: fatal panic in event loop: %v\n", r)
		}
	}()

	debounce := make(map[string]*debounceEntry)
	rateMap := make(map[string]*rateEntry)

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case ev, ok := <-fsw.Events:
			if !ok {
				// Watcher closed — exit cleanly.
				return
			}

			abs := filepath.Clean(ev.Name)
			tok, watched := pathMap[abs]
			if !watched {
				// Event is for a different file in the same directory — ignore.
				continue
			}

			evType := fsnotifyEventType(ev.Op)
			if evType == "" {
				continue
			}

			// Reset (or create) debounce entry for this path.
			debounce[abs] = &debounceEntry{
				token:  tok,
				event:  evType,
				expiry: time.Now().Add(w.cfg.DebounceDuration),
			}

		case fsErr, ok := <-fsw.Errors:
			if !ok {
				return
			}
			// Log watcher errors to stderr — they were previously swallowed
			// silently which made debugging impossible. Non-fatal: watcher continues.
			if fsErr != nil {
				fmt.Fprintf(os.Stderr, "decoyd watch: fsnotify error: %v\n", fsErr)
			}

		case now := <-ticker.C:
			// Fire debounced events that have expired.
			for path, e := range debounce {
				if now.After(e.expiry) {
					w.safeDispatch(rateMap, e.token, e.event, now)
					delete(debounce, path)
				}
			}
		}
	}
}

// fsnotifyEventType maps an fsnotify Op to a human-readable event type.
// Returns "" for ops we don't forward (Create, Chmod).
//
// NOTE: Pure read-only access (GENERIC_READ) is NOT detectable via
// ReadDirectoryChangesW and therefore never appears as an fsnotify event.
// This is a documented v1 limitation — see package comment above.
func fsnotifyEventType(op fsnotify.Op) string {
	switch {
	case op.Has(fsnotify.Write):
		return "write"
	case op.Has(fsnotify.Rename):
		return "rename"
	case op.Has(fsnotify.Remove):
		return "delete"
	default:
		return "" // Create, Chmod — not forwarded
	}
}

// safeDispatch wraps dispatch with a per-event panic recovery.
// A panic inside dispatch (e.g., nil pointer in an alert implementation,
// unexpected HTTP response structure) is caught and logged to stderr.
// The event loop continues running — one bad alert delivery does not kill
// the watcher process.
func (w *windowsWatcher) safeDispatch(rateMap map[string]*rateEntry, tok DeployedToken, evType string, now time.Time) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "decoyd watch: panic dispatching event for token %s: %v\n", tok.ID, r)
		}
	}()
	w.dispatch(rateMap, tok, evType, now)
}

// ----------------------------------------------------------------------------
// dispatch — rate-limit, quiet-hours, triglog, alert
// ----------------------------------------------------------------------------

func (w *windowsWatcher) dispatch(rateMap map[string]*rateEntry, tok DeployedToken, evType string, now time.Time) {
	evID := newWindowsEventID()

	ev := triglog.TriggerEvent{
		ID:          evID,
		TokenID:     tok.ID,
		TokenType:   tok.Type,
		Path:        tok.DeployedPath,
		TriggeredAt: now.UTC(),
		EventType:   evType,
		Status:      triglog.TriggerPending,
	}

	// Quiet hours check.
	if w.cfg.inQuietHours(now) {
		ev.Status = triglog.TriggerQuietHours
		_ = triglog.Append(w.dataDir, ev)
		return
	}

	// Rate-limit check.
	limit := w.cfg.RateLimit
	if limit <= 0 {
		limit = 5
	}
	re := rateMap[tok.ID]
	if re == nil || now.After(re.windowEnd) {
		rateMap[tok.ID] = &rateEntry{count: 1, windowEnd: now.Add(time.Hour)}
	} else {
		re.count++
		if re.count > limit {
			ev.Status = triglog.TriggerRateLimited
			_ = triglog.Append(w.dataDir, ev)
			return
		}
	}

	// Write pending record before dispatch.
	_ = triglog.Append(w.dataDir, ev)

	// Resolve alert channel.
	alertCfg, err := alert.Load(w.dataDir)
	if err != nil {
		ev.Status = triglog.TriggerFailed
		ev.AlertError = "failed to load alert config"
		_ = triglog.Append(w.dataDir, ev)
		return
	}
	ch, ok := alertCfg.ChannelForToken(tok.AlertChannelID)
	if !ok {
		ev.Status = triglog.TriggerFailed
		ev.AlertError = "no alert channel configured"
		_ = triglog.Append(w.dataDir, ev)
		return
	}

	a, err := alert.NewAlerter(ch)
	if err != nil {
		ev.Status = triglog.TriggerFailed
		ev.AlertError = alert.SanitizeErrString(err)
		_ = triglog.Append(w.dataDir, ev)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	payload := alert.AlertPayload{
		TokenID:     tok.ID,
		TokenType:   tok.Type,
		Path:        tok.DeployedPath,
		TriggeredAt: now.UTC(),
		Detail:      "file " + evType,
	}
	if sendErr := a.Send(ctx, payload); sendErr != nil {
		ev.Status = triglog.TriggerFailed
		ev.AlertError = alert.SanitizeErrString(sendErr)
	} else {
		ev.Status = triglog.TriggerSent
	}
	_ = triglog.Append(w.dataDir, ev)
}

// ----------------------------------------------------------------------------
// loadTokens — bbolt or snapshot file depending on mode
// ----------------------------------------------------------------------------

func (w *windowsWatcher) loadTokens() ([]DeployedToken, error) {
	if w.st != nil {
		ts, err := w.st.ListTokens()
		if err != nil {
			return nil, err
		}
		out := make([]DeployedToken, 0, len(ts))
		for _, t := range ts {
			if t.DeployedPath != "" {
				out = append(out, DeployedToken{
					ID:             t.ID,
					Type:           t.Type,
					DeployedPath:   t.DeployedPath,
					AlertChannelID: t.AlertChannelID,
				})
			}
		}
		return out, nil
	}
	return ReadDeployedSnapshot(w.dataDir)
}

// ----------------------------------------------------------------------------
// helpers
// ----------------------------------------------------------------------------

func newWindowsEventID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
