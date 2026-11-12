//go:build linux

// watch_linux.go — inotify-based file watcher for Linux.
//
// Architecture:
//   - InotifyInit1(IN_CLOEXEC) creates the inotify fd.
//   - Pipe2(O_CLOEXEC|O_NONBLOCK) provides the self-pipe stop mechanism.
//   - unix.Poll blocks on both fds with a 1-second timeout; the timeout
//     allows the event loop goroutine to exit cleanly even if Poll blocks.
//   - One inotify watch per deployed token file; watches are added at start
//     and after IN_MOVE_SELF / IN_DELETE_SELF (file replaced atomically).
//   - Token list source: st != nil → bbolt; st == nil → deployed_tokens.json.
//
// Debounce / rate-limit / quiet-hours:
//   - Debounce: per-path timer reset on every event; event is forwarded only
//     after DebounceDuration of silence.
//   - Rate limit: per-token sliding window counter (max RateLimit per hour).
//   - Quiet hours: events during quiet hours are logged as quiet_hours but
//     no alert is sent.
package watch

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/arjunjaincs/decoyd/internal/alert"
	"github.com/arjunjaincs/decoyd/internal/attribution"
	"github.com/arjunjaincs/decoyd/internal/sanitize"
	"github.com/arjunjaincs/decoyd/internal/store"
	"github.com/arjunjaincs/decoyd/internal/triglog"
)

// ----------------------------------------------------------------------------
// linuxWatcher
// ----------------------------------------------------------------------------

type linuxWatcher struct {
	st      *store.Store
	dataDir string
	cfg     WatcherConfig

	mu   sync.Mutex
	wdMu sync.RWMutex // protects wdMap against concurrent reads/writes

	// wdMap maps inotify watch descriptor → deployed token.
	// Protected by wdMu for all concurrent accesses.
	wdMap map[int32]DeployedToken

	// dirWdMap maps inotify dir watch descriptor → dir path.
	// Protected by wdMu for all concurrent accesses.
	dirWdMap map[int32]string
	// dirTokens maps dir path → (basename → DeployedToken) for tokens in that dir.
	// Protected by wdMu for all concurrent accesses.
	dirTokens map[string]map[string]DeployedToken

	running bool
	count   int    // number of files currently watched
	release func() // releases the watcher.pid lock; nil when not running

	// inotify and self-pipe fds; both -1 when not running.
	inoFD int
	stopR int // read end of stop pipe
	stopW int // write end of stop pipe

	// stopCh is closed by stop() to signal the event loop.
	stopCh chan struct{}
	// done is closed by the event loop goroutine when it exits.
	done chan struct{}

	// O2: per-token fanotify active set (when true, inotify access/open suppressed).
	suppressedMu       sync.RWMutex
	fanotifySuppressed map[string]bool
	onTokensLoaded     func([]DeployedToken)
	onFileCreate       func(DeployedToken)

	// Per-watcher rate-limit state for fanotify dispatches.
	fanRateMu  sync.Mutex
	fanRateMap map[string]*rateEntry
}

// ----------------------------------------------------------------------------
// start
// ----------------------------------------------------------------------------

// systemDataDirPath is the root-owned state directory for the Tier 1 daemon (O4).
const (
	systemDataDirPath   = "/var/lib/decoyd"
	systemConfigDirPath = "/etc/decoyd"
)

func (w *linuxWatcher) setFanotifySuppressed(tokenID string, active bool) {
	w.suppressedMu.Lock()
	if w.fanotifySuppressed == nil {
		w.fanotifySuppressed = make(map[string]bool)
	}
	if active {
		w.fanotifySuppressed[tokenID] = true
	} else {
		delete(w.fanotifySuppressed, tokenID)
	}
	w.suppressedMu.Unlock()
}

func (w *linuxWatcher) isFanotifySuppressed(tokenID string) bool {
	w.suppressedMu.RLock()
	active := w.fanotifySuppressed[tokenID]
	w.suppressedMu.RUnlock()
	return active
}

