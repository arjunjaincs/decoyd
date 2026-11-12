// triglog_compat_test.go — backward-compatibility tests for TriggerEvent schema v1.
//
// Old events (without watcher_tier and attribution fields) must still load
// correctly when unmarshalled. This is guaranteed by omitempty on those fields.
package triglog_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/arjunjaincs/decoyd/internal/triglog"
)

// TestTriggerEvent_OldFormatLoads verifies that pre-T5 event JSON (without
// watcher_tier or attribution) unmarshals without error and has zero values
// for the new fields.
func TestTriggerEvent_OldFormatLoads(t *testing.T) {
	// A realistic pre-T5 event JSON (schema v0 — no watcher_tier, no attribution)
	const oldJSON = `{
		"id": "abc123",
		"token_id": "tok-001",
		"token_type": "aws_credentials",
		"path": "/home/user/.aws/credentials",
		"triggered_at": "2026-01-01T00:00:00Z",
		"event_type": "access",
		"status": "sent"
	}`

	var ev triglog.TriggerEvent
	if err := json.Unmarshal([]byte(oldJSON), &ev); err != nil {
		t.Fatalf("Unmarshal old-format event: %v", err)
	}
	if ev.ID != "abc123" {
		t.Errorf("ID = %q; want %q", ev.ID, "abc123")
	}
	if ev.WatcherTier != "" {
		t.Errorf("WatcherTier = %q; want empty for old-format events", ev.WatcherTier)
	}
	if ev.Attribution != nil {
		t.Errorf("Attribution = %v; want nil for old-format events", ev.Attribution)
	}
}

// TestTriggerEvent_NewFormatRoundTrip verifies that schema v1 events (with
// watcher_tier and attribution) round-trip through JSON correctly.
func TestTriggerEvent_NewFormatRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	ev := triglog.TriggerEvent{
		ID:          "new123",
		TokenID:     "tok-002",
		TokenType:   "github_pat",
		Path:        "/home/user/.github/token",
		TriggeredAt: now,
		EventType:   "access",
		Status:      triglog.TriggerSent,
		WatcherTier: "tier1",
	}

	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var ev2 triglog.TriggerEvent
	if err := json.Unmarshal(data, &ev2); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if ev2.WatcherTier != "tier1" {
		t.Errorf("WatcherTier = %q; want %q", ev2.WatcherTier, "tier1")
	}
	if ev2.ID != "new123" {
		t.Errorf("ID = %q; want %q", ev2.ID, "new123")
	}
}

// TestTriggerEvent_OmitemptyOmitsNilAttribution verifies that nil attribution
// is not serialized (omitempty).
func TestTriggerEvent_OmitemptyOmitsNilAttribution(t *testing.T) {
	ev := triglog.TriggerEvent{
		ID:     "x",
		Status: triglog.TriggerPending,
	}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if _, hasAttr := m["attribution"]; hasAttr {
		t.Error("attribution field present in JSON for nil value; want omitted")
	}
	if _, hasTier := m["watcher_tier"]; hasTier {
		t.Error("watcher_tier field present in JSON for empty value; want omitted")
	}
}
