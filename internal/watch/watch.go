// Package watch implements the background file watcher for Decoyd.
//
// Architecture: the watcher is split into two completely independent
// platform implementations that share only this file's types and helpers:
//
//	inotify_linux.go  — Linux: raw inotify(7) via golang.org/x/sys/unix.
//	                     Watches IN_OPEN | IN_ACCESS | IN_MODIFY |
//	                     IN_MOVE_SELF | IN_DELETE_SELF for true read-detection.
//
//	watch_windows.go  — Windows: fsnotify on parent directory, filtered to
//	                     filename, covering Write/Rename/Remove.
//	                     True read-detection is NOT available on Windows (v1.1).
//
// Cross-process safety
// --------------------
// bbolt holds an EXCLUSIVE write lock per file.  To avoid deadlocking with
// the TUI, which holds decoyd.db open while running:
//
//   - The headless "decoyd watch" process MUST NOT open decoyd.db.
//   - Token paths are read from deployed_tokens.json (written by the TUI).
//   - Trigger events are appended to triggers.jsonl (triglog package).
//   - markTokenTriggered is called only in TUI-embedded mode where the TUI
//     already owns the bbolt lock.
//
// Durability contract: triglog.Append(TriggerPending) is called BEFORE alert
// dispatch.  Even if the process is killed mid-send the trigger record survives.
package watch

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/arjunjaincs/decoyd/internal/alert"
	"github.com/arjunjaincs/decoyd/internal/store"
	"github.com/arjunjaincs/decoyd/internal/tokens"
	"github.com/arjunjaincs/decoyd/internal/triglog"
)

// newEventID returns a cryptographically random 8-byte hex trigger ID.
func newEventID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// WatcherStatus is a point-in-time snapshot of the watcher state.
type WatcherStatus struct {
	Running   bool
	StartedAt time.Time
	Watching  int // number of actively watched file paths
}

// sendAlert dispatches an alert for a trigger event using the token's
// configured channel (or the default). Uses sanitizeAlertErr to scrub URLs.
func sendAlert(dataDir string, tok tokens.Token, te triglog.TriggerEvent) (triglog.TriggerStatus, string) {
	cfg, err := alert.Load(dataDir)
	if err != nil || len(cfg.Channels) == 0 {
		return triglog.TriggerFailed, "no alert channel configured"
	}
	ch, ok := cfg.ChannelForToken(tok.AlertChannelID)
	if !ok {
		return triglog.TriggerFailed, "no alert channel configured"
	}
	a, err := alert.NewAlerter(ch)
	if err != nil {
		return triglog.TriggerFailed, err.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	payload := alert.AlertPayload{
		TokenID:     te.TokenID,
		TokenType:   te.TokenType,
		Path:        te.Path,
		TriggeredAt: te.TriggeredAt,
		Detail:      fmt.Sprintf("Event: %s", te.EventType),
	}
	if err := a.Send(ctx, payload); err != nil {
		return triglog.TriggerFailed, sanitizeAlertErr(err)
	}
	return triglog.TriggerSent, ""
}

// sanitizeAlertErr caps alert error strings at 120 chars.
// alert.Send already strips webhook URLs via sanitizeErr before returning,
// so this only needs to truncate to keep dashboard display clean.
func sanitizeAlertErr(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	const maxLen = 120
	if len(s) > maxLen {
		s = s[:maxLen] + "…"
	}
	return s
}

// markTokenTriggered sets Token.Triggered = true and Token.TriggeredAt.
// Called only in TUI-embedded mode (st != nil), where the TUI owns the
// bbolt write lock.  No-op in headless mode (st == nil).
func markTokenTriggered(st *store.Store, tok tokens.Token, at time.Time) {
	if st == nil || tok.Triggered {
		return
	}
	tok.Triggered = true
	tok.TriggeredAt = &at
	_ = st.UpdateToken(tok)
}
