package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Version is the application version string, embedded at build time via ldflags.
// Falls back to "dev" if not set.
var Version = "dev"

// SplashDoneMsg is sent by SplashModel when the user presses any key,
// signalling the root model to transition to the main menu.
type SplashDoneMsg struct{}

// ----------------------------------------------------------------------------
// Animation tick messages (unexported — internal to the splash screen)
// ----------------------------------------------------------------------------

type splashTypeTickMsg struct{} // advance typewriter one rune
type splashBlinkTickMsg struct{} // toggle prompt visibility

const (
	typewriterDelay = 90 * time.Millisecond  // time between each revealed letter
	blinkInterval   = 550 * time.Millisecond // prompt blink rate
)

// wordmarkRunes is the full wordmark broken into individual runes so we can
// reveal them one by one.
var wordmarkRunes = []rune("D E C O Y D")

// ----------------------------------------------------------------------------
// SplashModel
// ----------------------------------------------------------------------------

// SplashModel is the bubbletea model for the first-run splash screen.
type SplashModel struct {
	width     int
	height    int
	letterIdx int  // number of wordmark runes currently visible
	blinkOn   bool // whether the prompt line is currently visible
	ready     bool // true once the typewriter has finished
}

// NewSplashModel creates a SplashModel with the given terminal dimensions.
func NewSplashModel(width, height int) SplashModel {
	return SplashModel{
		width:   width,
		height:  height,
		blinkOn: true,
	}
}

// Init kicks off the typewriter animation.
func (m SplashModel) Init() tea.Cmd {
	return tickType()
}

func tickType() tea.Cmd {
	return tea.Tick(typewriterDelay, func(time.Time) tea.Msg {
		return splashTypeTickMsg{}
	})
}

func tickBlink() tea.Cmd {
	return tea.Tick(blinkInterval, func(time.Time) tea.Msg {
		return splashBlinkTickMsg{}
	})
}

// Update handles animation ticks and any keypress.
func (m SplashModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case splashTypeTickMsg:
		if m.letterIdx < len(wordmarkRunes) {
			m.letterIdx++
		}
		if m.letterIdx < len(wordmarkRunes) {
			// More letters to reveal — keep going.
			return m, tickType()
		}
		// Typewriter finished — start blinking the prompt.
		m.ready = true
		return m, tickBlink()

	case splashBlinkTickMsg:
		m.blinkOn = !m.blinkOn
		return m, tickBlink()

	case tea.KeyMsg:
		// Any key skips past the splash immediately.
		_ = msg
		return m, func() tea.Msg { return SplashDoneMsg{} }
	}
	return m, nil
}

// View renders the splash screen with its animated wordmark and blinking prompt.
func (m SplashModel) View() string {
	if m.width < MinTermWidth {
		return NarrowTermMsg
	}

	// ── Wordmark — partially or fully revealed ────────────────────────────────
	revealed := string(wordmarkRunes[:m.letterIdx])
	// Pad to the full width of the wordmark so the box doesn't jump in size.
	// We use spaces so the layout stays stable during the reveal.
	padded := revealed + strings.Repeat(" ", len(wordmarkRunes)-m.letterIdx)
	wordmark := WordmarkStyle.Render(padded)

	// ── Subtitle — appears only after wordmark is complete ────────────────────
	var subtitle string
	if m.ready {
		subtitle = MutedStyle.Render(fmt.Sprintf("self-hosted deception  ·  v%s", Version))
	} else {
		// Reserve the line so the box height stays constant.
		subtitle = MutedStyle.Render(strings.Repeat(" ", 36))
	}

	// ── Prompt — blinks once typewriter is done ───────────────────────────────
	var prompt string
	switch {
	case m.ready && m.blinkOn:
		prompt = HelpTextStyle.Render("press any key to continue")
	default:
		// Invisible placeholder preserves height during blink-off phase.
		prompt = HelpTextStyle.Render(strings.Repeat(" ", 25))
	}

	// ── Compose inner content ─────────────────────────────────────────────────
	boxWidth := m.width - 4 // border chars + outer margin
	if boxWidth < 10 {
		boxWidth = 10
	}

	inner := lipgloss.JoinVertical(lipgloss.Center,
		"",
		wordmark,
		subtitle,
		"",
		prompt,
		"",
	)

	content := lipgloss.NewStyle().
		Width(boxWidth).
		Align(lipgloss.Center).
		Render(inner)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(ColorBorder).
		Padding(1, 2).
		Width(boxWidth).
		Render(content)

	// ── Vertically center in terminal ─────────────────────────────────────────
	boxLines := lipgloss.Height(box)
	topPad := (m.height - boxLines) / 2
	if topPad < 0 {
		topPad = 0
	}

	var out string
	for i := 0; i < topPad; i++ {
		out += "\n"
	}
	return out + box
}
