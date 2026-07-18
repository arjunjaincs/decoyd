// Package triglog implements the append-only trigger event log (triggers.jsonl).
//
// Design constraints:
//   - Append-only: every trigger event is appended as a newline-delimited JSON
//     record. Events are never deleted or rewritten.
//   - Deduplication: Load deduplicates by ID, latest record per ID wins. This
//     lets the watcher write a "pending" record before sending and a "sent"/"error"
//     record after, with the final record superseding the earlier one on read.
//   - No bbolt: triglog reads and writes only triggers.jsonl so it can run
//     safely alongside a live TUI that holds the bbolt exclusive write lock.
package triglog

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// ----------------------------------------------------------------------------
// TriggerStatus constants
// ----------------------------------------------------------------------------

// TriggerStatus is the outcome of a trigger event.
type TriggerStatus = string

const (
	// TriggerPending is written before the alert is dispatched (durability record).
	TriggerPending = "pending"
	// TriggerSent means the alert was dispatched successfully.
	TriggerSent = "sent"
	// TriggerFailed means the alert dispatch failed (network error, misconfigured channel, etc.).
	TriggerFailed = "failed"
	// TriggerRateLimited means the event was suppressed by the per-token rate limiter.
	TriggerRateLimited = "rate_limited"
	// TriggerQuietHours means the event was suppressed because it occurred during quiet hours.
	TriggerQuietHours = "quiet_hours"
)

// ----------------------------------------------------------------------------
// TriggerEvent — the log record
// ----------------------------------------------------------------------------

// TriggerEvent is a single entry in triggers.jsonl.
// Every watcher-detected access produces at least one TriggerEvent.
// The watcher writes a pending record before sending and a final record after,
// so a given ID may appear twice; Load deduplicates keeping the latest.
type TriggerEvent struct {
	// ID is a random per-event identifier used for deduplication.
	ID string `json:"id"`
	// TokenID is the ID of the token that triggered.
	TokenID string `json:"token_id"`
	// TokenType is the token type (e.g. "aws_credentials").
	TokenType string `json:"token_type"`
	// Path is the absolute path of the deployed file that was accessed.
	Path string `json:"path"`
	// TriggeredAt is the UTC time the event was detected.
	TriggeredAt time.Time `json:"triggered_at"`
	// EventType describes what inotify/fsnotify event occurred: "access", "write", "rename", "delete".
	EventType string `json:"event_type"`
	// Status is the outcome; one of the Trigger* constants.
	Status TriggerStatus `json:"status"`
	// AlertError is a sanitized error string when Status == TriggerFailed. Empty otherwise.
	// Guaranteed never to contain webhook URLs or bot tokens.
	AlertError string `json:"alert_error,omitempty"`
}

// ----------------------------------------------------------------------------
// Sentinel errors
// ----------------------------------------------------------------------------

// ErrEmptyID is returned by Append when the event has an empty ID field.
var ErrEmptyID = errors.New("triglog: event ID must not be empty")

// ----------------------------------------------------------------------------
// File path
// ----------------------------------------------------------------------------

const logFileName = "triggers.jsonl"

func logPath(dataDir string) string {
	return filepath.Join(dataDir, logFileName)
}

// ----------------------------------------------------------------------------
// Append — write one record to the log
// ----------------------------------------------------------------------------

// Append marshals te to JSON and appends it as a newline-terminated record to
// dataDir/triggers.jsonl. The file is created with 0600 permissions if absent.
// Append is the only mutation operation: records are never edited or deleted.
//
// Safe to call from multiple goroutines in the same process only if the caller
// holds an external mutex; the underlying os.OpenFile call is not atomic.
func Append(dataDir string, te TriggerEvent) error {
	if te.ID == "" {
		return ErrEmptyID
	}

	data, err := json.Marshal(te)
	if err != nil {
		return fmt.Errorf("triglog marshal: %w", err)
	}

	path := logPath(dataDir)
	// #nosec G304 -- path is always filepath.Join(dataDir, logFileName), never user input
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("triglog open: %w", err)
	}
	defer f.Close()

	data = append(data, '\n')
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("triglog write: %w", err)
	}
	return nil
}

// ----------------------------------------------------------------------------
// Load — read and deduplicate all records
// ----------------------------------------------------------------------------

// Load reads dataDir/triggers.jsonl and returns all events, deduplicated by ID
// (latest occurrence wins) and sorted newest-first by TriggeredAt.
// Returns an empty slice (not an error) when the file does not exist yet.
func Load(dataDir string) ([]TriggerEvent, error) {
	path := logPath(dataDir)
	// #nosec G304 -- path is always filepath.Join(dataDir, logFileName), never user input
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("triglog open: %w", err)
	}
	defer f.Close()

	// latest-record-per-ID deduplication map; preserves last occurrence.
	seen := make(map[string]TriggerEvent)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var te TriggerEvent
		if err := json.Unmarshal(line, &te); err != nil {
			// Skip malformed lines — do not abort the whole read.
			continue
		}
		if te.ID != "" {
			seen[te.ID] = te
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("triglog scan: %w", err)
	}

	events := make([]TriggerEvent, 0, len(seen))
	for _, te := range seen {
		events = append(events, te)
	}
	// Sort newest-first.
	sort.Slice(events, func(i, j int) bool {
		return events[i].TriggeredAt.After(events[j].TriggeredAt)
	})
	return events, nil
}

// ----------------------------------------------------------------------------
// LoadByToken — filtered view
// ----------------------------------------------------------------------------

// LoadByToken returns all events for a specific token ID, newest-first.
// It calls Load and filters; the deduplication logic is the same.
func LoadByToken(dataDir, tokenID string) ([]TriggerEvent, error) {
	all, err := Load(dataDir)
	if err != nil {
		return nil, err
	}
	var out []TriggerEvent
	for _, te := range all {
		if te.TokenID == tokenID {
			out = append(out, te)
		}
	}
	return out, nil
}
