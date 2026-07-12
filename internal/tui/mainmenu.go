package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ----------------------------------------------------------------------------
// Menu item definitions
// ----------------------------------------------------------------------------

// menuItem represents a single entry in the main menu.
type menuItem struct {
	label    string
	shortcut string // number key shortcut displayed to the user
}

var mainMenuItems = []menuItem{
	{label: "Generate a decoy", shortcut: "1"},
	{label: "Deploy existing decoys", shortcut: "2"},
	{label: "Alert settings", shortcut: "3"},
	{label: "Status", shortcut: "4"},
	{label: "Quit", shortcut: "5"},
}

// MenuActionMsg is sent when the user selects a menu entry.
// The root model acts on it (routes to the appropriate screen or quits).\
type MenuActionMsg struct {
	Index int // 0-based index of the selected item
}

// ----------------------------------------------------------------------------
// Cursor animation
// ----------------------------------------------------------------------------

// menuPulseTickMsg drives the cursor marker animation.
type menuPulseTickMsg struct{}

const menuPulseInterval = 400 * time.Millisecond

// pulseFrames cycles through arrow variants to create a subtle pulse.
var pulseFrames = []string{"▸ ", "▹ ", "▷ ", "▹ "}

func tickMenuPulse() tea.Cmd {
	return tea.Tick(menuPulseInterval, func(time.Time) tea.Msg {
		return menuPulseTickMsg{}
	})
}

// ----------------------------------------------------------------------------
// MainMenuModel
// ----------------------------------------------------------------------------

// MainMenuModel is the bubbletea model for the main navigation menu.
type MainMenuModel struct {
	cursor     int
	pulseFrame int // index into pulseFrames
	width      int
	height     int
}

// NewMainMenuModel creates a MainMenuModel with the given terminal dimensions.
func NewMainMenuModel(width, height int) MainMenuModel {
	return MainMenuModel{width: width, height: height}
}

// Init starts the cursor pulse animation.
func (m MainMenuModel) Init() tea.Cmd {
	return tickMenuPulse()
}

// Update handles navigation, selection keys, and the pulse tick.
func (m MainMenuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case menuPulseTickMsg:
		m.pulseFrame = (m.pulseFrame + 1) % len(pulseFrames)
		return m, tickMenuPulse()

	case tea.KeyMsg:
		switch msg.String() {
		// Vim-style and arrow navigation.
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(mainMenuItems)-1 {
				m.cursor++
			}

		// Number shortcuts.
		case "1", "2", "3", "4", "5":
			idx := int(msg.String()[0] - '1') // '1' → 0
			if idx < len(mainMenuItems) {
				m.cursor = idx
				return m, sendMenuAction(idx)
			}

		// Enter confirms selection.
		case "enter":
			return m, sendMenuAction(m.cursor)

		// q / Ctrl+C are handled by the root model via its global key filter.
		// ? is handled by root as well.
		}
	}
	return m, nil
}

// sendMenuAction builds the command that fires a MenuActionMsg.
func sendMenuAction(idx int) tea.Cmd {
	return func() tea.Msg { return MenuActionMsg{Index: idx} }
}

// View renders the main menu.
func (m MainMenuModel) View() string {
	if m.width < MinTermWidth {
		return NarrowTermMsg
	}

	// Current animated marker for the selected item.
	marker := pulseFrames[m.pulseFrame]
	if NoColor {
		// In NO_COLOR mode, use a static marker so there's no reliance on color.
		marker = "▸ "
	}

	var items string
	for i, item := range mainMenuItems {
		line := fmt.Sprintf("%s. %s", item.shortcut, item.label)
		if i == m.cursor {
			// Selected: animated marker + accent color (bold only in NO_COLOR).
			items += SelectedItemStyle().Render(marker+line) + "\n"
		} else {
			items += NormalItemStyle.Render("  " + line) + "\n"
		}
	}
	// Trim trailing newline for clean rendering.
	if len(items) > 0 {
		items = items[:len(items)-1]
	}

	boxWidth := m.width - 2
	if boxWidth < 10 {
		boxWidth = 10
	}

	box := renderBoxInner("Decoyd", items, boxWidth, ColorBorder)

	footer := HelpTextStyle.Render("↑/↓ navigate   enter select   ? help   q quit")

	return lipgloss.JoinVertical(lipgloss.Left, box, footer)
}
