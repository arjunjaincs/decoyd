package alert

import (
	"context"
	"fmt"
	"time"

	"github.com/arjunjaincs/decoyd/internal/sanitize"
)

// SlackAlerter sends a Block Kit message to a Slack incoming webhook.
type SlackAlerter struct {
	webhookURL string
}

// --- Wire types -------------------------------------------------------------

type slackPayload struct {
	Blocks []slackBlock `json:"blocks"`
}

type slackBlock struct {
	Type   string      `json:"type"`
	Text   *slackText  `json:"text,omitempty"`
	Fields []slackText `json:"fields,omitempty"`
}

type slackText struct {
	Type string `json:"type"` // "plain_text" or "mrkdwn"
	Text string `json:"text"`
}

// --- Send -------------------------------------------------------------------

func (a *SlackAlerter) Send(ctx context.Context, p AlertPayload) error {
	detail := p.Detail
	if detail == "" {
		detail = "(none)"
	}

	// Use plain_text for all user-data fields: Slack does not expand mentions
	// or apply markdown in plain_text objects.
	// Ref: https://api.slack.com/reference/block-kit/composition-objects#text
	body := slackPayload{
		Blocks: []slackBlock{
			{
				Type: "header",
				Text: &slackText{Type: "plain_text", Text: "Decoyd Alert"},
			},
			{
				Type: "section",
				Fields: []slackText{
					{Type: "plain_text", Text: fmt.Sprintf("Token ID: %s", sanitize.Sanitize(p.TokenID, 64))},
					{Type: "plain_text", Text: fmt.Sprintf("Type: %s", sanitize.Sanitize(p.TokenType, 64))},
					{Type: "plain_text", Text: fmt.Sprintf("Path: %s", sanitize.Sanitize(p.Path, 512))},
					{Type: "plain_text", Text: fmt.Sprintf("Time: %s", p.TriggeredAt.UTC().Format(time.RFC3339))},
				},
			},
			{
				Type: "section",
				Text: &slackText{Type: "plain_text", Text: fmt.Sprintf("Detail: %s", sanitize.Sanitize(detail, 512))},
			},
		},
	}
	return doPost(ctx, a.webhookURL, body)
}
