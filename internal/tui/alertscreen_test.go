package tui

import (
	"testing"

	"github.com/arjunjaincs/decoyd/internal/alert"
)

func findChannelIdx(channelType string) int {
	for i, c := range alert.Channels {
		if c.Type == channelType {
			return i
		}
	}
	return -1
}

// TestAlertChannelCyclePreservesCredentials is the regression test for:
//   configure Discord, fire test-send, cycle to another channel, cycle back —
//   URL field is now empty.
//
// Root cause: channel cycling called m.primaryBuf = nil unconditionally.
// Fix: loadChannelFields() restores from savedChannels cache on every cycle.
func TestAlertChannelCyclePreservesCredentials(t *testing.T) {
	dir := t.TempDir()
	m := NewAlertModel(80, 24, dir)

	discordIdx := findChannelIdx(alert.ChannelDiscord)
	if discordIdx < 0 {
		t.Fatal("Discord not found in alert.Channels")
	}
	m.channelIdx = discordIdx

	const wantURL = "https://discord.com/api/webhooks/12345/testtoken"
	m.primaryBuf = []rune(wantURL)
	m.primaryPos = len(m.primaryBuf)

	// Simulate what doTestSend does: cache the config.
	if m.savedChannels == nil {
		m.savedChannels = make(map[string]alert.ChannelConfig)
	}
	m.savedChannels[alert.ChannelDiscord] = alert.ChannelConfig{
		Type:       alert.ChannelDiscord,
		WebhookURL: wantURL,
	}

	// Cycle away.
	m.channelIdx = (m.channelIdx + 1) % len(alert.Channels)
	m.loadChannelFields()
	if string(m.primaryBuf) == wantURL {
		t.Error("primaryBuf should NOT equal the Discord URL after cycling away")
	}

	// Cycle back.
	m.channelIdx = discordIdx
	m.loadChannelFields()

	got := string(m.primaryBuf)
	if got != wantURL {
		t.Errorf("after cycling away and back, primaryBuf = %q; want %q", got, wantURL)
	}
}

// TestAlertChannelCycleFullRoundTrip cycles through ALL channel types and
// verifies Discord URL is intact after a full round-trip.
func TestAlertChannelCycleFullRoundTrip(t *testing.T) {
	dir := t.TempDir()
	m := NewAlertModel(80, 24, dir)

	discordIdx := findChannelIdx(alert.ChannelDiscord)
	if discordIdx < 0 {
		t.Fatal("Discord not found in alert.Channels")
	}

	const wantURL = "https://discord.com/api/webhooks/99/roundtrip"
	if m.savedChannels == nil {
		m.savedChannels = make(map[string]alert.ChannelConfig)
	}
	m.savedChannels[alert.ChannelDiscord] = alert.ChannelConfig{
		Type:       alert.ChannelDiscord,
		WebhookURL: wantURL,
	}

	// Spin through every channel type (lands back on Discord after len(Channels) steps).
	m.channelIdx = discordIdx
	for i := 0; i < len(alert.Channels); i++ {
		m.channelIdx = (m.channelIdx + 1) % len(alert.Channels)
		m.loadChannelFields()
	}

	got := string(m.primaryBuf)
	if got != wantURL {
		t.Errorf("after full round-trip, primaryBuf = %q; want %q", got, wantURL)
	}
}

// TestAlertChannelUnconfiguredIsEmpty verifies that cycling to a channel that
// was never configured shows empty fields, not stale data from another type.
func TestAlertChannelUnconfiguredIsEmpty(t *testing.T) {
	dir := t.TempDir()
	m := NewAlertModel(80, 24, dir)

	if m.savedChannels == nil {
		m.savedChannels = make(map[string]alert.ChannelConfig)
	}
	m.savedChannels[alert.ChannelDiscord] = alert.ChannelConfig{
		Type:       alert.ChannelDiscord,
		WebhookURL: "https://discord.com/api/webhooks/x/y",
	}

	slackIdx := findChannelIdx(alert.ChannelSlack)
	if slackIdx < 0 {
		t.Fatal("Slack not found in alert.Channels")
	}
	m.channelIdx = slackIdx
	m.loadChannelFields()

	if len(m.primaryBuf) != 0 {
		t.Errorf("Slack (unconfigured) primaryBuf = %q; want empty", string(m.primaryBuf))
	}
	if len(m.secondaryBuf) != 0 {
		t.Errorf("Slack (unconfigured) secondaryBuf = %q; want empty", string(m.secondaryBuf))
	}
}