// validateTokenPathO5 enforces O5 on a snapshot-loaded path:
//  1. Lstat the path.
//  2. If missing on disk, return (exists=false, ok=true) so parent-dir watches can detect creation.
//  3. Refuse symlinks and non-regular files (log & skip).
//  4. If tok.OwnerUID is set, require st.Uid == *tok.OwnerUID (log & skip).
func validateTokenPathO5(tok DeployedToken) (exists bool, ok bool) {
	fi, err := os.Lstat(tok.DeployedPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, true
		}
		log.Printf("decoyd watch: skipping token %s (%s): lstat: %v (O5)", tok.ID, tok.DeployedPath, err)
		return false, false
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
		log.Printf("decoyd watch: skipping token %s (%s): symlink or non-regular file refused (O5)", tok.ID, tok.DeployedPath)
		return true, false
	}
	if tok.OwnerUID != nil {
		st, sysOK := fi.Sys().(*syscall.Stat_t)
		if !sysOK {
			log.Printf("decoyd watch: skipping token %s (%s): cannot inspect file uid (O5)", tok.ID, tok.DeployedPath)
			return true, false
		}
		if st.Uid != *tok.OwnerUID {
			log.Printf("decoyd watch: skipping token %s (%s): owner uid %d != snapshot uid %d (O5)", tok.ID, tok.DeployedPath, st.Uid, *tok.OwnerUID)
			return true, false
		}
	}
	return true, true
}

func loadAlertConfigForWatcher(dataDir string) (alert.AlertConfig, error) {
	if filepath.Clean(dataDir) == systemDataDirPath && os.Geteuid() == 0 {
		sysCfgPath := filepath.Join(systemConfigDirPath, "alert_config.json")
		if _, err := os.Stat(sysCfgPath); err == nil {
			return alert.Load(systemConfigDirPath)
		}
	}
	return alert.Load(dataDir)
}

func appendTriglogForWatcher(dataDir string, ev triglog.TriggerEvent) {
	_ = triglog.Append(dataDir, ev)
	if filepath.Clean(dataDir) == systemDataDirPath && os.Geteuid() == 0 {
		// O4: /var/lib/decoyd triggers.jsonl contains no secrets and must be readable by unprivileged CLI/TUI.
		_ = os.Chmod(filepath.Join(dataDir, "triggers.jsonl"), 0o644) // #nosec G302 -- triggers.jsonl holds no secrets (O4/O8) and must be readable by unprivileged decoyd events
	}
}

func (w *linuxWatcher) start() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.running {
		return fmt.Errorf("watcher already running")
	}

	// Acquire singleton lock before any inotify initialisation.
	relFn, lockErr := AcquireWatchLock(w.dataDir)
	if lockErr != nil {
		return lockErr
	}

	// Create inotify instance.
	inoFD, err := unix.InotifyInit1(unix.IN_CLOEXEC)
	if err != nil {
		relFn()
		return fmt.Errorf("inotify_init1: %w", err)
	}

	// Create self-pipe for clean shutdown.
	pipefd := [2]int{}
	if err := unix.Pipe2(pipefd[:], unix.O_CLOEXEC|unix.O_NONBLOCK); err != nil {
		_ = unix.Close(inoFD)
		relFn()
		return fmt.Errorf("pipe2: %w", err)
	}

	w.inoFD = inoFD
	w.stopR = pipefd[0]
	w.stopW = pipefd[1]
	w.stopCh = make(chan struct{})
	w.done = make(chan struct{})
	w.release = relFn
	w.running = true

	// Load token list.
	tokens, err := w.loadTokens()
	if err != nil {
		w.closeFDs()
		if w.release != nil {
			w.release()
			w.release = nil
		}
		w.running = false
		return fmt.Errorf("load tokens: %w", err)
	}

	// Run optional pre-loop hook (used by Tier 1 hybrid to attach fanotify marks
	// and populate suppression state before eventLoop starts).
	if w.onTokensLoaded != nil {
		w.onTokensLoaded(tokens)
	}

	// Add one inotify watch per valid deployed path (O5 checked before watching).
	const mask = unix.IN_OPEN | unix.IN_ACCESS | unix.IN_MODIFY |
		unix.IN_MOVE_SELF | unix.IN_DELETE_SELF
	w.wdMap = make(map[int32]DeployedToken) // wd → token; protected by wdMu
	validTokens := make([]DeployedToken, 0, len(tokens))
	for _, tok := range tokens {
		if tok.DeployedPath == "" {
			continue
		}
		exists, ok := validateTokenPathO5(tok)
		if !ok {
			continue // O5 refusal: skipped and logged
		}
		validTokens = append(validTokens, tok)
		if !exists {
			continue // parent-dir watch will pick up creation
		}
		wd, err := unix.InotifyAddWatch(w.inoFD, tok.DeployedPath, mask)
		if err != nil {
			continue
		}
		w.wdMap[int32(wd)] = tok // #nosec G115 -- InotifyAddWatch returns int; Linux WDs are positive and bounded within int32 range
	}

	w.count = len(w.wdMap)

	// Add parent-directory inotify watches for valid token directories.
	const dirMask = unix.IN_CREATE | unix.IN_MOVED_TO | unix.IN_DELETE | unix.IN_MOVED_FROM
	w.dirWdMap = make(map[int32]string)
	w.dirTokens = make(map[string]map[string]DeployedToken)
	for _, tok := range validTokens {
		dir := filepath.Dir(tok.DeployedPath)
		base := filepath.Base(tok.DeployedPath)
		if _, exists := w.dirTokens[dir]; !exists {
			w.dirTokens[dir] = make(map[string]DeployedToken)
			wd, dirErr := unix.InotifyAddWatch(w.inoFD, dir, dirMask)
			if dirErr == nil {
				w.dirWdMap[int32(wd)] = dir // #nosec G115 -- InotifyAddWatch WD positive and in int32 range
			}
		}
		w.dirTokens[dir][base] = tok
	}

	go w.eventLoop(mask)
	return nil
}

