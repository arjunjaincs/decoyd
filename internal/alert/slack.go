package alert

import (
	"context"
	"fmt"
	"time"
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
	Type   string       `json:"type"`
	Text   *slackText   `json:"text,omitempty"`
	Fields []slackText  `json:"fields,omitempty"`
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

	body := slackPayload{
		Blocks: []slackBlock{
			{
				Type: "header",
				Text: &slackText{Type: "plain_text", Text: "Decoyd Alert"},
			},
			{
				Type: "section",
				Fields: []slackText{
					{Type: "mrkdwn", Text: fmt.Sprintf("*Token ID*\n%s", p.TokenID)},
					{Type: "mrkdwn", Text: fmt.Sprintf("*Type*\n%s", p.TokenType)},
					{Type: "mrkdwn", Text: fmt.Sprintf("*Path*\n%s", p.Path)},
					{Type: "mrkdwn", Text: fmt.Sprintf("*Time*\n%s", p.TriggeredAt.UTC().Format(time.RFC3339))},
				},
			},
			{
				Type: "section",
				Text: &slackText{Type: "mrkdwn", Text: fmt.Sprintf("*Detail*\n%s", detail)},
			},
		},
	}
	return doPost(ctx, a.webhookURL, body)
}
