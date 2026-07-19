// statusscreen.go — Phase 4 dashboard: watcher status + trigger event list.
package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/arjunjaincs/decoyd/internal/triglog"
	"github.com/arjunjaincs/decoyd/internal/watch"
)

// ----------------------------------------------------------------------------
// Messages
// ----------------------------------------------------------------------------

// StatusDoneMsg is emitted by StatusModel when the user presses esc.
type StatusDoneMsg struct{}

// ShowTriggerDetailMsg is emitted when the user presses enter on a trigger row.
type ShowTriggerDetailMsg struct {
	Event triglog.TriggerEvent
}

// statusTickMsg is the internal poll timer message.
type statusTickMsg struct{}

// ----------------------------------------------------------------------------
// StatusModel
// ----------------------------------------------------------------------------

// StatusModel is the Phase 4 dashboard. It shows:
//   - Watcher status: TUI-embedded / headless-running / headless-stale / not-running
//   - A newest-first list of trigger events (capped at maxStatusEvents), polled every 5s
//
// Three distinct watcher states (not binary):
//   - WatcherRef != nil: TUI-embedded watcher — query it directly via WatcherRef.Status()
//   - WatcherRef == nil, pid file alive: headless watcher (systemd/Task Scheduler)
//   - WatcherRef == nil, no file / stale pid: not running
type StatusModel struct {
	width   int
	height  int
	dataDir string

	// WatcherRef is set by root.go when a TUI-embedded watcher is running.
	// Nil means the TUI does not own the watcher (headless or not-running case).
	WatcherRef *watch.Watcher

	events       []triglog.TriggerEvent
	cursor       int
	loadErr      string
	clearConfirm bool   // true after first 'x' press; second 'x' commits the clear
	flashMsg     string // transient one-liner shown at the bottom after an action
}

const maxStatusEvents = 50

// NewStatusModel constructs the dashboard model.
func NewStatusModel(width, height int, dataDir string, watcherRef *watch.Watcher) StatusModel {
	return StatusModel{
		width:      width,
		height:     height,
		dataDir:    dataDir,
		WatcherRef: watcherRef,
	}
}

// ----------------------------------------------------------------------------
// Init
// ----------------------------------------------------------------------------

func (m StatusModel) Init() tea.Cmd {
	return tea.Batch(
		m.loadCmd(),
		m.tickCmd(),
	)
}

func (m StatusModel) loadCmd() tea.Cmd {
	return func() tea.Msg {
		events, err := triglog.Load(m.dataDir)
		if err != nil {
			return statusLoadErrMsg(err.Error())
		}
		if len(events) > maxStatusEvents {
			events = events[:maxStatusEvents]
		}
		return statusLoadedMsg(events)
	}
}

func (m StatusModel) tickCmd() tea.Cmd {
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg {
		return statusTickMsg{}
	})
}

type statusLoadedMsg []triglog.TriggerEvent
type statusLoadErrMsg string

// ----------------------------------------------------------------------------
// Update
// ----------------------------------------------------------------------------

func (m StatusModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case statusLoadedMsg:
		m.events = []triglog.TriggerEvent(msg)
		m.loadErr = ""
		if m.cursor >= len(m.events) && len(m.events) > 0 {
			m.cursor = len(m.events) - 1
		}
		return m, nil

	case statusLoadErrMsg:
		m.loadErr = string(msg)
		return m, nil

	case statusTickMsg:
		return m, tea.Batch(m.loadCmd(), m.tickCmd())

	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			m.clearConfirm = false
			return m, func() tea.Msg { return StatusDoneMsg{} }

		case "r", "R":
			m.clearConfirm = false
			m.flashMsg = ""
			return m, m.loadCmd()

		case "up", "k":
			m.clearConfirm = false
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil

		case "down", "j":
			m.clearConfirm = false
			if m.cursor < len(m.events)-1 {
				m.cursor++
			}
			return m, nil

		case "enter":
			m.clearConfirm = false
			if len(m.events) > 0 && m.cursor < len(m.events) {
				return m, func() tea.Msg {
					return ShowTriggerDetailMsg{Event: m.events[m.cursor]}
				}
			}
			return m, nil

		case "x", "X":
			if len(m.events) == 0 {
				return m, nil
			}
			if !m.clearConfirm {
				// First press: ask for confirmation.
				m.clearConfirm = true
				m.flashMsg = ""
			} else {
				// Second press: commit the clear.
				m.clearConfirm = false
				_ = triglog.ClearAll(m.dataDir)
				m.events = nil
				m.cursor = 0
				m.flashMsg = "All trigger logs cleared."
			}
			return m, nil
		}
	}
	return m, nil
}