// ----------------------------------------------------------------------------
// stop
// ----------------------------------------------------------------------------

func (w *linuxWatcher) stop() {
	w.mu.Lock()
	if !w.running {
		w.mu.Unlock()
		return
	}
	w.mu.Unlock()

	// Signal the event loop via the stop channel and the self-pipe.
	close(w.stopCh)
	_, _ = unix.Write(w.stopW, []byte{0})

	// Wait for the event loop to exit.
	<-w.done

	w.mu.Lock()
	w.closeFDs()
	if w.release != nil {
		w.release()
		w.release = nil
	}
	w.running = false
	w.count = 0
	w.mu.Unlock()
}

func (w *linuxWatcher) closeFDs() {
	if w.inoFD >= 0 {
		_ = unix.Close(w.inoFD)
		w.inoFD = -1
	}
	if w.stopR >= 0 {
		_ = unix.Close(w.stopR)
		w.stopR = -1
	}
	if w.stopW >= 0 {
		_ = unix.Close(w.stopW)
		w.stopW = -1
	}
}

// ----------------------------------------------------------------------------
// status
// ----------------------------------------------------------------------------

func (w *linuxWatcher) status() WatcherStatus {
	w.mu.Lock()
	defer w.mu.Unlock()
	return WatcherStatus{Running: w.running, Watching: w.count}
}

// ----------------------------------------------------------------------------
// eventLoop
// ----------------------------------------------------------------------------

