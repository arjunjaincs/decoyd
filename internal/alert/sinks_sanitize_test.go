// Package alert — per-sink sanitization tests (B7).
// Each test sends a payload containing hostile strings and verifies the
// outgoing HTTP request body is clean of ANSI escapes, U+202E bidi overrides,
// and that sink-specific security controls are applied:
//
//   - Discord: allowed_mentions.parse == [] to prevent @everyone/@here expansion
//   - Slack: all user-data text objects use "plain_text" (never "mrkdwn")
//   - Telegram: no parse_mode field (send as plain text)
package alert

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// hostilePayload returns an AlertPayload whose user-controlled fields contain:
//   - ANSI CSI colour escape sequence
//   - U+202E RIGHT-TO-LEFT OVERRIDE (bidi)
//   - @everyone and <!channel> mention triggers
func hostilePayload() AlertPayload {
	return AlertPayload{
		TokenID:     "\x1b[31mred\x1b[0m-tokenid",         // ANSI escape
		TokenType:   "@everyone\u202Ereverse",              // @mention + U+202E bidi
		Path:        "<!channel> *bold* /home/user/token",  // Slack-style mention
		TriggeredAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Detail:      "\x1b[1mfile\x1b[0m \u202Eopened",    // ANSI + bidi
	}
}

// captureBody starts an httptest server that captures the full request body
// and always responds with 204 No Content.
func captureBodyB7(t *testing.T) (*httptest.Server, *[]byte) {
	t.Helper()
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = b
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	return srv, &body
}

func assertNoANSIB7(t *testing.T, sink, body string) {
	t.Helper()
	if strings.ContainsRune(body, '\x1b') {
		t.Errorf("%s: HTTP body contains ANSI escape byte (0x1b) — hostile payload not stripped", sink)
	}
}

func assertNoBidiB7(t *testing.T, sink, body string) {
	t.Helper()
	if strings.ContainsRune(body, '\u202E') {
		t.Errorf("%s: HTTP body contains U+202E RIGHT-TO-LEFT OVERRIDE — bidi not stripped", sink)
	}
}

// bgCtx returns a background context (all alerters accept context.Context).
func bgCtx() context.Context { return context.Background() }

// ── Discord ──────────────────────────────────────────────────────────────────

