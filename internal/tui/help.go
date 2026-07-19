package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// HideHelpMsg is sent when the user dismisses the help overlay.
type HideHelpMsg struct{}

// ----------------------------------------------------------------------------
// Help keybinding table
// ----------------------------------------------------------------------------

// helpEntry is a single keybinding row in the overlay.
type helpEntry struct {
	key    string
	action string
}

// globalBindings is the canonical keybinding list shown in the help overlay.
// All screens share these bindings; screen-specific bindings are shown in each
// screen's footer rather than here.
var globalBindings = []helpEntry{
	{G.NavUp + " / k", "Move selection up"},
	{G.NavDown + " / j", "Move selection down"},
	{"Enter", "Confirm / select"},
	{"Space", "Toggle item (multi-select lists)"},
	{"Esc", "Back to previous screen"},
	{"?", "Toggle this help overlay"},
	{"q / Ctrl+C", "Quit"},
}

// ----------------------------------------------------------------------------
// HelpModel
// ----------------------------------------------------------------------------

// HelpModel renders the help overlay on top of the active screen.
type HelpModel struct {
	width  int
	height int
}

// NewHelpModel creates a HelpModel with the given terminal dimensions.
func NewHelpModel(width, height int) HelpModel {
	return HelpModel{width: width, height: height}
}

// Init satisfies tea.Model.
func (m HelpModel) Init() tea.Cmd {
	return nil
}

// Update handles Esc and ? to dismiss the overlay.
func (m HelpModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "?":
			return m, func() tea.Msg { return HideHelpMsg{} }
		}
	}
	return m, nil
}

// View renders the help overlay. It is composited by the root model on top of
// the active screen view.
func (m HelpModel) View() string {
	if m.width < MinTermWidth {
		return ""
	}

	// Key column width.
	keyColW := 16

	keyStyle := lipgloss.NewStyle().
		Foreground(ColorPrimary).
		Bold(true).
		Width(keyColW)
	descStyle := lipgloss.NewStyle().
		Foreground(ColorTextPrimary)
	headerStyle := lipgloss.NewStyle().
		Foreground(ColorTextMuted).
		Bold(true).
		MarginBottom(1)

	var rows string
	rows += headerStyle.Render("Global keybindings") + "\n"

	for _, b := range globalBindings {
		key := keyStyle.Render(b.key)
		desc := descStyle.Render(b.action)
		rows += key + desc + "\n"
	}

	// Trim trailing newline.
	if len(rows) > 0 && rows[len(rows)-1] == '\n' {
		rows = rows[:len(rows)-1]
	}

	footer := HelpTextStyle.Render("esc / ? close")

	boxWidth := m.width / 2
	if boxWidth < 40 {
		boxWidth = 40
	}
	if boxWidth > 70 {
		boxWidth = 70
	}

	box := renderBoxInner("Help", rows, boxWidth, ColorPrimary)

	return lipgloss.JoinVertical(lipgloss.Left, box, footer)
}
