package alert_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/arjunjaincs/decoyd/internal/alert"
)

// ----------------------------------------------------------------------------
// ChannelConfig.ID helpers
// ----------------------------------------------------------------------------

func TestGenerateChannelIDIsUnique(t *testing.T) {
	a := alert.GenerateChannelID()
	b := alert.GenerateChannelID()
	if a == "" {
		t.Fatal("GenerateChannelID returned empty string")
	}
	if a == b {
		t.Errorf("two consecutive IDs collided: %q", a)
	}
	if len(a) != 8 {
		t.Errorf("expected 8-char hex ID, got %d chars: %q", len(a), a)
	}
}

// ----------------------------------------------------------------------------
// AlertConfig.ResolveChannel
// ----------------------------------------------------------------------------

func TestResolveChannelFound(t *testing.T) {
	cfg := alert.AlertConfig{
		Channels: []alert.ChannelConfig{
			{ID: "aabb1122", Type: alert.ChannelDiscord, WebhookURL: "https://example.com/1"},
			{ID: "ccdd3344", Type: alert.ChannelSlack, WebhookURL: "https://example.com/2"},
		},
	}
	got, ok := cfg.ResolveChannel("ccdd3344")
	if !ok {
		t.Fatal("expected to find channel ccdd3344")
	}
	if got.Type != alert.ChannelSlack {
		t.Errorf("wrong type: got %q, want %q", got.Type, alert.ChannelSlack)
	}
}

func TestResolveChannelNotFound(t *testing.T) {
	cfg := alert.AlertConfig{
		Channels: []alert.ChannelConfig{
			{ID: "aabb1122", Type: alert.ChannelDiscord},
		},
	}
	_, ok := cfg.ResolveChannel("deadbeef")
	if ok {
		t.Error("expected not found for unknown ID")
	}
}

func TestResolveChannelEmptyList(t *testing.T) {
	_, ok := alert.AlertConfig{}.ResolveChannel("anything")
	if ok {
		t.Error("expected not found on empty Channels")
	}
}

// ----------------------------------------------------------------------------
// AlertConfig.ChannelForToken — assignment + fallback to default
// ----------------------------------------------------------------------------

func TestChannelForTokenUsesAssigned(t *testing.T) {
	cfg := alert.AlertConfig{
		Channels: []alert.ChannelConfig{
			{ID: "aaaa0000", Type: alert.ChannelDiscord, WebhookURL: "https://discord.example/1"},
			{ID: "bbbb1111", Type: alert.ChannelSlack, WebhookURL: "https://slack.example/1"},
		},
		DefaultIndex: 0,
	}
	got, ok := cfg.ChannelForToken("bbbb1111")
	if !ok {
		t.Fatal("expected a channel to be returned")
	}
	if got.Type != alert.ChannelSlack {
		t.Errorf("expected Slack (assigned), got %q", got.Type)
	}
}

func TestChannelForTokenFallsBackToDefault(t *testing.T) {
	cfg := alert.AlertConfig{
		Channels: []alert.ChannelConfig{
			{ID: "aaaa0000", Type: alert.ChannelDiscord, WebhookURL: "https://discord.example/1"},
			{ID: "bbbb1111", Type: alert.ChannelSlack, WebhookURL: "https://slack.example/1"},
		},
		DefaultIndex: 0,
	}
	// Token has no assignment — should get Discord (default index 0).
	got, ok := cfg.ChannelForToken("")
	if !ok {
		t.Fatal("expected default channel to be returned")
	}
	if got.Type != alert.ChannelDiscord {
		t.Errorf("expected Discord (default), got %q", got.Type)
	}
}