func (w *linuxWatcher) eventLoop(mask uint32) {
	defer close(w.done)

	// Top-level panic recovery: logs fatal panics to stderr so the crash is
	// visible even when stderr is redirected to a file. The event loop exits
	// after logging, which closes w.done and allows stop() to unblock.
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "decoyd watch: fatal panic in event loop: %v\n", r)
		}
	}()

	// Inotify event buffer: 4096 bytes, each event is ≥ unix.SizeofInotifyEvent bytes.
	buf := make([]byte, 4096)

	debounce := make(map[string]*debounceEntry) // deployedPath → entry
	rateMap := make(map[string]*rateEntry)      // tokenID → entry
	var debounceTimer <-chan time.Time

	fds := []unix.PollFd{
		{Fd: int32(w.inoFD), Events: unix.POLLIN}, // #nosec G115 -- fd is non-negative, bounded by RLIMIT_NOFILE (max ~1M), safely fits int32
		{Fd: int32(w.stopR), Events: unix.POLLIN}, // #nosec G115 -- same: pipe fd is non-negative and fits int32
	}

	for {
		// Fire any debounced events that have expired.
		now := time.Now()
		var earliest time.Time
		for path, e := range debounce {
			if now.After(e.expiry) {
				w.safeDispatch(rateMap, e.token, e.event, now)
				delete(debounce, path)
			} else if earliest.IsZero() || e.expiry.Before(earliest) {
				earliest = e.expiry
			}
		}
		if !earliest.IsZero() {
			debounceTimer = time.After(time.Until(earliest))
		} else {
			debounceTimer = nil
		}

		// Poll: 1-second timeout so we can re-check debounce timers.
		timeout := 1000 // ms
		if debounceTimer != nil {
			// Use a shorter timeout so we don't overshoot the debounce deadline.
			ms := int(time.Until(earliest).Milliseconds()) + 10
			if ms < 50 {
				ms = 50
			}
			if ms < timeout {
				timeout = ms
			}
		}
		n, err := unix.Poll(fds, timeout)
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			return // unexpected error — exit cleanly
		}

		// Check stop pipe.
		if fds[1].Revents&unix.POLLIN != 0 {
			return
		}

		select {
		case <-w.stopCh:
			return
		default:
		}

		if n == 0 {
			// Timeout — re-loop to fire debounce timers.
			continue
		}

		if fds[0].Revents&unix.POLLIN == 0 {
			continue
		}

		// Read inotify events.
		nr, err := unix.Read(w.inoFD, buf)
		if err != nil {
			continue
		}

		offset := 0
		for offset+unix.SizeofInotifyEvent <= nr {
			// Parse inotify_event header using unsafe.
			raw := (*unix.InotifyEvent)(unsafe.Pointer(&buf[offset])) // #nosec G103 -- required for inotify event parsing; buf is aligned and bounds-checked
			evMask := raw.Mask
			wd := raw.Wd
			nameLen := int(raw.Len)
			offset += unix.SizeofInotifyEvent + nameLen

			w.wdMu.RLock()
			tok, ok := w.wdMap[wd]
			dirPath, isDirWd := w.dirWdMap[wd]
			w.wdMu.RUnlock()

			// Handle parent-directory events (IN_CREATE/IN_MOVED_TO/IN_DELETE/IN_MOVED_FROM).
			if isDirWd {
				// Extract the filename from the name bytes that follow the event header.
				// After offset was advanced by SizeofInotifyEvent+nameLen, the name bytes
				// are at buf[offset-nameLen : offset].
				var name string
				if nameLen > 0 {
					nameBuf := buf[offset-nameLen : offset]
					// Filename is null-padded; find the first null terminator.
					for i, b := range nameBuf {
						if b == 0 {
							name = string(nameBuf[:i])
							break
						}
					}
					if name == "" {
						name = string(nameBuf) // no null terminator found, use all bytes
					}
				}
				if name != "" {
					w.wdMu.RLock()
					dirToks := w.dirTokens[dirPath]
					w.wdMu.RUnlock()
					if dirTok, hasTok := dirToks[name]; hasTok {
						fullPath := filepath.Join(dirPath, name)
						if evMask&(unix.IN_CREATE|unix.IN_MOVED_TO) != 0 {
							// O5 check before watching newly created/moved file.
							if ex, ok := validateTokenPathO5(dirTok); ex && ok {
								newWD, addErr := unix.InotifyAddWatch(w.inoFD, fullPath, mask)
								if addErr == nil {
									w.wdMu.Lock()
									w.wdMap[int32(newWD)] = dirTok // #nosec G115 -- WD positive
									w.mu.Lock()
									w.count = len(w.wdMap)
									w.mu.Unlock()
									w.wdMu.Unlock()
								}
								if cb := w.onFileCreate; cb != nil {
									cb(dirTok)
								}
							}
						} else if evMask&(unix.IN_DELETE|unix.IN_MOVED_FROM) != 0 {
							// File removed from directory - remove its file-level watch if present.
							w.wdMu.Lock()
							for fileWd, fileTok := range w.wdMap {
								if fileTok.DeployedPath == fullPath {
									_, _ = unix.InotifyRmWatch(w.inoFD, uint32(fileWd)) // #nosec G115 -- fileWd is an active wdMap key returned positive by InotifyAddWatch
									delete(w.wdMap, fileWd)
									break
								}
							}
							w.mu.Lock()
							w.count = len(w.wdMap)
							w.mu.Unlock()
							w.wdMu.Unlock()
						}
					}
				}
				continue // dir-level events never dispatch alerts themselves
			}

			if !ok {
				continue
			}

			evType := inotifyEventType(evMask)
			if evType == "" {
				continue
			}
			// O2: while token's fanotify mark is active, suppress inotify open/access alerts.
			if evType == "access" && w.isFanotifySuppressed(tok.ID) {
				continue
			}

			// Reset debounce timer for this path.
			debounce[tok.DeployedPath] = &debounceEntry{
				token:  tok,
				event:  evType,
				expiry: time.Now().Add(w.cfg.DebounceDuration),
			}

			// If file was moved/deleted, remove its watch and re-add after a delay.
			if evMask&(unix.IN_MOVE_SELF|unix.IN_DELETE_SELF) != 0 {
				_, _ = unix.InotifyRmWatch(w.inoFD, uint32(wd)) // #nosec G115 -- wd is int32 from wdMap key; WDs we added are always positive so sign bit is 0
				w.wdMu.Lock()
				delete(w.wdMap, wd)
				w.mu.Lock()
				w.count = len(w.wdMap)
				w.mu.Unlock()
				w.wdMu.Unlock()
				// Attempt to re-add the watch (e.g. after atomic file replace).
				// Snapshot inoFD under mu to avoid a race with closeFDs().
				w.mu.Lock()
				snapFD := w.inoFD
				w.mu.Unlock()
				go func(t DeployedToken, fd int) {
					select {
					case <-w.stopCh:
						return // watcher shutting down; don't use fd
					case <-time.After(100 * time.Millisecond):
					}
					// Re-check that the watcher is still running and fd unchanged.
					w.mu.Lock()
					if !w.running || w.inoFD != fd || fd < 0 {
						w.mu.Unlock()
						return
					}
					w.mu.Unlock()
					if ex, ok := validateTokenPathO5(t); ex && ok {
						newWD, err := unix.InotifyAddWatch(fd, t.DeployedPath, mask)
						if err == nil {
							w.wdMu.Lock()
							w.wdMap[int32(newWD)] = t // #nosec G115 -- InotifyAddWatch returns int; Linux WDs are positive and bounded within int32 range
							w.mu.Lock()
							w.count = len(w.wdMap)
							w.mu.Unlock()
							w.wdMu.Unlock()
						}
						if cb := w.onFileCreate; cb != nil {
							cb(t)
						}
					}
				}(tok, snapFD)
			}
		}
	}
}

