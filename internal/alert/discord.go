package alert

import (
	"context"
	"time"

	"github.com/arjunjaincs/decoyd/internal/sanitize"
)

// DiscordAlerter sends a formatted embed to a Discord incoming webhook.
type DiscordAlerter struct {
	webhookURL string
}

// --- Wire types -------------------------------------------------------------

// discordAllowedMentions disables all mention expansions so user-controlled
// strings cannot ping @everyone, @here, or any role/user.
// Ref: https://discord.com/developers/docs/resources/message#allowed-mentions-object
type discordAllowedMentions struct {
	Parse []string `json:"parse"`
}

type discordPayload struct {
	Embeds          []discordEmbed          `json:"embeds"`
	AllowedMentions *discordAllowedMentions `json:"allowed_mentions"`
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
	// Sanitize user-controlled fields; AllowedMentions disables all @pings.
	body := discordPayload{
		Embeds: []discordEmbed{{
			Title: "Decoyd Alert",
			Color: discordDanger,
			Fields: []discordField{
				{Name: "Token ID", Value: sanitize.Sanitize(p.TokenID, 64), Inline: true},
				{Name: "Type", Value: sanitize.Sanitize(p.TokenType, 64), Inline: true},
				{Name: "Path", Value: sanitize.Sanitize(p.Path, 512), Inline: false},
				{Name: "Time", Value: p.TriggeredAt.UTC().Format(time.RFC3339), Inline: true},
				{Name: "Detail", Value: detailOrNone(sanitize.Sanitize(p.Detail, 512)), Inline: false},
			},
			Timestamp: p.TriggeredAt.UTC().Format(time.RFC3339),
		}},
		AllowedMentions: &discordAllowedMentions{Parse: []string{}},
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
