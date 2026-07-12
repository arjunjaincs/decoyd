package tui

// triggerdetail.go — Phase 4 trigger detail view.
//
// Wireframe (spec §Phase 4):
//
//	┌─ Trigger Detail ─────────────────────────────┐
//	│ Token:      .env (id: 4f2a91c8)               │
//	│ Path:       ~/projects/.env                    │
//	│ Time:       2026-07-11 14:32:07                │
//	│ Event:      file opened                        │
//	│ Process:    unknown (best-effort, not resolved) │
//	│ Alert:      sent via Discord webhook  ✓         │
//	│                                                 │
//	└─────────────────────────────────────────────────┘
//	 esc back

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/arjunjaincs/decoyd/internal/triglog"
)

// ----------------------------------------------------------------------------
// Messages
// ----------------------------------------------------------------------------

// TriggerDetailDoneMsg is sent when the user presses Esc in the detail view.
type TriggerDetailDoneMsg struct{}

// ----------------------------------------------------------------------------
// TriggerDetailModel
// ----------------------------------------------------------------------------

// TriggerDetailModel is the bubbletea model for the trigger detail screen.
type TriggerDetailModel struct {
	width  int
	height int
	event  triglog.TriggerEvent
}

// NewTriggerDetailModel creates a detail model for the given trigger event.
func NewTriggerDetailModel(width, height int, ev triglog.TriggerEvent) TriggerDetailModel {
	return TriggerDetailModel{width: width, height: height, event: ev}
}

// Init satisfies tea.Model.
func (m TriggerDetailModel) Init() tea.Cmd { return nil }

// Update handles keyboard input on the detail screen.
func (m TriggerDetailModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q":
			return m, func() tea.Msg { return TriggerDetailDoneMsg{} }
		}
	}
	return m, nil
}

// View renders the full trigger detail.
func (m TriggerDetailModel) View() string {
	if m.width < MinTermWidth {
		return NarrowTermMsg
	}
	boxW := m.width - 2
	if boxW < 10 {
		boxW = 10
	}

	ev := m.event
	label := lipgloss.NewStyle().Foreground(ColorTextMuted)
	val := lipgloss.NewStyle().Foreground(ColorTextPrimary)

	row := func(k, v string) string {
		return label.Render(fmt.Sprintf("  %-10s ", k)) + val.Render(v) + "\n"
	}

	var sb strings.Builder

	// Token row: "TypeName (id: shortID)"
	shortID := ev.TokenID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	typeName := ev.TokenType
	if typeName == "" {
		typeName = "unknown"
	}
	sb.WriteString(row("Token:", fmt.Sprintf("%s (id: %s)", typeName, shortID)))

	// Path: mask nothing — path is not a secret.
	sb.WriteString(row("Path:", ev.Path))

	// Time: local timezone, human-readable.
	sb.WriteString(row("Time:", ev.TriggeredAt.Local().Format("2006-01-02 15:04:05")))

	// Uptime-ago line.
	ago := formatAgo(time.Since(ev.TriggeredAt))
	sb.WriteString(row("", "("+ago+")"))

	// Event type.
	eventLabel := ev.EventType
	if eventLabel == "" {
		eventLabel = "unknown"
	}
	sb.WriteString(row("Event:", eventLabel))

	// Process attribution (always "unknown" in v1 — see PROGRESS.md).
	sb.WriteString(row("Process:", "unknown (best-effort, not resolved)"))

	sb.WriteString("\n")

	// Alert status.
	var alertLine string
	switch ev.Status {
	case triglog.TriggerSent:
		alertLine = lipgloss.NewStyle().Foreground(ColorPrimary).Render("sent ✓")
	case triglog.TriggerFailed:
		errStr := ev.AlertError
		if errStr == "" {
			errStr = "unknown error"
		}
		alertLine = lipgloss.NewStyle().Foreground(ColorDanger).Render("failed ✗") +
			MutedStyle.Render("  "+Truncate(errStr, 60))
	case triglog.TriggerPending:
		alertLine = lipgloss.NewStyle().Foreground(ColorWarning).Render("pending (process may have exited)")
	default:
		alertLine = MutedStyle.Render(string(ev.Status))
	}
	sb.WriteString(row("Alert:", alertLine))

	// Event ID (full, for copy-paste).
	sb.WriteString("\n")
	sb.WriteString(MutedStyle.Render(fmt.Sprintf("  event-id: %s", ev.ID)) + "\n")

	content := strings.TrimRight(sb.String(), "\n")
	box := renderBoxInner("Trigger Detail", content, boxW, ColorBorder)
	footer := HelpTextStyle.Render("esc back")
	return lipgloss.JoinVertical(lipgloss.Left, box, footer)
}