func TestChannelForTokenDeletedAssignmentFallsBackToDefault(t *testing.T) {
	// Token was assigned "deleted-id" which no longer exists → must not crash,
	// must fall back to the default channel.
	cfg := alert.AlertConfig{
		Channels: []alert.ChannelConfig{
			{ID: "aaaa0000", Type: alert.ChannelDiscord, WebhookURL: "https://discord.example/1"},
		},
		DefaultIndex: 0,
	}
	got, ok := cfg.ChannelForToken("deleted-id-that-no-longer-exists")
	if !ok {
		t.Fatal("expected fallback to default, not not-found")
	}
	if got.Type != alert.ChannelDiscord {
		t.Errorf("expected Discord fallback, got %q", got.Type)
	}
}

func TestChannelForTokenNoChannels(t *testing.T) {
	_, ok := alert.AlertConfig{}.ChannelForToken("any-id")
	if ok {
		t.Error("expected false when no channels configured")
	}
}

// ----------------------------------------------------------------------------
// Save + Load round-trip — ID backfill
// ----------------------------------------------------------------------------

func TestSaveBackfillsIDs(t *testing.T) {
	dir := t.TempDir()
	cfg := alert.AlertConfig{
		Channels: []alert.ChannelConfig{
			// No ID set — Save() must assign one.
			{Type: alert.ChannelDiscord, WebhookURL: "https://discord.example/hook"},
		},
		DefaultIndex: 0,
	}
	if err := alert.Save(dir, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := alert.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Channels) != 1 {
		t.Fatalf("expected 1 channel, got %d", len(loaded.Channels))
	}
	if loaded.Channels[0].ID == "" {
		t.Error("expected Save to backfill an ID, got empty")
	}
}

func TestSavePreservesExistingIDs(t *testing.T) {
	dir := t.TempDir()
	const fixedID = "cafebabe"
	cfg := alert.AlertConfig{
		Channels: []alert.ChannelConfig{
			{ID: fixedID, Type: alert.ChannelSlack, WebhookURL: "https://slack.example/hook"},
		},
	}
	if err := alert.Save(dir, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := alert.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Channels[0].ID != fixedID {
		t.Errorf("expected ID %q to be preserved, got %q", fixedID, loaded.Channels[0].ID)
	}
}

func TestMultiChannelSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	cfg := alert.AlertConfig{
		Channels: []alert.ChannelConfig{
			{Type: alert.ChannelDiscord, WebhookURL: "https://discord.example/1"},
			{Type: alert.ChannelSlack, WebhookURL: "https://slack.example/1"},
			{Type: alert.ChannelTelegram, BotToken: "tok:secret", ChatID: "123456"},
		},
		DefaultIndex: 1,
	}
	if err := alert.Save(dir, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := alert.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Channels) != 3 {
		t.Fatalf("expected 3 channels, got %d", len(loaded.Channels))
	}
	if loaded.DefaultIndex != 1 {
		t.Errorf("DefaultIndex not preserved: got %d", loaded.DefaultIndex)
	}
	// All must have IDs after load.
	for i, ch := range loaded.Channels {
		if ch.ID == "" {
			t.Errorf("channel %d has empty ID after Save+Load", i)
		}
	}
	// IDs must be unique.
	seen := map[string]bool{}
	for _, ch := range loaded.Channels {
		if seen[ch.ID] {
			t.Errorf("duplicate ID %q", ch.ID)
		}
		seen[ch.ID] = true
	}
}

// LoadBackfillsLegacyMissingID verifies that old config files (no ID field)
// get IDs on next Load, and the backfill is written to disk.
func TestLoadBackfillsLegacyMissingID(t *testing.T) {
	dir := t.TempDir()
	// Write a config manually WITHOUT the id field (simulating old format).
	legacy := `{"channels":[{"type":"discord","webhook_url":"https://discord.example/hook"}],"default_index":0}`
	if err := os.WriteFile(filepath.Join(dir, "alert_config.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := alert.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Channels[0].ID == "" {
		t.Error("Load should have backfilled an ID for legacy config")
	}
	// Reload again to confirm it was persisted.
	reloaded, err := alert.Load(dir)
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if reloaded.Channels[0].ID != loaded.Channels[0].ID {
		t.Error("backfilled ID was not persisted to disk")
	}
}