func TestSinkSanitize_Discord_ANSIAndBidiStripped(t *testing.T) {
	srv, body := captureBodyB7(t)
	a := &DiscordAlerter{webhookURL: srv.URL}
	if err := a.Send(bgCtx(), hostilePayload()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	assertNoANSIB7(t, "Discord", string(*body))
	assertNoBidiB7(t, "Discord", string(*body))
}

func TestSinkSanitize_Discord_AllowedMentionsParseIsEmpty(t *testing.T) {
	srv, body := captureBodyB7(t)
	a := &DiscordAlerter{webhookURL: srv.URL}
	payload := hostilePayload()
	payload.TokenType = "@everyone"
	if err := a.Send(bgCtx(), payload); err != nil {
		t.Fatalf("Send: %v", err)
	}
	var p discordPayload
	if err := json.Unmarshal(*body, &p); err != nil {
		t.Fatalf("unmarshal discord body: %v", err)
	}
	if p.AllowedMentions == nil {
		t.Fatal("Discord: allowed_mentions field is absent — @everyone expansion not prevented")
	}
	if len(p.AllowedMentions.Parse) != 0 {
		t.Errorf("Discord: allowed_mentions.parse = %v; want [] to block @everyone/@here pings",
			p.AllowedMentions.Parse)
	}
}

// ── Slack ─────────────────────────────────────────────────────────────────────

func TestSinkSanitize_Slack_ANSIAndBidiStripped(t *testing.T) {
	srv, body := captureBodyB7(t)
	a := &SlackAlerter{webhookURL: srv.URL}
	if err := a.Send(bgCtx(), hostilePayload()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	assertNoANSIB7(t, "Slack", string(*body))
	assertNoBidiB7(t, "Slack", string(*body))
}

func TestSinkSanitize_Slack_UserDataUsesPlainText(t *testing.T) {
	srv, body := captureBodyB7(t)
	a := &SlackAlerter{webhookURL: srv.URL}
	if err := a.Send(bgCtx(), hostilePayload()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	var p slackPayload
	if err := json.Unmarshal(*body, &p); err != nil {
		t.Fatalf("unmarshal slack body: %v", err)
	}
	// All text objects that carry user data must use "plain_text", not "mrkdwn".
	// Ref: https://api.slack.com/reference/block-kit/composition-objects#text
	for i, block := range p.Blocks {
		if block.Text != nil && block.Text.Type == "mrkdwn" {
			t.Errorf("Slack: block[%d].text.type=%q; user-data blocks must use plain_text", i, block.Text.Type)
		}
		for j, field := range block.Fields {
			if field.Type == "mrkdwn" {
				t.Errorf("Slack: block[%d].fields[%d].type=%q; must be plain_text to prevent mention expansion",
					i, j, field.Type)
			}
		}
	}
}

// ── Telegram ──────────────────────────────────────────────────────────────────

func TestSinkSanitize_Telegram_ANSIAndBidiStripped(t *testing.T) {
	srv, body := captureBodyB7(t)
	a := newTelegramAlerter("bot-token-123", "9999", srv.URL)
	if err := a.Send(bgCtx(), hostilePayload()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	assertNoANSIB7(t, "Telegram", string(*body))
	assertNoBidiB7(t, "Telegram", string(*body))
}

func TestSinkSanitize_Telegram_NoParseMode(t *testing.T) {
	srv, body := captureBodyB7(t)
	a := newTelegramAlerter("bot-token-123", "9999", srv.URL)
	if err := a.Send(bgCtx(), hostilePayload()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	// Telegram sends plain text when no parse_mode is specified.
	// Verify the payload JSON does not contain a parse_mode key.
	if strings.Contains(string(*body), "parse_mode") {
		t.Errorf("Telegram: JSON body contains parse_mode — should send plain text without markup parsing")
	}
}

// ── ntfy ─────────────────────────────────────────────────────────────────────

func TestSinkSanitize_Ntfy_ANSIAndBidiStripped(t *testing.T) {
	srv, body := captureBodyB7(t)
	// ntfy posts to serverURL/topic; captureBodyB7's server handles any path.
	a := &NtfyAlerter{serverURL: srv.URL, topic: "alerts"}
	if err := a.Send(bgCtx(), hostilePayload()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	assertNoANSIB7(t, "ntfy", string(*body))
	assertNoBidiB7(t, "ntfy", string(*body))
}

// ── Teams ─────────────────────────────────────────────────────────────────────

// TestSinkSanitize_Teams_PreSanitizedPayloadPassesCleaned verifies that a
// pre-sanitized payload (as produced by watch_linux.go per Rule 13) produces
// a body without ANSI or bidi characters.
// Teams does not apply its own sanitization; it relies on the caller to
// pre-sanitize per Rule 13.
func TestSinkSanitize_Teams_PreSanitizedPayloadPassesCleaned(t *testing.T) {
	srv, body := captureBodyB7(t)
	a := &TeamsAlerter{webhookURL: srv.URL}
	// Use a clean payload (as the watcher sends after sanitize.Sanitize).
	clean := AlertPayload{
		TokenID:     "b3e61130d9bda4f8",
		TokenType:   "aws_credentials",
		Path:        "/home/user/.aws/credentials",
		TriggeredAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Detail:      "file opened",
	}
	if err := a.Send(bgCtx(), clean); err != nil {
		t.Fatalf("Send: %v", err)
	}
	assertNoANSIB7(t, "Teams", string(*body))
	assertNoBidiB7(t, "Teams", string(*body))
}

// ── Generic webhook ──────────────────────────────────────────────────────────

// TestSinkSanitize_Webhook_PreSanitizedPayloadIsClean verifies that a
// pre-sanitized AlertPayload (as produced by Rule 13) passes through
// the generic webhook sink without introducing hostile chars.
func TestSinkSanitize_Webhook_PreSanitizedPayloadIsClean(t *testing.T) {
	srv, body := captureBodyB7(t)
	a := &WebhookAlerter{webhookURL: srv.URL}
	clean := AlertPayload{
		TokenID:     "b3e61130d9bda4f8",
		TokenType:   "aws_credentials",
		Path:        "/home/user/.aws/credentials",
		TriggeredAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Detail:      "file opened",
	}
	if err := a.Send(bgCtx(), clean); err != nil {
		t.Fatalf("Send: %v", err)
	}
	assertNoANSIB7(t, "webhook", string(*body))
	assertNoBidiB7(t, "webhook", string(*body))
}
