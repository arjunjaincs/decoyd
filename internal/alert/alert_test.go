// Tests are in package alert (not alert_test) so we can access unexported
// constructors like newTelegramAlerter and the sanitizeErr function.
package alert

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ----------------------------------------------------------------------------
// Shared test helpers
// ----------------------------------------------------------------------------

// testPayload returns a fully-populated AlertPayload for use in every test.
func testPayload() AlertPayload {
	return AlertPayload{
		TokenID:     "b3e61130d9bda4f8",
		TokenType:   "aws_credentials",
		Path:        "/home/user/.aws/credentials",
		TriggeredAt: time.Date(2026, 7, 12, 8, 42, 3, 0, time.UTC),
		Detail:      "file opened",
	}
}

// captureServer starts an httptest server that returns statusCode and captures
// the request body into *out. Use for payload-shape assertions.
func captureServer(t *testing.T, statusCode int, out *[]byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if out != nil {
			*out = b
		}
		w.WriteHeader(statusCode)
	}))
}

// slowServer returns an httptest server that sleeps 200 ms before responding.
// This is longer than the 50 ms test-context timeout used in timeout tests,
// so the client context fires first, but short enough that httptest.Server.Close
// drains the in-flight connection without the 5-second watchdog triggering.
func slowServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(200)
	}))
}

// captureHeaders is like captureServer but also copies response headers.
func captureHeaderServer(t *testing.T, out *[]byte, hdrs *http.Header) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if out != nil {
			*out = b
		}
		if hdrs != nil {
			*hdrs = r.Header.Clone()
		}
		w.WriteHeader(200)
	}))
}

// ----------------------------------------------------------------------------
// Discord
// ----------------------------------------------------------------------------

func TestDiscordAlerter_CorrectPayload(t *testing.T) {
	var got []byte
	ts := captureServer(t, 204, &got)
	defer ts.Close()

	if err := (&DiscordAlerter{webhookURL: ts.URL}).Send(context.Background(), testPayload()); err != nil {
		t.Fatalf("Send() error: %v", err)
	}

	var p discordPayload
	if err := json.Unmarshal(got, &p); err != nil {
		t.Fatalf("unmarshal discord payload: %v", err)
	}
	if len(p.Embeds) != 1 {
		t.Fatalf("embeds len = %d; want 1", len(p.Embeds))
	}
	embed := p.Embeds[0]
	if embed.Title != "Decoyd Alert" {
		t.Errorf("embed.Title = %q; want \"Decoyd Alert\"", embed.Title)
	}
	if embed.Color != discordDanger {
		t.Errorf("embed.Color = %d; want %d", embed.Color, discordDanger)
	}
	fieldNames := make(map[string]string, len(embed.Fields))
	for _, f := range embed.Fields {
		fieldNames[f.Name] = f.Value
	}
	for _, want := range []string{"Token ID", "Type", "Path", "Time", "Detail"} {
		if _, ok := fieldNames[want]; !ok {
			t.Errorf("embed missing field %q", want)
		}
	}
	if fieldNames["Token ID"] != testPayload().TokenID {
		t.Errorf("Token ID = %q; want %q", fieldNames["Token ID"], testPayload().TokenID)
	}
}

func TestDiscordAlerter_NonTwoXX(t *testing.T) {
	ts := captureServer(t, 400, nil)
	defer ts.Close()

	err := (&DiscordAlerter{webhookURL: ts.URL}).Send(context.Background(), testPayload())
	if err == nil {
		t.Fatal("expected error on 400 response")
	}
	if strings.Contains(err.Error(), ts.URL) {
		t.Errorf("error leaks URL: %v", err)
	}
}

func TestDiscordAlerter_Timeout(t *testing.T) {
	ts := slowServer(t)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := (&DiscordAlerter{webhookURL: ts.URL}).Send(ctx, testPayload())
	if err == nil {
		t.Fatal("expected error on timeout")
	}
	if strings.Contains(err.Error(), ts.URL) {
		t.Errorf("error leaks URL on timeout: %v", err)
	}
}

// ----------------------------------------------------------------------------
// Slack
// ----------------------------------------------------------------------------

func TestSlackAlerter_CorrectPayload(t *testing.T) {
	var got []byte
	ts := captureServer(t, 200, &got)
	defer ts.Close()

	if err := (&SlackAlerter{webhookURL: ts.URL}).Send(context.Background(), testPayload()); err != nil {
		t.Fatalf("Send() error: %v", err)
	}

	var p slackPayload
	if err := json.Unmarshal(got, &p); err != nil {
		t.Fatalf("unmarshal slack payload: %v", err)
	}
	if len(p.Blocks) < 2 {
		t.Fatalf("blocks len = %d; want >= 2", len(p.Blocks))
	}
	if p.Blocks[0].Type != "header" {
		t.Errorf("blocks[0].Type = %q; want \"header\"", p.Blocks[0].Type)
	}
	sectionBlock := p.Blocks[1]
	if sectionBlock.Type != "section" {
		t.Errorf("blocks[1].Type = %q; want \"section\"", sectionBlock.Type)
	}
	if len(sectionBlock.Fields) < 4 {
		t.Errorf("section fields = %d; want >= 4", len(sectionBlock.Fields))
	}
}

