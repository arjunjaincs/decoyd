// triggerdetail.go — Phase 4 trigger event detail screen.
package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/arjunjaincs/decoyd/internal/triglog"
)

// ----------------------------------------------------------------------------
// Messages
// ----------------------------------------------------------------------------

// TriggerDetailDoneMsg is emitted when the user presses esc to return to Status.
type TriggerDetailDoneMsg struct{}

// TriggerDetailDeletedMsg is emitted after the event has been deleted from the
// log. The status screen handles this by refreshing its event list.
type TriggerDetailDeletedMsg struct{}

// ----------------------------------------------------------------------------
// TriggerDetailModel
// ----------------------------------------------------------------------------

// TriggerDetailModel shows the full details of a single trigger event.
//
// Fields shown:
//   - Token type + short ID (first 8 chars)
//   - Full deployed path
//   - Timestamp (local) + relative "X ago"
//   - Event type (access / write / rename / delete)
//   - Process attribution: always "unknown" — process attribution requires
//     ETW/eBPF which are out of scope for v1. Stated explicitly, not implied.
//   - Alert status with error text when status == failed
//   - Full event ID (for correlation with triggers.jsonl)
type TriggerDetailModel struct {
	width         int
	height        int
	event         triglog.TriggerEvent
	dataDir       string
	deleteConfirm bool // true after first 'd' press; second 'd' commits delete
}

// NewTriggerDetailModel constructs the detail model for the given event.
func NewTriggerDetailModel(width, height int, event triglog.TriggerEvent, dataDir string) TriggerDetailModel {
	return TriggerDetailModel{
		width:   width,
		height:  height,
		event:   event,
		dataDir: dataDir,
	}
}

// ----------------------------------------------------------------------------
// Init / Update / View
// ----------------------------------------------------------------------------

func (m TriggerDetailModel) Init() tea.Cmd { return nil }

func (m TriggerDetailModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q":
			m.deleteConfirm = false
			return m, func() tea.Msg { return TriggerDetailDoneMsg{} }

		case "d", "D":
			if !m.deleteConfirm {
				// First press: show confirmation in footer.
				m.deleteConfirm = true
			} else {
				// Second press: commit the delete and return to status.
				_ = triglog.DeleteOne(m.dataDir, m.event.ID)
				return m, func() tea.Msg { return TriggerDetailDeletedMsg{} }
			}
			return m, nil
		}
	}
	return m, nil
}

func (m TriggerDetailModel) View() string {
	ev := m.event
	var b strings.Builder

	shortID := ev.TokenID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}

	ago := formatAgo(time.Since(ev.TriggeredAt))
	ts := ev.TriggeredAt.Local().Format("2006-01-02 15:04:05 MST")

	row := func(label, value string) {
		b.WriteString(MutedStyle.Render(fmt.Sprintf("%-20s", label+":")))
		b.WriteString("  ")
		b.WriteString(NormalItemStyle.Render(value))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	row("Token type", ev.TokenType)
	row("Token ID (short)", shortID)
	row("Deployed path", ev.Path)
	row("Triggered at", fmt.Sprintf("%s  (%s)", ts, ago))
	row("Event type", ev.EventType)

	// Process attribution — always unknown in v1.
	// Linux: process attribution requires eBPF (fanotify FAN_REPORT_PID or
	// audit subsystem). Windows: requires ETW kernel provider. Both are out
	// of scope for v1. This is a known limitation, not a bug.
	row("Process", "unknown (v1 limitation — requires eBPF/ETW)")

	// Alert status.
	statusLine := alertStatusLine(ev)
	row("Alert status", statusLine)

	b.WriteString("\n")
	row("Event ID (full)", ev.ID)

	b.WriteString("\n")
	if m.deleteConfirm {
		b.WriteString(WarningStyle.Render("Press d again to delete this event from the log — cannot be undone."))
	} else {
		b.WriteString(HelpTextStyle.Render("d delete event   esc / q back"))
	}

	boxW := ScreenBoxWidth(m.width, 80)
	box := renderBoxInner("Trigger Detail", b.String(), boxW, ColorBorder)
	return PlaceScreen(m.width, m.height, box)
}

// alertStatusLine produces the status + error display for the alert outcome.
func alertStatusLine(ev triglog.TriggerEvent) string {
	switch ev.Status {
	case triglog.TriggerSent:
		return G.OK + " sent"
	case triglog.TriggerFailed:
		msg := G.Fail + " failed"
		if ev.AlertError != "" {
			msg += ": " + ev.AlertError
		}
		return msg
	case triglog.TriggerRateLimited:
		return "~ rate-limited (see watcher config)"
	case triglog.TriggerQuietHours:
		return "z suppressed (quiet hours)"
	case triglog.TriggerPending:
		return G.Ellipsis + " pending (watcher may have crashed before completing dispatch)"
	default:
		return ev.Status
	}
}
