package tui

import (
	"fmt"
	"strings"
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
	{label: "Generate a decoy",      shortcut: "1"},
	{label: "Deploy existing decoys", shortcut: "2"},
	{label: "Alert settings",        shortcut: "3"},
	{label: "Status",                shortcut: "4"},
	{label: "Quit",                  shortcut: "5"},
}

// menuBoxMaxInner is the maximum content-area width of the menu card.
// Keeping this fixed gives the menu a contained, premium card appearance
// rather than stretching to fill the terminal.
const menuBoxMaxInner = 46

// MenuActionMsg is sent when the user selects a menu entry.
// The root model acts on it (routes to the appropriate screen or quits).
type MenuActionMsg struct {
	Index int // 0-based index of the selected item
}

// ----------------------------------------------------------------------------
// Cursor animation
// ----------------------------------------------------------------------------

// menuPulseTickMsg drives the cursor marker animation.
type menuPulseTickMsg struct{}

const menuPulseInterval = 380 * time.Millisecond

// pulseFrames cycles through arrow variants to create a subtle pulse.
// Unicode triangles are used when the terminal supports them (Windows Terminal,
// Linux, macOS). Plain cmd.exe (no VT) gets ASCII '>' frames instead.
var pulseFrames = func() []string {
	if HasUnicode {
		return []string{"\u25b8 ", "\u25b9 ", "\u25b7 ", "\u25b9 "}
	}
	return []string{"> ", "> ", "> ", "> "}
}()

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

// ----------------------------------------------------------------------------
// View
// ----------------------------------------------------------------------------

// View renders the main menu as a centered card with a wordmark header.
// lipgloss.Place() is used for true, stable centering regardless of when
// WindowSizeMsg fires.
func (m MainMenuModel) View() string {
	if m.width < MinTermWidth {
		return NarrowTermMsg
	}

	// ── Box inner content-area width ──────────────────────────────────────
	// Responsive, but capped so the menu reads as a focused card rather than
	// a banner stretched across the terminal.
	inner := m.width - 24
	if inner > menuBoxMaxInner {
		inner = menuBoxMaxInner
	}
	if inner < 28 {
		inner = 28
	}

	// center renders s centered within the inner content area.
	center := func(s string) string {
		return lipgloss.NewStyle().
			Width(inner).
			Align(lipgloss.Center).
			Render(s)
	}

	// ── Header ────────────────────────────────────────────────────────────
	wordmarkRow := center(WordmarkStyle.Render("D E C O Y D"))
	taglineRow := center(
		lipgloss.NewStyle().
			Foreground(ColorTextMuted).
			Render("canary token generator"),
	)

	// ── Separator ─────────────────────────────────────────────────────────
	sepLen := inner - 4
	if sepLen < 4 {
		sepLen = 4
	}
	// In ASCII mode G.Horiz = "-", which is identical to NormalBorder's top
	// edge character. Using "~" instead avoids the "double line" visual where
	// the box border and the internal separator look like one thick bar.
	sepChar := G.Horiz
	if !HasUnicode {
		sepChar = "~"
	}
	separatorRow := center(
		lipgloss.NewStyle().Foreground(ColorBorder).
			Render(strings.Repeat(sepChar, sepLen)),
	)

	// ── Cursor marker ─────────────────────────────────────────────────────
	// Animated in normal mode; static in NO_COLOR mode.
	marker := pulseFrames[m.pulseFrame]
	if NoColor {
		marker = pulseFrames[0] // first frame = "▸ " (VT) or "> " (ASCII)
	}

	// ── Menu items ────────────────────────────────────────────────────────
	// Items are left-aligned within the box. Each row is padded to inner
	// width so the box never shifts on cursor movement.
	itemStyle := lipgloss.NewStyle().Width(inner)

	var itemRows []string
	for i, item := range mainMenuItems {
		line := fmt.Sprintf("%s%s.  %s", func() string {
			if i == m.cursor {
				return marker
			}
			return "  "
		}(), item.shortcut, item.label)

		var rendered string
		if i == m.cursor {
			rendered = itemStyle.Render(SelectedItemStyle().Render(line))
		} else {
			rendered = itemStyle.Render(NormalItemStyle.Render(line))
		}
		itemRows = append(itemRows, rendered)
	}

	// ── Compose box content ───────────────────────────────────────────────
	rows := []string{
		"",
		wordmarkRow,
		taglineRow,
		"",
		separatorRow,
		"",
	}
	rows = append(rows, itemRows...)
	rows = append(rows, "")

	content := lipgloss.JoinVertical(lipgloss.Left, rows...)

	// ── Box ───────────────────────────────────────────────────────────────
	border := lipgloss.RoundedBorder()
	if !HasUnicode {
		border = lipgloss.NormalBorder()
	}

	box := lipgloss.NewStyle().
		Border(border).
		BorderForeground(ColorBorder).
		Padding(0, 3).
		Width(inner).
		Render(content)

	// ── Footer (hint bar) ────────────────────────────────────────────────
	navHint := G.NavUp + "/" + G.NavDown + " navigate   enter select   ? help   q quit"
	footer := HelpTextStyle.Render(navHint)

	// ── Center box + footer as one unit ──────────────────────────────────
	// JoinVertical(Center) aligns the footer under the center of the box.
	combined := lipgloss.JoinVertical(lipgloss.Center, box, "", footer)

	if m.width > 0 && m.height > 0 {
		return lipgloss.Place(
			m.width, m.height,
			lipgloss.Center, lipgloss.Center,
			combined,
		)
	}
	return combined
}
