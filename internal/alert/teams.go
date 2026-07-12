package alert

import (
	"context"
	"time"
)

// TeamsAlerter sends a MessageCard to a Microsoft Teams incoming webhook.
//
// The MessageCard format (legacy Connectors API) is used because it works
// with any Teams tenant without OAuth setup. Adaptive Cards (the newer format)
// require app registration and are deferred to Phase 5 polish.
type TeamsAlerter struct {
	webhookURL string
}

// --- Wire types -------------------------------------------------------------

type teamsPayload struct {
	Type       string         `json:"@type"`
	Context    string         `json:"@context"`
	Summary    string         `json:"summary"`
	ThemeColor string         `json:"themeColor"`
	Title      string         `json:"title"`
	Sections   []teamsSection `json:"sections"`
}

type teamsSection struct {
	Facts []teamsFact `json:"facts"`
}

type teamsFact struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// --- Send -------------------------------------------------------------------

func (a *TeamsAlerter) Send(ctx context.Context, p AlertPayload) error {
	detail := p.Detail
	if detail == "" {
		detail = "(none)"
	}

	body := teamsPayload{
		Type:       "MessageCard",
		Context:    "https://schema.org/extensions",
		Summary:    "Decoyd Alert",
		ThemeColor: "F04747",
		Title:      "Decoyd Alert",
		Sections: []teamsSection{{
			Facts: []teamsFact{
				{Name: "Token ID", Value: p.TokenID},
				{Name: "Type", Value: p.TokenType},
				{Name: "Path", Value: p.Path},
				{Name: "Time", Value: p.TriggeredAt.UTC().Format(time.RFC3339)},
				{Name: "Detail", Value: detail},
			},
		}},
	}
	return doPost(ctx, a.webhookURL, body)
}
