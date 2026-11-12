package alert

import "context"

// WebhookAlerter POSTs the AlertPayload as JSON to any arbitrary URL.
// This is the escape-hatch channel for integrations not explicitly supported.
//
// The payload is the AlertPayload struct itself, JSON-marshalled — spec
// mandates that the generic webhook "sends valid, parseable JSON matching
// AlertPayload exactly".
type WebhookAlerter struct {
	webhookURL string
}

// --- Send -------------------------------------------------------------------

func (a *WebhookAlerter) Send(ctx context.Context, p AlertPayload) error {
	// Sanitize user-controlled fields before serialising to JSON.
	// This strips ANSI escapes, bidi overrides, and C0/C1 control chars
	// from fields that may carry attacker-controlled strings.
	p = p.Sanitized()
	// AlertPayload has json tags that produce the canonical field names
	// (token_id, token_type, path, triggered_at, detail).
	return doPost(ctx, a.webhookURL, p)
}