// inotifyEventType maps an inotify event mask to a human-readable event type.
func inotifyEventType(mask uint32) string {
	switch {
	case mask&(unix.IN_OPEN|unix.IN_ACCESS) != 0:
		return "access"
	case mask&unix.IN_MODIFY != 0:
		return "write"
	case mask&unix.IN_MOVE_SELF != 0:
		return "rename"
	case mask&unix.IN_DELETE_SELF != 0:
		return "delete"
	default:
		return ""
	}
}

// sendSuppressionAlert sends a single alert informing the user that further
// events for this token are suppressed until the current rate-limit window expires.
// It is called at most once per token per window (guarded by rateEntry.alertSent).
func (w *linuxWatcher) sendSuppressionAlert(tok DeployedToken, now time.Time) {
	alertCfg, err := loadAlertConfigForWatcher(w.dataDir)
	if err != nil {
		return
	}
	ch, ok := alertCfg.ChannelForToken(tok.AlertChannelID)
	if !ok {
		return
	}
	a, err := alert.NewAlerter(ch)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	payload := alert.AlertPayload{
		TokenID:     sanitize.Sanitize(tok.ID, 64),
		TokenType:   sanitize.Sanitize(tok.Type, 64),
		Path:        sanitize.Sanitize(tok.DeployedPath, 512),
		TriggeredAt: now.UTC(),
		Detail:      "rate limit reached; further events suppressed until window expires",
	}
	_ = a.Send(ctx, payload)
}

