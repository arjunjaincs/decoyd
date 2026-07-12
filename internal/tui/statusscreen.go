package tui

// statusscreen.go — Phase 4 dashboard / Status screen.
//
// Wireframe (spec §Phase 4):
//
//	┌─ Status ───────────────────────────────────────┐
//	│ Watcher   ● running     uptime 3d 4h            │
//	│ Tokens watched: 6                               │
//	│                                                  │
//	│ Recent triggers                                 │
//	│  ⚠ .env            2m ago      alert sent ✓     │
//	│  ⚠ id_ed25519       1d ago      alert sent ✓     │
//	│                                                  │
//	└──────────────────────────────────────────────────┘
//	 ↑/↓ browse   enter view detail   esc back   ? help
//
// The watcher is NOT started by this screen.  Status info comes from two
// sources:
//   - watch.Watcher (if one is already running in TUI-embedded mode) via
//     the WatcherRef field, set by root.go.  When nil, the screen shows
//     "not running" and offers no toggle — the user must start it via
//     "decoyd watch" from the CLI.
//   - triglog.Load(dataDir) — read every time the screen is opened or
//     refreshed — so headless-watcher triggers appear without a restart.
//
// Refresh strategy: poll every 5 seconds via tea.Tick for a live feed.
// This is intentional — "live" means triggers sent by a headless watcher
// (running in a separate process) show up within 5s of the file write.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/arjunjaincs/decoyd/internal/triglog"
	"github.com/arjunjaincs/decoyd/internal/watch"
)

// ----------------------------------------------------------------------------
// Messages
// ----------------------------------------------------------------------------

// StatusDoneMsg is sent when the user presses Esc to leave the Status screen.
type StatusDoneMsg struct{}

// ShowTriggerDetailMsg is sent when the user selects a trigger to drill into.
type ShowTriggerDetailMsg struct {
	Event triglog.TriggerEvent
}

// statusTickMsg is sent every 5 seconds to refresh the trigger list.
type statusTickMsg struct{}

// statusTickCmd returns a tea.Cmd that fires statusTickMsg after 5 seconds.
func statusTickCmd() tea.Cmd {
	return tea.Tick(5*time.Second, func(_ time.Time) tea.Msg {
		return statusTickMsg{}
	})
}

// ----------------------------------------------------------------------------
// StatusModel
// ----------------------------------------------------------------------------

// StatusModel is the bubbletea model for the dashboard screen.
type StatusModel struct {
	width   int
	height  int
	dataDir string

	// WatcherRef is optionally set by root.go to the running TUI-embedded
	// watcher so Status can query live WatcherStatus.  May be nil.
	WatcherRef *watch.Watcher

	// Loaded trigger events (newest-first, from triglog).
	triggers []triglog.TriggerEvent
	cursor   int // selected row in the trigger list

	// detailModel is owned here so root.go can access it without a map.
	detailModel TriggerDetailModel

	// err holds a non-fatal load error (shown in muted text).
	err string
}

// NewStatusModel creates a fresh StatusModel.
func NewStatusModel(width, height int, dataDir string) StatusModel {
	m := StatusModel{
		width:   width,
		height:  height,
		dataDir: dataDir,
	}
	m.reload()
	return m
}

func (m *StatusModel) reload() {
	events, err := triglog.Load(m.dataDir)
	if err != nil {
		m.err = sanitizeDashErr(err)
		return
	}
	m.err = ""
	// Cap at 50 for performance; triglog.Load already returns newest-first.
	if len(events) > 50 {
		events = events[:50]
	}
	m.triggers = events
	if m.cursor >= len(m.triggers) && len(m.triggers) > 0 {
		m.cursor = len(m.triggers) - 1
	}
}

// sanitizeDashErr caps error strings at 80 chars so they don't blow the layout.
func sanitizeDashErr(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if len(s) > 80 {
		s = s[:80] + "…"
	}
	return s
}

// Init satisfies tea.Model; starts the 5-second refresh ticker.
func (m StatusModel) Init() tea.Cmd {
	return statusTickCmd()
}

// Update handles input and tick messages.
func (m StatusModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height

	case statusTickMsg:
		m.reload()
		return m, statusTickCmd()

	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.triggers)-1 {
				m.cursor++
			}
		case "enter":
			if len(m.triggers) > 0 {
				ev := m.triggers[m.cursor]
				m.detailModel = NewTriggerDetailModel(m.width, m.height, ev)
				return m, func() tea.Msg { return ShowTriggerDetailMsg{Event: ev} }
			}
		case "r":
			// Manual refresh.
			m.reload()
		case "esc":
			return m, func() tea.Msg { return StatusDoneMsg{} }
		}
	}
	return m, nil
}