func TestSlackAlerter_NonTwoXX(t *testing.T) {
	ts := captureServer(t, 500, nil)
	defer ts.Close()

	err := (&SlackAlerter{webhookURL: ts.URL}).Send(context.Background(), testPayload())
	if err == nil {
		t.Fatal("expected error on 500 response")
	}
	if strings.Contains(err.Error(), ts.URL) {
		t.Errorf("error leaks URL: %v", err)
	}
}

func TestSlackAlerter_Timeout(t *testing.T) {
	ts := slowServer(t)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := (&SlackAlerter{webhookURL: ts.URL}).Send(ctx, testPayload())
	if err == nil {
		t.Fatal("expected error on timeout")
	}
	if strings.Contains(err.Error(), ts.URL) {
		t.Errorf("error leaks URL on timeout: %v", err)
	}
}

// ----------------------------------------------------------------------------
// Telegram
// ----------------------------------------------------------------------------

func TestTelegramAlerter_CorrectPayload(t *testing.T) {
	var got []byte
	ts := captureServer(t, 200, &got)
	defer ts.Close()

	a := newTelegramAlerter("TESTTOKEN", "12345", ts.URL)
	if err := a.Send(context.Background(), testPayload()); err != nil {
		t.Fatalf("Send() error: %v", err)
	}

	var p telegramPayload
	if err := json.Unmarshal(got, &p); err != nil {
		t.Fatalf("unmarshal telegram payload: %v", err)
	}
	if p.ChatID != "12345" {
		t.Errorf("chat_id = %q; want \"12345\"", p.ChatID)
	}
	for _, want := range []string{testPayload().TokenType, testPayload().TokenID, testPayload().Path} {
		if !strings.Contains(p.Text, want) {
			t.Errorf("telegram text missing %q; text = %q", want, p.Text)
		}
	}
	// No parse_mode field — plain text only to avoid HTML-escaping issues.
	if strings.Contains(string(got), "parse_mode") {
		t.Error("payload must not include parse_mode (plain text only)")
	}
}

func TestTelegramAlerter_NonTwoXX(t *testing.T) {
	ts := captureServer(t, 400, nil)
	defer ts.Close()

	err := newTelegramAlerter("TESTTOKEN", "12345", ts.URL).Send(context.Background(), testPayload())
	if err == nil {
		t.Fatal("expected error on 400 response")
	}
	// Must not leak the bot token.
	if strings.Contains(err.Error(), "TESTTOKEN") {
		t.Errorf("error leaks bot token: %v", err)
	}
}

func TestTelegramAlerter_Timeout(t *testing.T) {
	ts := slowServer(t)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := newTelegramAlerter("TESTTOKEN", "12345", ts.URL).Send(ctx, testPayload())
	if err == nil {
		t.Fatal("expected error on timeout")
	}
	// Must not leak the bot token even in timeout error.
	if strings.Contains(err.Error(), "TESTTOKEN") {
		t.Errorf("error leaks bot token on timeout: %v", err)
	}
}

// ----------------------------------------------------------------------------
// Teams
// ----------------------------------------------------------------------------

func TestTeamsAlerter_CorrectPayload(t *testing.T) {
	var got []byte
	ts := captureServer(t, 200, &got)
	defer ts.Close()

	if err := (&TeamsAlerter{webhookURL: ts.URL}).Send(context.Background(), testPayload()); err != nil {
		t.Fatalf("Send() error: %v", err)
	}

	var p teamsPayload
	if err := json.Unmarshal(got, &p); err != nil {
		t.Fatalf("unmarshal teams payload: %v", err)
	}
	if p.Type != "MessageCard" {
		t.Errorf("@type = %q; want \"MessageCard\"", p.Type)
	}
	if len(p.Sections) != 1 {
		t.Fatalf("sections len = %d; want 1", len(p.Sections))
	}
	if len(p.Sections[0].Facts) < 5 {
		t.Errorf("facts len = %d; want >= 5", len(p.Sections[0].Facts))
	}
	var found bool
	for _, f := range p.Sections[0].Facts {
		if f.Name == "Token ID" && f.Value == testPayload().TokenID {
			found = true
		}
	}
	if !found {
		t.Error("Teams payload missing Token ID fact")
	}
}

