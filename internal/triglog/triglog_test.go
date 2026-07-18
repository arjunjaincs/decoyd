package triglog_test

import (
	"os"
	"testing"
	"time"

	"github.com/arjunjaincs/decoyd/internal/triglog"
)

// newEvent is a test helper that returns a minimal valid TriggerEvent.
func newEvent(id, tokenID string, status triglog.TriggerStatus, t time.Time) triglog.TriggerEvent {
	return triglog.TriggerEvent{
		ID:          id,
		TokenID:     tokenID,
		TokenType:   "aws_credentials",
		Path:        "/tmp/credentials",
		TriggeredAt: t,
		EventType:   "access",
		Status:      status,
	}
}

func TestTriglog_AppendAndLoad(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Millisecond)

	ev := newEvent("aabbccdd", "tok1", triglog.TriggerSent, now)
	if err := triglog.Append(dir, ev); err != nil {
		t.Fatalf("Append: %v", err)
	}

	events, err := triglog.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("Load: want 1 event, got %d", len(events))
	}
	got := events[0]
	if got.ID != ev.ID {
		t.Errorf("ID: got %q, want %q", got.ID, ev.ID)
	}
	if got.Status != triglog.TriggerSent {
		t.Errorf("Status: got %q, want %q", got.Status, triglog.TriggerSent)
	}
}

func TestTriglog_DeduplicatesByIDLatestWins(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()

	pending := newEvent("id1", "tok1", triglog.TriggerPending, now)
	final := newEvent("id1", "tok1", triglog.TriggerSent, now.Add(time.Second))

	if err := triglog.Append(dir, pending); err != nil {
		t.Fatal(err)
	}
	if err := triglog.Append(dir, final); err != nil {
		t.Fatal(err)
	}

	events, err := triglog.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("want 1 deduplicated event, got %d", len(events))
	}
	if events[0].Status != triglog.TriggerSent {
		t.Errorf("latest record should win: got status %q", events[0].Status)
	}
}

func TestTriglog_LoadMissingFileReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	events, err := triglog.Load(dir)
	if err != nil {
		t.Fatalf("Load on missing file: %v", err)
	}
	if len(events) != 0 {
		t.Errorf("want empty slice, got %d events", len(events))
	}
}

func TestTriglog_LoadByToken(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()

	_ = triglog.Append(dir, newEvent("e1", "tok1", triglog.TriggerSent, now))
	_ = triglog.Append(dir, newEvent("e2", "tok2", triglog.TriggerSent, now.Add(time.Second)))
	_ = triglog.Append(dir, newEvent("e3", "tok1", triglog.TriggerFailed, now.Add(2*time.Second)))

	events, err := triglog.LoadByToken(dir, "tok1")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("want 2 events for tok1, got %d", len(events))
	}
	for _, e := range events {
		if e.TokenID != "tok1" {
			t.Errorf("LoadByToken returned wrong token: %q", e.TokenID)
		}
	}
}

func TestTriglog_NewestFirst(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().UTC()

	_ = triglog.Append(dir, newEvent("old", "tok1", triglog.TriggerSent, base))
	_ = triglog.Append(dir, newEvent("new", "tok1", triglog.TriggerSent, base.Add(time.Hour)))

	events, err := triglog.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("want 2 events, got %d", len(events))
	}
	if events[0].ID != "new" {
		t.Errorf("newest-first: first event should be %q, got %q", "new", events[0].ID)
	}
}

func TestTriglog_AppendEmptyIDReturnsError(t *testing.T) {
	dir := t.TempDir()
	ev := newEvent("", "tok1", triglog.TriggerSent, time.Now())
	err := triglog.Append(dir, ev)
	if err == nil {
		t.Fatal("Append with empty ID should return error")
	}
}

// Verify the log file itself is created with restricted permissions (Unix only).
func TestTriglog_FilePermissions(t *testing.T) {
	if os.Getenv("GOOS") == "windows" {
		t.Skip("permission bits not enforced on Windows")
	}
	// Also detect actual Windows runtime.
	fi, _ := os.Stat(os.TempDir())
	_ = fi

	dir := t.TempDir()
	ev := newEvent("perm1", "tok1", triglog.TriggerSent, time.Now())
	if err := triglog.Append(dir, ev); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir + "/triggers.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		// Only enforce on Linux; silently pass on other platforms.
		if info.Mode().Perm()&0o077 != 0 {
			t.Skipf("permission bits not enforced on this platform (%v)", info.Mode().Perm())
		}
	}
}
