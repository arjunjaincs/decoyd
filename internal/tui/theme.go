// Package tui contains the bubbletea TUI models and shared design system for Decoyd.
package tui

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ----------------------------------------------------------------------------
// NO_COLOR detection
// ----------------------------------------------------------------------------

// NoColor is true when the NO_COLOR environment variable is set to any
// non-empty value. All rendering code checks this flag instead of reading the
// env directly, so it is resolved exactly once at startup.
//
// See https://no-color.org/
var NoColor = os.Getenv("NO_COLOR") != ""

// HasUnicode is true when the running terminal supports Unicode / VT sequences.
// On Linux/macOS this is always true. On Windows it is false for plain cmd.exe
// (which uses CP437 and renders box-drawing chars as '█') and true for Windows
// Terminal, VSCode, PowerShell 7+, and any terminal with VT processing enabled.
//
// Resolved once at startup via hasVTSupport() (platform-specific in
// unicode_windows.go / unicode_notwindows.go).
var HasUnicode = hasVTSupport()

// ----------------------------------------------------------------------------
// Glyphs — terminal-conditional symbol set
// All rendered glyphs come from here; never use raw Unicode literals elsewhere.
// ----------------------------------------------------------------------------

// glyphs holds the full set of symbols used across the TUI, chosen at startup
// based on whether the terminal supports Unicode/VT sequences.
type glyphs struct {
	// Navigation
	NavUp   string // ↑ or ^
	NavDown string // ↓ or v
	Arrow   string // → or ->

	// Cursor / selection
	Cursor  string // ▸ or >

	// Status icons
	OK      string // ✓ or [ok]
	Fail    string // ✗ or [x]
	Warn    string // ⚠ or [!]
	Bullet  string // ● or *
	Empty   string // ○ or o
	Star    string // ★ or *

	// Text
	Ellipsis string // … or ...
	Dot      string // · or .

	// Box drawing (used in titledBorder top edge)
	Horiz string // ─ or -
}

// G is the global glyph set, resolved once at startup.
var G = func() glyphs {
	if HasUnicode {
		return glyphs{
			NavUp:   "\u2191", // ↑
			NavDown: "\u2193", // ↓
			Arrow:   "\u2192", // →
			Cursor:  "\u25b8", // ▸
			OK:      "\u2713", // ✓
			Fail:    "\u2717", // ✗
			Warn:    "\u26a0", // ⚠
			Bullet:  "\u25cf", // ●
			Empty:   "\u25cb", // ○
			Star:    "\u2605", // ★
			Ellipsis: "\u2026", // …
			Dot:     "\u00b7", // ·
			Horiz:   "\u2500", // ─
		}
	}
	return glyphs{
		NavUp:   "^",
		NavDown: "v",
		Arrow:   "->",
		Cursor:  ">",
		OK:      "[ok]",
		Fail:    "[x]",
		Warn:    "[!]",
		Bullet:  "*",
		Empty:   "o",
		Star:    "*",
		Ellipsis: "...",
		Dot:     ".",
		Horiz:   "-",
	}
}()

// ----------------------------------------------------------------------------
// Palette — named color constants
// All hex values are defined exactly once here. No other file uses raw hex.
// ----------------------------------------------------------------------------

const (
	// ColorBackground is the app background color.
	ColorBackground = lipgloss.Color("#0d1117")

	// ColorPrimary is the primary accent: selected items, success, borders, wordmark.
	ColorPrimary = lipgloss.Color("#3fb950")

	// ColorWarning is used for non-fatal warnings.
	ColorWarning = lipgloss.Color("#d29922")

	// ColorDanger is used for errors and live trigger alerts.
	ColorDanger = lipgloss.Color("#f85149")

	// ColorTextPrimary is the main body text color.
	ColorTextPrimary = lipgloss.Color("#c9d1d9")

	// ColorTextMuted is used for help footers, timestamps, and secondary detail.
	ColorTextMuted = lipgloss.Color("#8b949e")

	// ColorBorder is used for box borders and dividers.
	ColorBorder = lipgloss.Color("#30363d")
)

// ----------------------------------------------------------------------------
// Shared styles
// These are the canonical instances. Individual screens use these and may
// derive from them with .Copy() — they never hard-code hex values.
// ----------------------------------------------------------------------------

// BoxStyle is the base bordered box style used for every screen.
// Use RenderBox() rather than applying this directly.
// On terminals without Unicode/VT support (plain cmd.exe), NormalBorder is
// used instead of RoundedBorder to avoid CP437 box-drawing garbage.
var BoxStyle = func() lipgloss.Style {
	border := lipgloss.RoundedBorder()
	if !HasUnicode {
		border = lipgloss.NormalBorder()
	}
	return lipgloss.NewStyle().
		Border(border).
		BorderForeground(ColorBorder).
		Padding(1, 2)
}()

// TitleStyle renders the box title (placed in the top border line).
var TitleStyle = lipgloss.NewStyle().
	Foreground(ColorPrimary).
	Bold(true)

// SelectedItemStyle renders a selected list item in the primary accent color.
// When NoColor is active, the color is omitted and only the ▸ marker signals
// selection (set in the list render logic).
func SelectedItemStyle() lipgloss.Style {
	s := lipgloss.NewStyle().Bold(true)
	if !NoColor {
		s = s.Foreground(ColorPrimary)
	}
	return s
}