// View renders the Status dashboard.
func (m StatusModel) View() string {
	if m.width < MinTermWidth {
		return NarrowTermMsg
	}
	boxW := m.width - 2
	if boxW < 10 {
		boxW = 10
	}

	var sb strings.Builder

	// ── Watcher status row ────────────────────────────────────────────────
	sb.WriteString(m.renderWatcherRow() + "\n")
	sb.WriteString("\n")

	// ── Recent triggers header ────────────────────────────────────────────
	sb.WriteString(lipgloss.NewStyle().Bold(true).Foreground(ColorTextPrimary).Render("  Recent triggers") + "\n")

	if m.err != "" {
		sb.WriteString(MutedStyle.Render("  (load error: "+m.err+")") + "\n")
	} else if len(m.triggers) == 0 {
		sb.WriteString(MutedStyle.Render("  No triggers recorded yet.") + "\n")
	} else {
		for i, ev := range m.triggers {
			sb.WriteString(m.renderTriggerRow(i, ev) + "\n")
		}
	}

	content := strings.TrimRight(sb.String(), "\n")
	box := renderBoxInner("Status", content, boxW, ColorBorder)
	footer := HelpTextStyle.Render("↑/↓ browse   enter view detail   r refresh   esc back   ? help")
	return lipgloss.JoinVertical(lipgloss.Left, box, footer)
}

// renderWatcherRow renders the "Watcher ● running uptime…" summary line.
func (m StatusModel) renderWatcherRow() string {
	var status, tokenLine string

	if m.WatcherRef != nil {
		ws := m.WatcherRef.Status()
		if ws.Running {
			dot := lipgloss.NewStyle().Foreground(ColorPrimary).Render("●")
			uptime := formatUptime(time.Since(ws.StartedAt))
			status = MutedStyle.Render("  Watcher  ") + dot +
				lipgloss.NewStyle().Foreground(ColorPrimary).Render(" running") +
				MutedStyle.Render("     uptime "+uptime)
			tokenLine = MutedStyle.Render(fmt.Sprintf("  Tokens watched: %d", ws.Watching))
		} else {
			dot := lipgloss.NewStyle().Foreground(ColorTextMuted).Render("○")
			status = MutedStyle.Render("  Watcher  ") + dot + MutedStyle.Render(" stopped")
			tokenLine = ""
		}
	} else {
		// Check for a running headless watcher by reading watcher.pid.
		pidPath := filepath.Join(m.dataDir, "watcher.pid")
		if pidData, err := readPIDFile(pidPath); err == nil && pidData > 0 {
			dot := lipgloss.NewStyle().Foreground(ColorWarning).Render("●")
			status = MutedStyle.Render("  Watcher  ") + dot +
				lipgloss.NewStyle().Foreground(ColorWarning).Render(" running (headless)")
			tokenLine = MutedStyle.Render("  (started via 'decoyd watch')")
		} else {
			dot := lipgloss.NewStyle().Foreground(ColorTextMuted).Render("○")
			status = MutedStyle.Render("  Watcher  ") + dot + MutedStyle.Render(" not running")
			tokenLine = MutedStyle.Render("  Start with: decoyd watch")
		}
	}

	if tokenLine != "" {
		return status + "\n" + tokenLine
	}
	return status
}

// renderTriggerRow renders one row in the trigger list.
func (m StatusModel) renderTriggerRow(idx int, ev triglog.TriggerEvent) string {
	marker := "  "
	if idx == m.cursor {
		marker = "▸ "
	}

	// File basename for compact display.
	name := filepath.Base(ev.Path)
	if name == "" || name == "." {
		name = ev.TokenID
	}
	// Truncate long names.
	name = Truncate(name, 16)

	ago := formatAgo(time.Since(ev.TriggeredAt))

	var statusStr string
	switch ev.Status {
	case triglog.TriggerSent:
		statusStr = lipgloss.NewStyle().Foreground(ColorPrimary).Render("alert sent ✓")
	case triglog.TriggerFailed:
		statusStr = lipgloss.NewStyle().Foreground(ColorDanger).Render("alert failed ✗")
		if ev.AlertError != "" {
			statusStr += MutedStyle.Render(" " + Truncate(ev.AlertError, 30))
		}
	case triglog.TriggerPending:
		statusStr = lipgloss.NewStyle().Foreground(ColorWarning).Render("pending…")
	default:
		statusStr = MutedStyle.Render(string(ev.Status))
	}

	warn := lipgloss.NewStyle().Foreground(ColorWarning).Render("⚠")
	line := fmt.Sprintf("%s%s %-16s  %-8s  %s", marker, warn, name, ago, statusStr)

	if idx == m.cursor {
		return SelectedItemStyle().Render(marker+warn+" "+fmt.Sprintf("%-16s  %-8s  %s", name, ago, statusStr))
	}
	return NormalItemStyle.Render(line)
}

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

// formatUptime formats a duration as "Xd Yh" or "Xh Ym" or "Xs".
func formatUptime(d time.Duration) string {
	d = d.Round(time.Second)
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	secs := int(d.Seconds()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	case mins > 0:
		return fmt.Sprintf("%dm %ds", mins, secs)
	default:
		return fmt.Sprintf("%ds", secs)
	}
}

// formatAgo returns a compact "just now / Xm ago / Xh ago / Xd ago" string.
func formatAgo(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < 5*time.Second:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// readPIDFile reads an integer PID from a file. Returns 0 on any error.
func readPIDFile(path string) (int, error) {
	data, err := os.ReadFile(path) // #nosec G304 — path is internal dataDir file
	if err != nil {
		return 0, err
	}
	var pid int
	_, err = fmt.Sscan(strings.TrimSpace(string(data)), &pid)
	return pid, err
}
