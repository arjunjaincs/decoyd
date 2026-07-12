package alert

import (
	"context"
	"fmt"
	"time"
)

// NtfyAlerter sends a push notification via ntfy (ntfy.sh or self-hosted).
// Topic functions as a shared secret for public ntfy instances; it is masked
// in TUI display via MaskSecret and never included in error strings.
//
// Auth tokens (for self-hosted ntfy with authentication) are deferred to
// Phase 5 — the Phase 3 form only exposes ServerURL + Topic.
type NtfyAlerter struct {
	serverURL string
	topic     string
}

// --- Send -------------------------------------------------------------------

func (a *NtfyAlerter) Send(ctx context.Context, p AlertPayload) error {
	detail := p.Detail
	if detail == "" {
		detail = "(none)"
	}

	// ntfy expects the full URL: serverURL + "/" + topic
	targetURL := fmt.Sprintf("%s/%s", a.serverURL, a.topic)

	body := fmt.Sprintf(
		"Token: %s (%s)\nPath: %s\nTime: %s\nDetail: %s",
		p.TokenType,
		p.TokenID,
		p.Path,
		p.TriggeredAt.UTC().Format(time.RFC3339),
		detail,
	)

	headers := map[string]string{
		"Title":    "Decoyd Alert",
		"Priority": "urgent",
		"Tags":     "warning",
	}

	return doPostText(ctx, targetURL, body, headers)
}
