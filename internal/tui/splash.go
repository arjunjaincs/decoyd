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
// Animation phases
// ----------------------------------------------------------------------------

// splashPhase represents the current stage of the splash animation.
type splashPhase uint8

const (
	// splashPhaseWordmark: D E C O Y D is being typed letter by letter.
	splashPhaseWordmark splashPhase = iota
	// splashPhasePause: wordmark complete — brief hold before tagline starts.
	// The separator line fades in during this phase.
	splashPhasePause
	// splashPhaseTagline: tagline is being typed letter by letter.
	splashPhaseTagline
	// splashPhasePrompt: everything revealed — prompt is blinking.
	splashPhasePrompt
)

// ----------------------------------------------------------------------------
// Tick messages (unexported — internal to the splash screen only)
// ----------------------------------------------------------------------------

type splashWordTickMsg  struct{} // advance wordmark typewriter one rune
type splashPauseTickMsg struct{} // fire after the post-wordmark pause
type splashTagTickMsg   struct{} // advance tagline typewriter one rune
type splashBlinkTickMsg struct{} // toggle prompt blink state

// ----------------------------------------------------------------------------
// Timing
// ----------------------------------------------------------------------------

const (
	splashWordDelay = 95 * time.Millisecond  // inter-rune delay for wordmark
	splashPauseDur  = 500 * time.Millisecond // pause after wordmark completes
	splashTagDelay  = 26 * time.Millisecond  // inter-rune delay for tagline
	splashBlinkDur  = 560 * time.Millisecond // prompt blink period
)

// ----------------------------------------------------------------------------
// Content
// ----------------------------------------------------------------------------

var (
	splashWordmarkRunes = []rune("D E C O Y D")
	splashTaglineRunes  = []rune("canary token generator")
)

// splashBoxMaxInner is the maximum content-area width of the splash box.
// Keeping this fixed gives the splash a premium, intentional appearance
// rather than stretching across the full terminal.
const splashBoxMaxInner = 52

// ----------------------------------------------------------------------------
// SplashModel
// ----------------------------------------------------------------------------

// SplashModel is the bubbletea model for the splash/intro screen.
type SplashModel struct {
	width   int
	height  int
	phase   splashPhase
	wordIdx int  // number of wordmark runes currently visible
	tagIdx  int  // number of tagline runes currently visible
	blinkOn bool // current blink state of the prompt
}

// NewSplashModel creates a SplashModel with the given terminal dimensions.
func NewSplashModel(width, height int) SplashModel {
	return SplashModel{width: width, height: height, blinkOn: true}
}

// Init kicks off the wordmark typewriter animation.
func (m SplashModel) Init() tea.Cmd {
	return tea.Tick(splashWordDelay, func(time.Time) tea.Msg { return splashWordTickMsg{} })
}

// Update handles animation ticks and any keypress.
func (m SplashModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	// ── Wordmark typewriter ────────────────────────────────────────────────
	case splashWordTickMsg:
		if m.wordIdx < len(splashWordmarkRunes) {
			m.wordIdx++
		}
		if m.wordIdx < len(splashWordmarkRunes) {
			// More letters to reveal — keep going.
			return m, tea.Tick(splashWordDelay, func(time.Time) tea.Msg { return splashWordTickMsg{} })
		}
		// Wordmark complete → enter pause phase so it can breathe.
		m.phase = splashPhasePause
		return m, tea.Tick(splashPauseDur, func(time.Time) tea.Msg { return splashPauseTickMsg{} })

	// ── Post-wordmark pause ────────────────────────────────────────────────
	case splashPauseTickMsg:
		m.phase = splashPhaseTagline
		return m, tea.Tick(splashTagDelay, func(time.Time) tea.Msg { return splashTagTickMsg{} })

	// ── Tagline typewriter ─────────────────────────────────────────────────
	case splashTagTickMsg:
		if m.tagIdx < len(splashTaglineRunes) {
			m.tagIdx++
		}
		if m.tagIdx < len(splashTaglineRunes) {
			return m, tea.Tick(splashTagDelay, func(time.Time) tea.Msg { return splashTagTickMsg{} })
		}
		// Tagline complete → show prompt and start blinking.
		m.phase = splashPhasePrompt
		m.blinkOn = true
		return m, tea.Tick(splashBlinkDur, func(time.Time) tea.Msg { return splashBlinkTickMsg{} })

	// ── Prompt blink ──────────────────────────────────────────────────────
	case splashBlinkTickMsg:
		if m.phase == splashPhasePrompt {
			m.blinkOn = !m.blinkOn
			return m, tea.Tick(splashBlinkDur, func(time.Time) tea.Msg { return splashBlinkTickMsg{} })
		}

	// ── Any key skips to the menu immediately ─────────────────────────────
	case tea.KeyMsg:
		_ = msg
		return m, func() tea.Msg { return SplashDoneMsg{} }
	}
	return m, nil
}

