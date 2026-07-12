//go:build linux

package watch

import (
	"fmt"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/arjunjaincs/decoyd/internal/store"
	"github.com/arjunjaincs/decoyd/internal/tokens"
	"github.com/arjunjaincs/decoyd/internal/triglog"
)

// inotifyEventSize is the byte-size of the fixed-length inotify_event header.
var inotifyEventSize = int(unsafe.Sizeof(unix.InotifyEvent{}))

// inotifyMask is the set of inotify events we care about.
// IN_OPEN + IN_ACCESS cover read-detection.
const inotifyMask uint32 = unix.IN_OPEN | unix.IN_ACCESS |
	unix.IN_MODIFY | unix.IN_MOVE_SELF | unix.IN_DELETE_SELF

// Watcher watches deployed decoy-token files using raw Linux inotify(7).
//
// Two operating modes:
//
//	TUI-embedded (st != nil): watcher runs inside the TUI process, which
//	  already owns the bbolt write lock.  Token list is read from bbolt.
//	  markTokenTriggered writes back to bbolt via the TUI's store handle.
//
//	Headless (st == nil): watcher runs as "decoyd watch" (systemd service).
//	  Token list is read from deployed_tokens.json.  NO bbolt access.
//	  markTokenTriggered is a no-op.  Triggers are appended to triggers.jsonl.
//
// NOTE: process attribution is not available through standard inotify —
// that requires fanotify(7) with CAP_SYS_ADMIN. The Process field is always
// empty in this implementation.
type Watcher struct {
	mu      sync.RWMutex
	watched map[int]tokens.Token // inotify watch-descriptor → token
	paths   map[string]int       // deployed path → watch-descriptor

	// st is nil in headless mode; non-nil in TUI-embedded mode.
	st      *store.Store
	dataDir string
	cfg     WatchConfig

	debouncer *Debouncer
	rateLim   *RateLimiter

	inoFd       int
	stopPipe    [2]int
	lockRelease func() // releases the watcher.pid singleton lock

	startedAt time.Time
	running   bool
	wg        sync.WaitGroup

	// In-memory recent trigger cache (newest-first, capped at 50).
	trigMu   sync.Mutex
	triggers []triglog.TriggerEvent
}

// New creates a Watcher. Pass st=nil for headless mode (no bbolt access).
func New(st *store.Store, dataDir string) (*Watcher, error) {
	cfg, _ := LoadWatchConfig(dataDir)
	w := &Watcher{
		watched:   make(map[int]tokens.Token),
		paths:     make(map[string]int),
		st:        st,
		dataDir:   dataDir,
		cfg:       cfg,
		debouncer: NewDebouncer(time.Duration(cfg.DebounceSeconds) * time.Second),
		rateLim:   NewRateLimiter(cfg.RateLimitPerHour),
		inoFd:     -1,
	}
	// Pre-load recent trigger history for the status screen.
	past, _ := triglog.Load(dataDir)
	if len(past) > 50 {
		past = past[:50]
	}
	w.triggers = past
	return w, nil
}

// Start initialises inotify, adds watches, and launches the event loop.
func (w *Watcher) Start() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.running {
		return nil
	}

	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC)
	if err != nil {
		return fmt.Errorf("inotify init: %w", err)
	}
	w.inoFd = fd

	if err := unix.Pipe2(w.stopPipe[:], unix.O_CLOEXEC); err != nil {
		_ = unix.Close(fd)
		w.inoFd = -1
		return fmt.Errorf("stop pipe: %w", err)
	}

	// Acquire singleton lock before adding inotify watches.
	// This prevents a headless 'decoyd watch' and a TUI-embedded watcher
	// from running concurrently with split rate-limiter/debounce state.
	release, lockErr := acquireWatchLock(w.dataDir)
	if lockErr != nil {
		_ = unix.Close(fd)
		_ = unix.Close(w.stopPipe[0])
		_ = unix.Close(w.stopPipe[1])
		w.inoFd = -1
		return lockErr
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
	_, _ = unix.Write(w.stopPipe[1], []byte{0})
	release := w.lockRelease
	w.lockRelease = nil
	w.mu.Unlock()

	w.debouncer.Stop()
	w.wg.Wait()

	_ = unix.Close(w.inoFd)
	_ = unix.Close(w.stopPipe[0])
	_ = unix.Close(w.stopPipe[1])
	w.inoFd = -1
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

// AddToken adds a watch for a newly deployed token while the watcher is
// running. No-op if stopped or token has no DeployedPath.
func (w *Watcher) AddToken(tok tokens.Token) {
	if tok.DeployedPath == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.inoFd < 0 {
		return
	}
	w.addWatchLocked(tok)
}

func (w *Watcher) addWatchLocked(tok tokens.Token) {
	if _, exists := w.paths[tok.DeployedPath]; exists {
		return
	}
	wd, err := unix.InotifyAddWatch(w.inoFd, tok.DeployedPath, inotifyMask)
	if err != nil {
		return
	}
	w.watched[wd] = tok
	w.paths[tok.DeployedPath] = wd
}

func (w *Watcher) loop() {
	defer w.wg.Done()
	buf := make([]byte, 4096)
	fds := []unix.PollFd{
		{Fd: int32(w.inoFd), Events: unix.POLLIN},
		{Fd: int32(w.stopPipe[0]), Events: unix.POLLIN},
	}
	for {
		n, err := unix.Poll(fds, 1000)
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			return
		}
		if n > 0 && fds[1].Revents&unix.POLLIN != 0 {
			return
		}
		if n > 0 && fds[0].Revents&unix.POLLIN != 0 {
			nb, err := unix.Read(w.inoFd, buf)
			if err != nil || nb == 0 {
				continue
			}
			w.parseEvents(buf[:nb])
		}
	}
}

func (w *Watcher) parseEvents(buf []byte) {
	for offset := 0; offset+inotifyEventSize <= len(buf); {
		ev := (*unix.InotifyEvent)(unsafe.Pointer(&buf[offset])) // #nosec G103
		nameLen := int(ev.Len)
		offset += inotifyEventSize + nameLen

		evtType := maskToEventType(ev.Mask)
		if evtType == "" {
			continue
		}

		w.mu.RLock()
		tok, ok := w.watched[int(ev.Wd)]
		w.mu.RUnlock()
		if !ok {
			continue
		}

		path := tok.DeployedPath
		tokCopy := tok
		w.debouncer.Trigger(path, func() {
			w.fire(tokCopy, path, evtType)
		})
	}
}

// fire runs after debounce, applies rate-limit and quiet-hours, then appends
// to triggers.jsonl and dispatches the alert.
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

	// ── DURABILITY: write pending record BEFORE sending ───────────────────
	_ = triglog.Append(w.dataDir, te)

	// ── Dispatch alert ────────────────────────────────────────────────────
	status, alertErr := sendAlert(w.dataDir, tok, te)

	// ── Append final status (deduplication: latest record per ID wins) ────
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

func maskToEventType(mask uint32) string {
	switch {
	case mask&unix.IN_OPEN != 0:
		return "access"
	case mask&unix.IN_ACCESS != 0:
		return "access"
	case mask&unix.IN_MODIFY != 0:
		return "write"
	case mask&unix.IN_MOVE_SELF != 0:
		return "rename"
	case mask&unix.IN_DELETE_SELF != 0:
		return "delete"
	}
	return ""
}