// safeDispatch wraps dispatch with a per-event panic recovery.
// A panic inside dispatch (e.g., nil pointer in an alert implementation,
// unexpected HTTP response structure) is caught and logged to stderr.
// The event loop continues running — one bad alert delivery does not kill
// the watcher process.
func (w *linuxWatcher) safeDispatch(rateMap map[string]*rateEntry, tok DeployedToken, evType string, now time.Time) {
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

func (w *linuxWatcher) dispatch(rateMap map[string]*rateEntry, tok DeployedToken, evType string, now time.Time) {
	// Generate a unique event ID.
	evID := newEventID()

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
		appendTriglogForWatcher(w.dataDir, ev)
		return
	}

	// Rate-limit check: max RateLimit events per token per hour.
	limit := w.cfg.RateLimit
	if limit <= 0 {
		limit = 5
	}
	re := rateMap[tok.ID]
	if re == nil || now.After(re.windowEnd) {
		// New window: reset state.
		rateMap[tok.ID] = &rateEntry{count: 1, windowEnd: now.Add(time.Hour)}
	} else {
		re.count++
		if re.count > limit {
			ev.Status = triglog.TriggerRateLimited
			appendTriglogForWatcher(w.dataDir, ev)
			// Send ONE suppression alert per token per window.
			if !re.alertSent {
				re.alertSent = true
				w.sendSuppressionAlert(tok, now)
			}
			return
		}
	}

	// Write pending record before dispatch (durability).
	appendTriglogForWatcher(w.dataDir, ev)

	// Resolve alert channel.
	alertCfg, err := loadAlertConfigForWatcher(w.dataDir)
	if err != nil {
		ev.Status = triglog.TriggerFailed
		ev.AlertError = "failed to load alert config"
		appendTriglogForWatcher(w.dataDir, ev)
		return
	}
	ch, ok := alertCfg.ChannelForToken(tok.AlertChannelID)
	if !ok {
		// No channel configured — log and continue, not an error.
		ev.Status = triglog.TriggerFailed
		ev.AlertError = "no alert channel configured"
		appendTriglogForWatcher(w.dataDir, ev)
		return
	}

	a, err := alert.NewAlerter(ch)
	if err != nil {
		ev.Status = triglog.TriggerFailed
		ev.AlertError = alert.SanitizeErrString(err)
		appendTriglogForWatcher(w.dataDir, ev)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	// Sanitize all external strings before building AlertPayload (Rule 13).
	payload := alert.AlertPayload{
		TokenID:     sanitize.Sanitize(tok.ID, 64),
		TokenType:   sanitize.Sanitize(tok.Type, 64),
		Path:        sanitize.Sanitize(tok.DeployedPath, 512),
		TriggeredAt: now.UTC(),
		Detail:      sanitize.Sanitize("file "+evType, 128),
	}
	if sendErr := a.Send(ctx, payload); sendErr != nil {
		ev.Status = triglog.TriggerFailed
		ev.AlertError = alert.SanitizeErrString(sendErr)
	} else {
		ev.Status = triglog.TriggerSent
	}
	appendTriglogForWatcher(w.dataDir, ev)
}

// ----------------------------------------------------------------------------
// dispatchFanotifyEvent — called from the fanotify dispatch goroutine (T2)
// ----------------------------------------------------------------------------

// dispatchFanotifyEvent dispatches an alert for a fanotify-detected access.
// Called from hybridLinuxWatcher.runFanotifyDispatch.
// attr may be nil when attribution data is unavailable.
func (w *linuxWatcher) dispatchFanotifyEvent(tok DeployedToken, pid int32, attr *attribution.Attribution) {
	now := time.Now()

	// Generate a unique event ID.
	evID := newEventID()

	ev := triglog.TriggerEvent{
		ID:          evID,
		TokenID:     tok.ID,
		TokenType:   tok.Type,
		Path:        tok.DeployedPath,
		TriggeredAt: now.UTC(),
		EventType:   "access",
		Status:      triglog.TriggerPending,
		WatcherTier: "tier1",
		Attribution: attr,
	}

	// Quiet hours check.
	if w.cfg.inQuietHours(now) {
		ev.Status = triglog.TriggerQuietHours
		appendTriglogForWatcher(w.dataDir, ev)
		return
	}

	// Rate-limit check.
	limit := w.cfg.RateLimit
	if limit <= 0 {
		limit = 5
	}
	w.fanRateMu.Lock()
	if w.fanRateMap == nil {
		w.fanRateMap = make(map[string]*rateEntry)
	}
	re := w.fanRateMap[tok.ID]
	rateExceeded := false
	if re == nil || now.After(re.windowEnd) {
		w.fanRateMap[tok.ID] = &rateEntry{count: 1, windowEnd: now.Add(time.Hour)}
	} else {
		re.count++
		if re.count > limit {
			rateExceeded = true
		}
	}
	w.fanRateMu.Unlock()
	if rateExceeded {
		ev.Status = triglog.TriggerRateLimited
		appendTriglogForWatcher(w.dataDir, ev)
		return
	}

	appendTriglogForWatcher(w.dataDir, ev)

	alertCfg, err := loadAlertConfigForWatcher(w.dataDir)
	if err != nil {
		ev.Status = triglog.TriggerFailed
		ev.AlertError = "failed to load alert config"
		appendTriglogForWatcher(w.dataDir, ev)
		return
	}
	ch, ok := alertCfg.ChannelForToken(tok.AlertChannelID)
	if !ok {
		ev.Status = triglog.TriggerFailed
		ev.AlertError = "no alert channel configured"
		appendTriglogForWatcher(w.dataDir, ev)
		return
	}
	a, err := alert.NewAlerter(ch)
	if err != nil {
		ev.Status = triglog.TriggerFailed
		ev.AlertError = alert.SanitizeErrString(err)
		appendTriglogForWatcher(w.dataDir, ev)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	detail := fmt.Sprintf("fanotify access pid=%d", pid)
	payload := alert.AlertPayload{
		TokenID:     sanitize.Sanitize(tok.ID, 64),
		TokenType:   sanitize.Sanitize(tok.Type, 64),
		Path:        sanitize.Sanitize(tok.DeployedPath, 512),
		TriggeredAt: now.UTC(),
		Detail:      sanitize.Sanitize(detail, 128),
	}
	if sendErr := a.Send(ctx, payload); sendErr != nil {
		ev.Status = triglog.TriggerFailed
		ev.AlertError = alert.SanitizeErrString(sendErr)
	} else {
		ev.Status = triglog.TriggerSent
	}
	appendTriglogForWatcher(w.dataDir, ev)
}

const fanotifyOverflowRateKey = "__fanotify_overflow__"

// safeDispatchFanotifyOverflow wraps dispatchFanotifyOverflow with panic
// recovery so an alert sink failure cannot terminate the fanotify loop.
func (w *linuxWatcher) safeDispatchFanotifyOverflow() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "decoyd watch: panic dispatching overflow alert: %v\n", r)
		}
	}()
	w.dispatchFanotifyOverflow(time.Now())
}