// ----------------------------------------------------------------------------
// View
// ----------------------------------------------------------------------------

// View renders the splash screen. The box is always the same height across all
// phases — content rows are reserved from the start (blank or dimmed) so the
// box never jumps. lipgloss.Place() is used for true, stable centering.
func (m SplashModel) View() string {
	if m.width < MinTermWidth {
		return NarrowTermMsg
	}

	// ── Box inner content width ────────────────────────────────────────────
	// Responsive up to splashBoxMaxInner, leaving at least 8 chars of margin
	// on each side. This gives a contained, focused appearance.
	inner := m.width - 16
	if inner > splashBoxMaxInner {
		inner = splashBoxMaxInner
	}
	if inner < 28 {
		inner = 28
	}

	// center renders s centered inside a fixed-width field.
	center := func(s string) string {
		return lipgloss.NewStyle().
			Width(inner).
			Align(lipgloss.Center).
			Render(s)
	}

	// ── Row 1: Wordmark ───────────────────────────────────────────────────
	// Pad the unrevealed tail with spaces so the field width stays constant.
	wm := string(splashWordmarkRunes[:m.wordIdx]) +
		strings.Repeat(" ", len(splashWordmarkRunes)-m.wordIdx)
	wordmarkRow := center(WordmarkStyle.Render(wm))

	// ── Row 2: Separator ─────────────────────────────────────────────────
	// Invisible (spaces) during wordmark phase so height doesn't change;
	// appears as a dim line from pause phase onward.
	var separatorRow string
	if m.phase >= splashPhasePause {
		sepLen := inner - 8
		if sepLen < 4 {
			sepLen = 4
		}
		sep := strings.Repeat(G.Horiz, sepLen)
		separatorRow = center(
			lipgloss.NewStyle().Foreground(ColorBorder).Render(sep),
		)
	} else {
		separatorRow = center(" ")
	}

	// ── Row 3: Tagline ────────────────────────────────────────────────────
	// Reserved blank until tagline phase, then typed letter by letter.
	var taglineRow string
	switch {
	case m.phase == splashPhaseTagline:
		tl := string(splashTaglineRunes[:m.tagIdx]) +
			strings.Repeat(" ", len(splashTaglineRunes)-m.tagIdx)
		taglineRow = center(MutedStyle.Render(tl))
	case m.phase >= splashPhasePrompt:
		taglineRow = center(MutedStyle.Render(string(splashTaglineRunes)))
	default:
		// Reserve the line height without showing anything.
		taglineRow = center(strings.Repeat(" ", len(splashTaglineRunes)))
	}

	// ── Row 4: Version ───────────────────────────────────────────────────
	// Appears (without animation) once tagline is complete.
	var versionRow string
	if m.phase >= splashPhasePrompt {
		versionRow = center(
			lipgloss.NewStyle().Foreground(ColorTextMuted).
				Render(fmt.Sprintf("v%s", Version)),
		)
	} else {
		versionRow = center(" ")
	}

	// ── Row 5: Prompt ─────────────────────────────────────────────────────
	// Reserved as blank until prompt phase; blinks once active.
	const promptText = "press any key to continue"
	var promptRow string
	if m.phase == splashPhasePrompt && m.blinkOn {
		promptRow = center(
			lipgloss.NewStyle().Foreground(ColorTextMuted).Italic(true).
				Render(promptText),
		)
	} else {
		promptRow = center(strings.Repeat(" ", len(promptText)))
	}

	// ── Compose ───────────────────────────────────────────────────────────
	content := lipgloss.JoinVertical(lipgloss.Left,
		"",
		wordmarkRow,
		separatorRow,
		taglineRow,
		versionRow,
		"",
		promptRow,
		"",
	)

	// Box border — rounded when VT is available, ASCII otherwise.
	border := lipgloss.RoundedBorder()
	if !HasUnicode {
		border = lipgloss.NormalBorder()
	}

	box := lipgloss.NewStyle().
		Border(border).
		BorderForeground(ColorBorder).
		Padding(1, 4).
		Render(content)

	// ── True centering with lipgloss.Place ────────────────────────────────
	// Place handles everything correctly regardless of m.height. When called
	// before WindowSizeMsg (width/height == 0) fall back to bare render so
	// we at least show something — next re-render after WindowSizeMsg fixes it.
	if m.width > 0 && m.height > 0 {
		return lipgloss.Place(
			m.width, m.height,
			lipgloss.Center, lipgloss.Center,
			box,
		)
	}
	return box
}