func TestTeamsAlerter_NonTwoXX(t *testing.T) {
	ts := captureServer(t, 503, nil)
	defer ts.Close()

	err := (&TeamsAlerter{webhookURL: ts.URL}).Send(context.Background(), testPayload())
	if err == nil {
		t.Fatal("expected error on 503 response")
	}
	if strings.Contains(err.Error(), ts.URL) {
		t.Errorf("error leaks URL: %v", err)
	}
}

func TestTeamsAlerter_Timeout(t *testing.T) {
	ts := slowServer(t)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := (&TeamsAlerter{webhookURL: ts.URL}).Send(ctx, testPayload())
	if err == nil {
		t.Fatal("expected error on timeout")
	}
}

// ----------------------------------------------------------------------------
// ntfy
// ----------------------------------------------------------------------------

func TestNtfyAlerter_CorrectPayload(t *testing.T) {
	var gotBody []byte
	var gotHeaders http.Header
	ts := captureHeaderServer(t, &gotBody, &gotHeaders)
	defer ts.Close()

	a := &NtfyAlerter{serverURL: ts.URL, topic: "my-test-topic"}
	if err := a.Send(context.Background(), testPayload()); err != nil {
		t.Fatalf("Send() error: %v", err)
	}

	if !strings.Contains(string(gotBody), testPayload().TokenID) {
		t.Errorf("body missing token ID; body = %q", string(gotBody))
	}
	if gotHeaders.Get("Title") != "Decoyd Alert" {
		t.Errorf("Title header = %q; want \"Decoyd Alert\"", gotHeaders.Get("Title"))
	}
	if gotHeaders.Get("Priority") != "urgent" {
		t.Errorf("Priority header = %q; want \"urgent\"", gotHeaders.Get("Priority"))
	}
}

func TestNtfyAlerter_NonTwoXX(t *testing.T) {
	ts := captureServer(t, 403, nil)
	defer ts.Close()

	err := (&NtfyAlerter{serverURL: ts.URL, topic: "t"}).Send(context.Background(), testPayload())
	if err == nil {
		t.Fatal("expected error on 403 response")
	}
	if strings.Contains(err.Error(), ts.URL) {
		t.Errorf("error leaks URL: %v", err)
	}
}

func TestNtfyAlerter_Timeout(t *testing.T) {
	ts := slowServer(t)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := (&NtfyAlerter{serverURL: ts.URL, topic: "t"}).Send(ctx, testPayload())
	if err == nil {
		t.Fatal("expected error on timeout")
	}
}

// ----------------------------------------------------------------------------
// Generic webhook
// ----------------------------------------------------------------------------

func TestWebhookAlerter_CorrectPayload(t *testing.T) {
	var got []byte
	ts := captureServer(t, 200, &got)
	defer ts.Close()

	if err := (&WebhookAlerter{webhookURL: ts.URL}).Send(context.Background(), testPayload()); err != nil {
		t.Fatalf("Send() error: %v", err)
	}

	// Must round-trip back to AlertPayload exactly.
	var p AlertPayload
	if err := json.Unmarshal(got, &p); err != nil {
		t.Fatalf("unmarshal webhook payload: %v", err)
	}
	orig := testPayload()
	if p.TokenID != orig.TokenID {
		t.Errorf("TokenID = %q; want %q", p.TokenID, orig.TokenID)
	}
	if p.TokenType != orig.TokenType {
		t.Errorf("TokenType = %q; want %q", p.TokenType, orig.TokenType)
	}
	if p.Path != orig.Path {
		t.Errorf("Path = %q; want %q", p.Path, orig.Path)
	}
	if !p.TriggeredAt.Equal(orig.TriggeredAt) {
		t.Errorf("TriggeredAt = %v; want %v", p.TriggeredAt, orig.TriggeredAt)
	}
	if p.Detail != orig.Detail {
		t.Errorf("Detail = %q; want %q", p.Detail, orig.Detail)
	}
}

func TestWebhookAlerter_JSONFieldNames(t *testing.T) {
	var got []byte
	ts := captureServer(t, 200, &got)
	defer ts.Close()

	_ = (&WebhookAlerter{webhookURL: ts.URL}).Send(context.Background(), testPayload())

	// Spec requires the canonical JSON field names.
	for _, want := range []string{`"token_id"`, `"token_type"`, `"path"`, `"triggered_at"`, `"detail"`} {
		if !strings.Contains(string(got), want) {
			t.Errorf("payload missing JSON field %s; body = %s", want, string(got))
		}
	}
}

func TestWebhookAlerter_NonTwoXX(t *testing.T) {
	ts := captureServer(t, 401, nil)
	defer ts.Close()

	err := (&WebhookAlerter{webhookURL: ts.URL}).Send(context.Background(), testPayload())
	if err == nil {
		t.Fatal("expected error on 401 response")
	}
}