// NormalItemStyle renders an unselected list item.
var NormalItemStyle = lipgloss.NewStyle().
	Foreground(ColorTextPrimary)

// HelpTextStyle renders the persistent one-line help footer.
var HelpTextStyle = lipgloss.NewStyle().
	Foreground(ColorTextMuted)

// MutedStyle renders secondary detail text (timestamps, labels).
var MutedStyle = lipgloss.NewStyle().
	Foreground(ColorTextMuted)

// ErrorStyle renders error text.
var ErrorStyle = lipgloss.NewStyle().
	Foreground(ColorDanger).
	Bold(true)

// WarningStyle renders non-fatal warning text.
var WarningStyle = lipgloss.NewStyle().
	Foreground(ColorWarning).
	Bold(true)

// WordmarkStyle renders the D E C O Y D wordmark in the splash screen.
var WordmarkStyle = lipgloss.NewStyle().
	Foreground(ColorPrimary).
	Bold(true)

// ----------------------------------------------------------------------------
// RenderBox — the canonical way to draw a screen
// ----------------------------------------------------------------------------

// MinTermWidth is the minimum terminal width below which Decoyd shows a
// "please widen your terminal" message instead of rendering broken boxes.
const MinTermWidth = 60

// NarrowTermMsg is shown when the terminal is too narrow to render properly.
// Unicode arrows are used when the terminal supports them; plain ASCII otherwise.
var NarrowTermMsg = func() string {
	if hasVTSupport() {
		return "\u27f5 Please widen your terminal to at least 60 columns \u27f6"
	}
	return "<< Please widen your terminal to at least 60 columns >>"
}()

// RenderBox wraps content in the standard rounded-border box.
// title is placed in the top border (empty string = no title).
// width is the outer width of the box; pass the terminal width.
func RenderBox(title, content string, width int) string {
	if width < MinTermWidth {
		return lipgloss.NewStyle().
			Foreground(ColorWarning).
			Render(NarrowTermMsg)
	}

	// Cap to terminal width, subtract 2 for the border characters themselves.
	boxWidth := width - 2
	if boxWidth < 10 {
		boxWidth = 10
	}

	return renderBoxInner(title, content, boxWidth, ColorBorder)
}

// renderBoxInner renders a rounded-border box with an optional title embedded
// in the top edge. borderColor controls the border foreground color, allowing
// the help overlay to use the accent color.
//
// lipgloss v1.x has no BorderTitle API, so we build a custom BorderStyle whose
// Top string encodes the title between the corner characters.
func renderBoxInner(title, content string, boxWidth int, borderColor lipgloss.Color) string {
	// Inner content width = box width minus 2 border chars minus 4 padding chars.
	contentWidth := boxWidth - 2 - 4
	if contentWidth < 1 {
		contentWidth = 1
	}

	var border lipgloss.Border
	if title == "" {
		if HasUnicode {
			border = lipgloss.RoundedBorder()
		} else {
			border = lipgloss.NormalBorder()
		}
	} else {
		border = titledBorder(title, contentWidth)
	}

	return lipgloss.NewStyle().
		Border(border).
		BorderForeground(borderColor).
		Padding(1, 2).
		Width(contentWidth).
		Render(content)
}

// titledBorder produces a lipgloss.Border whose Top segment encodes title.
// The inner width (content area, no border chars) is needed to compute how
// many filler dashes surround the title text.
func titledBorder(title string, innerWidth int) lipgloss.Border {
	titleLabel := " " + title + " "
	titleLen := len([]rune(titleLabel))

	// Total top-border dashes available: innerWidth + padding (4) = inner+4.
	// We leave at least 2 dashes on the right so it looks balanced.
	totalDashes := innerWidth + 4 // matches the box outer width minus 2 corners
	rightDashes := totalDashes - titleLen
	if rightDashes < 2 {
		// Title too long — truncate it.
		max := totalDashes - 2
		if max < 1 {
			max = 1
		}
		titleLabel = Truncate(" "+title+" ", max)
		titleLen = len([]rune(titleLabel))
		rightDashes = totalDashes - titleLen
		if rightDashes < 0 {
			rightDashes = 0
		}
	}

	// Build the top edge using the appropriate horizontal character.
	top := titleLabel + strings.Repeat(G.Horiz, rightDashes)

	var b lipgloss.Border
	if HasUnicode {
		b = lipgloss.RoundedBorder()
	} else {
		b = lipgloss.NormalBorder()
	}
	b.Top = top
	return b
}

// titleBorderStr is kept for internal callers that need the spaced label text.
func titleBorderStr(title string) string {
	return " " + title + " "
}

// ----------------------------------------------------------------------------
// Helper utilities
// ----------------------------------------------------------------------------

// Truncate clips s to maxLen runes and appends G.Ellipsis if truncated.
func Truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	suffix := G.Ellipsis
	cutAt := maxLen - len([]rune(suffix))
	if cutAt < 0 {
		cutAt = 0
	}
	return string(runes[:cutAt]) + suffix
}

// PadCenter centers s within a field of width w, padding with spaces.
func PadCenter(s string, w int) string {
	sLen := len([]rune(s))
	if sLen >= w {
		return s
	}
	total := w - sLen
	left := total / 2
	right := total - left
	return strings.Repeat(" ", left) + s + strings.Repeat(" ", right)
}
