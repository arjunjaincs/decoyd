package alert

import (
	"context"
	"fmt"
	"time"
)

// TelegramAlerter sends a plain-text message via the Telegram Bot API.
//
// Plain text (no parse_mode) is deliberate: HTML parse_mode requires the
// caller to HTML-escape all user-controlled fields (Path, Detail) which are
// plausible sources of <, >, & characters. Sending plain text avoids that
// error class entirely while still delivering all the information.
//
// The URL constructed is:  apiBase + "/bot" + botToken + "/sendMessage"
// botToken is therefore part of the URL path, which is why sanitizeErr
// (called by doPost on any HTTP error) is critical here — without it any
// network failure would leak the token into the error message displayed in
// the TUI or written to logs.
type TelegramAlerter struct {
	botToken string
	chatID   string
	// apiBase is the Telegram API root. Defaults to "https://api.telegram.org".
	// Overridable for testing (set to an httptest.Server URL).
	apiBase string
}

// newTelegramAlerter is the package-internal constructor.
// apiBase may be empty (defaults to "https://api.telegram.org").
func newTelegramAlerter(botToken, chatID, apiBase string) *TelegramAlerter {
	if apiBase == "" {
		apiBase = "https://api.telegram.org"
	}
	return &TelegramAlerter{botToken: botToken, chatID: chatID, apiBase: apiBase}
}

// --- Wire types -------------------------------------------------------------

type telegramPayload struct {
	ChatID string `json:"chat_id"`
	Text   string `json:"text"`
}

// --- Send -------------------------------------------------------------------

func (a *TelegramAlerter) Send(ctx context.Context, p AlertPayload) error {
	detail := p.Detail
	if detail == "" {
		detail = "(none)"
	}

	text := fmt.Sprintf(
		"[DECOYD ALERT]\nToken: %s (%s)\nPath:  %s\nTime:  %s\nDetail: %s",
		p.TokenType,
		p.TokenID,
		p.Path,
		p.TriggeredAt.UTC().Format(time.RFC3339),
		detail,
	)

	// apiURL contains the bot token in its path — sanitizeErr (called inside
	// doPost on any HTTP error) strips the URL before the error surfaces.
	apiURL := fmt.Sprintf("%s/bot%s/sendMessage", a.apiBase, a.botToken)
	body := telegramPayload{ChatID: a.chatID, Text: text}
	return doPost(ctx, apiURL, body)
}