func TestWebhookAlerter_Timeout(t *testing.T) {
	ts := slowServer(t)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := (&WebhookAlerter{webhookURL: ts.URL}).Send(ctx, testPayload())
	if err == nil {
		t.Fatal("expected error on timeout")
	}
}

// ----------------------------------------------------------------------------
// sanitizeErr
// ----------------------------------------------------------------------------

func TestSanitizeErr_StripsURLFromURLError(t *testing.T) {
	inner := errors.New("connection refused")
	urlErr := &url.Error{Op: "Post", URL: "https://discord.com/api/webhooks/123/SECRET", Err: inner}
	got := sanitizeErr(urlErr)
	if strings.Contains(got.Error(), "discord.com") {
		t.Errorf("sanitizeErr leaked URL: %v", got)
	}
	if strings.Contains(got.Error(), "SECRET") {
		t.Errorf("sanitizeErr leaked secret: %v", got)
	}
	if !strings.Contains(got.Error(), "Post") {
		t.Errorf("sanitizeErr should preserve Op; got: %v", got)
	}
}

func TestSanitizeErr_PassthroughNonURLError(t *testing.T) {
	plain := errors.New("something else")
	if sanitizeErr(plain) != plain {
		t.Error("sanitizeErr should not wrap non-url.Error")
	}
}

func TestSanitizeErr_Nil(t *testing.T) {
	if sanitizeErr(nil) != nil {
		t.Error("sanitizeErr(nil) must return nil")
	}
}

// ----------------------------------------------------------------------------
// MaskSecret
// ----------------------------------------------------------------------------

func TestMaskSecret(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"ab", "••"},
		{"abcd", "••••"},
		{"abcde", "•bcde"},
		// 10-char input → 6 dots + last 4
		{"0123456789", "••••••6789"},
	}
	for _, tc := range tests {
		got := MaskSecret(tc.input)
		if got != tc.want {
			t.Errorf("MaskSecret(%q) = %q; want %q", tc.input, got, tc.want)
		}
	}
}

// ----------------------------------------------------------------------------
// Config Load/Save
// ----------------------------------------------------------------------------

func TestSave_WritesJSON(t *testing.T) {
	dir := t.TempDir()
	cfg := AlertConfig{
		Channels: []ChannelConfig{
			{Type: ChannelDiscord, WebhookURL: "https://discord.com/api/webhooks/test"},
		},
		DefaultIndex: 0,
	}
	if err := Save(dir, cfg); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() after Save() error: %v", err)
	}
	if len(loaded.Channels) != 1 {
		t.Errorf("channels len = %d; want 1", len(loaded.Channels))
	}
	if loaded.Channels[0].WebhookURL != cfg.Channels[0].WebhookURL {
		t.Error("WebhookURL mismatch after round-trip")
	}
}

func TestSave_FilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permissions not enforced on Windows")
	}
	dir := t.TempDir()
	if err := Save(dir, AlertConfig{}); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, configFileName))
	if err != nil {
		t.Fatalf("stat config file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("config file perm = %04o; want 0600", got)
	}
}

func TestLoad_FileNotExist(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load on missing file should not error; got: %v", err)
	}
	if len(cfg.Channels) != 0 {
		t.Errorf("expected empty channels; got %d", len(cfg.Channels))
	}
}

// ----------------------------------------------------------------------------
// NewAlerter
// ----------------------------------------------------------------------------

func TestNewAlerter_Misconfigured(t *testing.T) {
	tests := []struct {
		name string
		cfg  ChannelConfig
	}{
		{"discord no URL", ChannelConfig{Type: ChannelDiscord}},
		{"slack no URL", ChannelConfig{Type: ChannelSlack}},
		{"telegram no token", ChannelConfig{Type: ChannelTelegram, ChatID: "123"}},
		{"telegram no chatID", ChannelConfig{Type: ChannelTelegram, BotToken: "tok"}},
		{"teams no URL", ChannelConfig{Type: ChannelTeams}},
		{"ntfy no topic", ChannelConfig{Type: ChannelNtfy}},
		{"webhook no URL", ChannelConfig{Type: ChannelWebhook}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewAlerter(tc.cfg)
			if !errors.Is(err, ErrMisconfigured) {
				t.Errorf("expected ErrMisconfigured; got %v", err)
			}
		})
	}
}

func TestNewAlerter_UnknownType(t *testing.T) {
	_, err := NewAlerter(ChannelConfig{Type: "fax_machine"})
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
	if errors.Is(err, ErrMisconfigured) {
		t.Error("unknown type should not return ErrMisconfigured")
	}
}
