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
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/arjunjaincs/decoyd/internal/alert"
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

	mu      sync.Mutex
	running bool
	count   int // number of files currently watched

	// inotify and self-pipe fds; both -1 when not running.
	inoFD  int
	stopR  int // read end of stop pipe
	stopW  int // write end of stop pipe

	// stopCh is closed by stop() to signal the event loop.
	stopCh chan struct{}
	// done is closed by the event loop goroutine when it exits.
	done chan struct{}
}

func newPlatformImpl(st *store.Store, dataDir string) (watcherImpl, error) {
	return &linuxWatcher{
		st:      st,
		dataDir: dataDir,
		cfg:     DefaultWatcherConfig(),
		inoFD:   -1,
		stopR:   -1,
		stopW:   -1,
	}, nil
}

// ----------------------------------------------------------------------------
// start
// ----------------------------------------------------------------------------

func (w *linuxWatcher) start() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.running {
		return fmt.Errorf("watcher already running")
	}

	// Create inotify instance.
	inoFD, err := unix.InotifyInit1(unix.IN_CLOEXEC)
	if err != nil {
		return fmt.Errorf("inotify_init1: %w", err)
	}

	// Create self-pipe for clean shutdown.
	pipefd := [2]int{}
	if err := unix.Pipe2(pipefd[:], unix.O_CLOEXEC|unix.O_NONBLOCK); err != nil {
		_ = unix.Close(inoFD)
		return fmt.Errorf("pipe2: %w", err)
	}

	w.inoFD = inoFD
	w.stopR = pipefd[0]
	w.stopW = pipefd[1]
	w.stopCh = make(chan struct{})
	w.done = make(chan struct{})
	w.running = true

	// Load token list.
	tokens, err := w.loadTokens()
	if err != nil {
		w.closeFDs()
		w.running = false
		return fmt.Errorf("load tokens: %w", err)
	}

	// Add one inotify watch per deployed path.
	// path → DeployedToken (for alert dispatch)
	const mask = unix.IN_OPEN | unix.IN_ACCESS | unix.IN_MODIFY |
		unix.IN_MOVE_SELF | unix.IN_DELETE_SELF
	wdMap := make(map[int32]DeployedToken) // wd → token
	for _, tok := range tokens {
		if tok.DeployedPath == "" {
			continue
		}
		wd, err := unix.InotifyAddWatch(w.inoFD, tok.DeployedPath, mask)
		if err != nil {
			// File may not exist yet — skip silently, not fatal.
			continue
		}
		wdMap[int32(wd)] = tok // #nosec G115 -- InotifyAddWatch returns int; Linux WDs are positive and bounded within int32 range
	}

	w.count = len(wdMap)

	go w.eventLoop(wdMap, mask)
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

// debounceEntry tracks per-path debounce state.
type debounceEntry struct {
	token    DeployedToken
	event    string    // "access", "write", "rename", "delete"
	expiry   time.Time // fire the alert at or after this time
}

func (w *linuxWatcher) eventLoop(wdMap map[int32]DeployedToken, mask uint32) {
	defer close(w.done)

	// Inotify event buffer: 4096 bytes, each event is ≥ unix.SizeofInotifyEvent bytes.
	buf := make([]byte, 4096)

	debounce := make(map[string]*debounceEntry)  // deployedPath → entry
	rateMap := make(map[string]*rateEntry)        // tokenID → entry
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
				w.dispatch(rateMap, e.token, e.event, now)
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

			tok, ok := wdMap[wd]
			if !ok {
				continue
			}

			evType := inotifyEventType(evMask)
			if evType == "" {
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
				delete(wdMap, wd)
				w.mu.Lock()
				w.count = len(wdMap)
				w.mu.Unlock()
				// Attempt to re-add the watch (e.g. after atomic file replace).
				go func(t DeployedToken) {
					time.Sleep(100 * time.Millisecond)
					newWD, err := unix.InotifyAddWatch(w.inoFD, t.DeployedPath, mask)
					if err == nil {
						w.mu.Lock()
						wdMap[int32(newWD)] = t // #nosec G115 -- InotifyAddWatch returns int; Linux WDs are positive and bounded within int32 range
						w.count = len(wdMap)
						w.mu.Unlock()
					}
				}(tok)
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
		_ = triglog.Append(w.dataDir, ev)
		return
	}

	// Rate-limit check: max RateLimit events per token per hour.
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

	// Write pending record before dispatch (durability).
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
		// No channel configured — log and continue, not an error.
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