// ----------------------------------------------------------------------------
// View
// ----------------------------------------------------------------------------

func (m StatusModel) View() string {
	var b strings.Builder

	// --- watcher status row ---
	b.WriteString(m.watcherStatusLine())
	b.WriteString("\n\n")

	// --- trigger list ---
	if m.loadErr != "" {
		b.WriteString(ErrorStyle.Render("Error loading triggers: " + m.loadErr))
		b.WriteString("\n")
	} else if len(m.events) == 0 {
		b.WriteString(MutedStyle.Render("No trigger events recorded yet."))
		b.WriteString("\n")
	} else {
		b.WriteString(MutedStyle.Render(fmt.Sprintf("Recent triggers (newest first, max %d):", maxStatusEvents)))
		b.WriteString("\n\n")
		for i, ev := range m.events {
			b.WriteString(m.renderEventRow(i, ev))
			b.WriteString("\n")
		}
	}

	b.WriteString("\n")

	// Footer: confirm-clear prompt overrides the normal hint bar.
	if m.clearConfirm {
		b.WriteString(WarningStyle.Render("Press x again to clear ALL trigger logs — this cannot be undone."))
	} else if m.flashMsg != "" {
		b.WriteString(SelectedItemStyle().Render(m.flashMsg))
	} else {
		b.WriteString(HelpTextStyle.Render(G.NavUp + "/" + G.NavDown + " navigate  enter detail  r refresh  x clear all  esc back"))
	}

	boxW := ScreenBoxWidth(m.width, 90)
	box := renderBoxInner("Status / Triggers", b.String(), boxW, ColorBorder)
	return PlaceScreen(m.width, m.height, box)
}

// watcherStatusLine produces the watcher-status summary row with three distinct states.
func (m StatusModel) watcherStatusLine() string {
	if m.WatcherRef != nil {
		// TUI-embedded: query the live watcher directly.
		st := m.WatcherRef.Status()
		if st.Running {
			line := fmt.Sprintf("%s running — watching %d file(s)", G.Bullet, st.Watching)
			return SelectedItemStyle().Render(line)
		}
		return WarningStyle.Render(G.Empty + " watcher stopped")
	}

	// Headless: read watcher.pid.
	state, pid := watch.HeadlessWatcherState(m.dataDir)
	switch state {
	case watch.HeadlessRunning:
		line := fmt.Sprintf("%s running (headless, PID %d)", G.Bullet, pid)
		return SelectedItemStyle().Render(line)
	case watch.HeadlessStale:
		line := fmt.Sprintf("%s stale lock (PID %d dead) — delete watcher.pid to clear", G.Warn, pid)
		return WarningStyle.Render(line)
	default: // HeadlessNotRunning
		return MutedStyle.Render(G.Empty + " watcher not running — start with: decoyd watch")
	}
}

// renderEventRow renders one trigger event row.
func (m StatusModel) renderEventRow(idx int, ev triglog.TriggerEvent) string {
	cursor := "  "
	if idx == m.cursor {
		cursor = G.Cursor + " "
	}

	timestamp := ev.TriggeredAt.Local().Format("2006-01-02 15:04:05")
	ago := formatAgo(time.Since(ev.TriggeredAt))
	shortID := ev.TokenID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}

	statusMark := statusMarker(ev.Status)
	line := fmt.Sprintf("%s%s  %-12s  %-10s  %s  %s (%s)",
		cursor, statusMark, ev.EventType, shortID, ev.TokenType, timestamp, ago)

	if idx == m.cursor {
		return SelectedItemStyle().Render(line)
	}
	return NormalItemStyle.Render(line)
}

// statusMarker returns a short visual indicator for the trigger status.
func statusMarker(status string) string {
	switch status {
	case triglog.TriggerSent:
		return G.OK
	case triglog.TriggerFailed:
		return G.Fail
	case triglog.TriggerRateLimited:
		return "~"
	case triglog.TriggerQuietHours:
		return "z"
	case triglog.TriggerPending:
		return G.Ellipsis
	default:
		return "?"
	}
}

// formatAgo returns a human-readable "X ago" string for a duration.
func formatAgo(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