// dispatchFanotifyOverflow sends at most one sanitized "events lost" alert
// per hour window when the kernel reports FAN_Q_OVERFLOW, logs the outcome,
// and lets the watcher continue running.
func (w *linuxWatcher) dispatchFanotifyOverflow(now time.Time) {
	if w.cfg.inQuietHours(now) {
		return
	}

	w.fanRateMu.Lock()
	if w.fanRateMap == nil {
		w.fanRateMap = make(map[string]*rateEntry)
	}
	re := w.fanRateMap[fanotifyOverflowRateKey]
	rateLimited := false
	if re == nil || now.After(re.windowEnd) {
		w.fanRateMap[fanotifyOverflowRateKey] = &rateEntry{count: 1, windowEnd: now.Add(time.Hour)}
	} else {
		re.count++
		rateLimited = true
	}
	w.fanRateMu.Unlock()
	if rateLimited {
		log.Printf("decoyd fanotify: overflow alert suppressed by rate limit")
		return
	}

	alertCfg, err := loadAlertConfigForWatcher(w.dataDir)
	if err != nil {
		log.Printf("decoyd fanotify: overflow alert load config failed: %v", err)
		return
	}
	ch, ok := alertCfg.DefaultChannel()
	if !ok {
		log.Printf("decoyd fanotify: overflow alert skipped (no alert channel configured)")
		return
	}
	a, err := alert.NewAlerter(ch)
	if err != nil {
		log.Printf("decoyd fanotify: overflow alert init failed: %s", alert.SanitizeErrString(err))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	payload := alert.AlertPayload{
		TokenID:     sanitize.Sanitize("fanotify-overflow", 64),
		TokenType:   sanitize.Sanitize("system", 64),
		Path:        sanitize.Sanitize(w.dataDir, 512),
		TriggeredAt: now.UTC(),
		Detail:      sanitize.Sanitize("fanotify event queue overflow (FAN_Q_OVERFLOW): events lost", 128),
	}
	if sendErr := a.Send(ctx, payload); sendErr != nil {
		log.Printf("decoyd fanotify: overflow alert delivery failed: %s", alert.SanitizeErrString(sendErr))
		return
	}
	log.Printf("decoyd fanotify: sent FAN_Q_OVERFLOW events-lost alert")
}

// ----------------------------------------------------------------------------
// loadTokens — bbolt or snapshot file depending on mode
// ----------------------------------------------------------------------------

func (w *linuxWatcher) loadTokens() ([]DeployedToken, error) {
	if w.st != nil {
		// TUI-embedded mode: read from bbolt.
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
	// Headless mode: read from deployed_tokens.json.
	return ReadDeployedSnapshot(w.dataDir)
}

// ----------------------------------------------------------------------------
// helpers
// ----------------------------------------------------------------------------

func newEventID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ----------------------------------------------------------------------------
// reload — re-read token list and sync inotify watches
// ----------------------------------------------------------------------------

// reload loads the current token list and diffs it against the active w.wdMap,
// adding inotify watches for newly deployed tokens and removing watches for
// tokens that no longer exist. Thread-safe; safe to call from any goroutine.
// Returns nil if the watcher is not running.
func (w *linuxWatcher) reload() error {
	w.mu.Lock()
	running := w.running
	inoFD := w.inoFD
	w.mu.Unlock()
	if !running {
		return nil
	}

	tokens, err := w.loadTokens()
	if err != nil {
		return fmt.Errorf("reload: load tokens: %w", err)
	}

	// Build a path→token map for valid tokens (O5 enforced).
	newByPath := make(map[string]DeployedToken, len(tokens))
	validReloadTokens := make([]DeployedToken, 0, len(tokens))
	for _, t := range tokens {
		if t.DeployedPath == "" {
			continue
		}
		exists, ok := validateTokenPathO5(t)
		if !ok {
			continue
		}
		validReloadTokens = append(validReloadTokens, t)
		if exists {
			newByPath[t.DeployedPath] = t
		}
	}

	const mask = unix.IN_OPEN | unix.IN_ACCESS | unix.IN_MODIFY |
		unix.IN_MOVE_SELF | unix.IN_DELETE_SELF

	w.wdMu.Lock()
	defer w.wdMu.Unlock()

	// Build reverse map: path → wd, for tokens we already watch.
	watchedByPath := make(map[string]int32, len(w.wdMap))
	for wd, tok := range w.wdMap {
		watchedByPath[tok.DeployedPath] = wd
	}

	// Add watches for newly deployed tokens.
	for path, tok := range newByPath {
		if _, already := watchedByPath[path]; already {
			continue // already watched
		}
		wd, addErr := unix.InotifyAddWatch(inoFD, path, mask)
		if addErr != nil {
			continue // file may not exist yet; not fatal
		}
		w.wdMap[int32(wd)] = tok // #nosec G115 -- InotifyAddWatch returns int; WDs are positive
	}

	// Remove watches for tokens no longer in the list.
	for path, wd := range watchedByPath {
		if _, stillExists := newByPath[path]; !stillExists {
			_, _ = unix.InotifyRmWatch(inoFD, uint32(wd)) // #nosec G115 -- wd is int32
			delete(w.wdMap, wd)
		}
	}

	// Sync parent-directory watches for new/removed token directories.
	const dirMask = unix.IN_CREATE | unix.IN_MOVED_TO | unix.IN_DELETE | unix.IN_MOVED_FROM
	newDirTokens := make(map[string]map[string]DeployedToken)
	for _, t := range validReloadTokens {
		d := filepath.Dir(t.DeployedPath)
		if newDirTokens[d] == nil {
			newDirTokens[d] = make(map[string]DeployedToken)
		}
		newDirTokens[d][filepath.Base(t.DeployedPath)] = t
	}
	// dirWdMap/dirTokens are already locked (wdMu.Lock above); update in place.
	dirPathToWd := make(map[string]int32, len(w.dirWdMap))
	for dirWd, dp := range w.dirWdMap {
		dirPathToWd[dp] = dirWd
	}
	// Add watches for newly required directories.
	for d, baseToks := range newDirTokens {
		w.dirTokens[d] = baseToks
		if _, alreadyWatched := dirPathToWd[d]; !alreadyWatched {
			wd, addErr := unix.InotifyAddWatch(inoFD, d, dirMask)
			if addErr == nil {
				w.dirWdMap[int32(wd)] = d // #nosec G115 -- WD positive
			}
		}
	}
	// Remove watches for directories no longer in the token list.
	for dirWd, dp := range w.dirWdMap {
		if _, stillExists := newDirTokens[dp]; !stillExists {
			_, _ = unix.InotifyRmWatch(inoFD, uint32(dirWd)) // #nosec G115 -- dirWd is an active dirWdMap key returned positive by InotifyAddWatch
			delete(w.dirWdMap, dirWd)
			delete(w.dirTokens, dp)
		}
	}

	// Update count under mu.
	w.mu.Lock()
	w.count = len(w.wdMap)
	w.mu.Unlock()

	return nil
}
