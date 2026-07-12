package alert

import (
	"context"
	"time"
)

// DiscordAlerter sends a formatted embed to a Discord incoming webhook.
type DiscordAlerter struct {
	webhookURL string
}

// --- Wire types -------------------------------------------------------------

type discordPayload struct {
	Embeds []discordEmbed `json:"embeds"`
}

type discordEmbed struct {
	Title     string         `json:"title"`
	Color     int            `json:"color"` // 0xRRGGBB decimal
	Fields    []discordField `json:"fields"`
	Timestamp string         `json:"timestamp"` // ISO 8601
}

type discordField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

// discordDanger is the embed accent colour for alert messages (red).
const discordDanger = 0xF04747

// --- Send -------------------------------------------------------------------

func (a *DiscordAlerter) Send(ctx context.Context, p AlertPayload) error {
	body := discordPayload{
		Embeds: []discordEmbed{{
			Title: "Decoyd Alert",
			Color: discordDanger,
			Fields: []discordField{
				{Name: "Token ID", Value: p.TokenID, Inline: true},
				{Name: "Type", Value: p.TokenType, Inline: true},
				{Name: "Path", Value: p.Path, Inline: false},
				{Name: "Time", Value: p.TriggeredAt.UTC().Format(time.RFC3339), Inline: true},
				{Name: "Detail", Value: detailOrNone(p.Detail), Inline: false},
			},
			Timestamp: p.TriggeredAt.UTC().Format(time.RFC3339),
		}},
	}
	return doPost(ctx, a.webhookURL, body)
}

// detailOrNone returns p or "(none)" if p is empty — Discord rejects blank field values.
func detailOrNone(p string) string {
	if p == "" {
		return "(none)"
	}
	return p
}
