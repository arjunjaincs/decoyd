package alert

import (
	"context"
	"fmt"
	"time"

	"github.com/arjunjaincs/decoyd/internal/sanitize"
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

	// Sanitize user-controlled fields; ntfy displays as plain text.
	body := fmt.Sprintf(
		"Token: %s (%s)\nPath: %s\nTime: %s\nDetail: %s",
		sanitize.Sanitize(p.TokenType, 64),
		sanitize.Sanitize(p.TokenID, 64),
		sanitize.Sanitize(p.Path, 512),
		p.TriggeredAt.UTC().Format(time.RFC3339),
		sanitize.Sanitize(detail, 512),
	)

	headers := map[string]string{
		"Title":    "Decoyd Alert",
		"Priority": "urgent",
		"Tags":     "warning",
	}

	return doPostText(ctx, targetURL, body, headers)
}
