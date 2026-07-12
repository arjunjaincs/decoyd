// Package triglog provides an append-only JSONL trigger event log.
//
// Design rationale
// ----------------
// Trigger events are written by the background "decoyd watch" process and read
// by the TUI and "decoyd triggers" command.  bbolt holds an exclusive write
// lock per file, so two processes cannot share the same decoyd.db without
// risking a deadlock or timeout.  Using a plain JSONL file avoids this
// conflict entirely:
//
//   - ONE writer:  the watcher goroutine (serialised through a mutex).
//   - Many readers: TUI, CLI (read-only, no lock needed on POSIX; Windows
//                   file sharing is handled by the standard O_RDONLY open).
//
// Update-in-place is not attempted.  When a trigger's status changes after an
// alert send, a second JSON line with the same ID is appended; Load()
// de-duplicates by ID and uses the last (newest) record.  This preserves the
// durability guarantee — the trigger is on disk before the HTTP call — without
// requiring any file seeking.
//
// The file path is: <dataDir>/triggers.jsonl
package triglog

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const triggersFile = "triggers.jsonl"

// TriggerStatus describes the outcome of a trigger event.
type TriggerStatus string

const (
	TriggerSent        TriggerStatus = "sent"         // alert dispatched successfully
	TriggerFailed      TriggerStatus = "failed"        // alert attempted but errored
	TriggerRateLimited TriggerStatus = "rate_limited"  // suppressed by rate limiter
	TriggerQuietHours  TriggerStatus = "quiet_hours"   // suppressed by quiet hours
	TriggerPending     TriggerStatus = "pending"       // written before send, updated after
)

// TriggerEvent is a durable record of a file-access event on a decoy token.
// Written to triggers.jsonl BEFORE the alert is dispatched so evidence is
// preserved even if the process is killed mid-send.
type TriggerEvent struct {
	ID          string        `json:"id"`
	TokenID     string        `json:"token_id"`
	TokenType   string        `json:"token_type"`
	Path        string        `json:"path"`
	TriggeredAt time.Time     `json:"triggered_at"`
	EventType   string        `json:"event_type"` // "access","write","rename","delete","create"
	Process     string        `json:"process,omitempty"` // Linux best-effort (future fanotify)
	Status      TriggerStatus `json:"status"`
	AlertError  string        `json:"alert_error,omitempty"` // sanitizeErr output only
}

// appendMu serialises all writes to triggers.jsonl within a single process.
// Multiple processes are safe by design: there is always exactly ONE writer
// (the watcher process) and O_APPEND writes of ≤ 4096 bytes are atomic on
// POSIX; on Windows the mutex ensures sequential writes within the process.
var appendMu sync.Mutex

// Append writes a single TriggerEvent JSON line to <dataDir>/triggers.jsonl.
// It is safe to call from multiple goroutines.  The file is created if it
// does not exist (mode 0600).
//
// DURABILITY CONTRACT: call Append with Status=TriggerPending BEFORE
// attempting to send the alert, then call Append again with the final status
// after the send completes.
func Append(dataDir string, e TriggerEvent) error {
	if e.ID == "" {
		return errors.New("append trigger: ID must not be empty")
	}
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("marshal trigger: %w", err)
	}
	line = append(line, '\n')

	path := filepath.Join(dataDir, triggersFile)
	appendMu.Lock()
	defer appendMu.Unlock()

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) // #nosec G304
	if err != nil {
		return fmt.Errorf("open triggers.jsonl: %w", err)
	}
	defer f.Close()
	_, err = f.Write(line)
	return err
}

// Load reads all trigger events from <dataDir>/triggers.jsonl, de-duplicates
// by ID (latest entry per ID wins), and returns them newest-first.
// Returns an empty slice (not an error) if the file does not exist yet.
func Load(dataDir string) ([]TriggerEvent, error) {
	path := filepath.Join(dataDir, triggersFile)
	f, err := os.Open(path) // #nosec G304
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open triggers.jsonl: %w", err)
	}
	defer f.Close()

	// latest record per ID wins (status update appended after initial write).
	byID := make(map[string]TriggerEvent)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e TriggerEvent
		if err := json.Unmarshal(line, &e); err != nil {
			continue // skip malformed lines; do not abort
		}
		byID[e.ID] = e
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan triggers.jsonl: %w", err)
	}

	out := make([]TriggerEvent, 0, len(byID))
	for _, e := range byID {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].TriggeredAt.After(out[j].TriggeredAt)
	})
	return out, nil
}

// LoadByToken is like Load but filters to events for a specific tokenID.
func LoadByToken(dataDir, tokenID string) ([]TriggerEvent, error) {
	all, err := Load(dataDir)
	if err != nil {
		return nil, err
	}
	var out []TriggerEvent
	for _, e := range all {
		if e.TokenID == tokenID {
			out = append(out, e)
		}
	}
	return out, nil
}
