package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/arjunjaincs/decoyd/internal/alert"
	"github.com/arjunjaincs/decoyd/internal/store"
	"github.com/arjunjaincs/decoyd/internal/tokens"
	"github.com/arjunjaincs/decoyd/internal/watch"
)

// ----------------------------------------------------------------------------
// Messages
// ----------------------------------------------------------------------------

// TokenListDoneMsg is sent when the user exits the token list screen.
type TokenListDoneMsg struct{}

// ----------------------------------------------------------------------------
// tokenListState
// ----------------------------------------------------------------------------

type tokenListState int

const (
	tokenListStateBrowse   tokenListState = iota // browsing the list
	tokenListStateConfDel                        // confirming a delete
	tokenListStateEdit                           // editing the Notes field
	tokenListStateAssign                         // picking an alert channel
)

// ----------------------------------------------------------------------------
// TokenListModel
// ----------------------------------------------------------------------------

// TokenListModel is the bubbletea model for the "Deployed Tokens" list screen.
type TokenListModel struct {
	width  int
	height int
	st      *store.Store
	dataDir string // for loading alert config
	all    []tokens.Token
	cursor int
	state  tokenListState
	notice string // brief status line shown below the table
	// Edit state — holds the in-progress rune buffer for the Notes field.
	editBuf []rune
	editPos int
	// Assign state — channel picker.
	alertCfg    alert.AlertConfig // refreshed when entering assign state
	assignCursor int              // cursor within channel picker
}

// NewTokenListModel creates a fresh model, loading all tokens from the store.
func NewTokenListModel(width, height int, st *store.Store, dataDir string) TokenListModel {
	m := TokenListModel{width: width, height: height, st: st, dataDir: dataDir}
	m.reload()
	return m
}

func (m *TokenListModel) reload() {
	if m.st == nil {
		return
	}
	ts, _ := m.st.ListTokens()
	m.all = ts
	if m.cursor >= len(m.all) && len(m.all) > 0 {
		m.cursor = len(m.all) - 1
	}
}

// Init satisfies tea.Model.
func (m TokenListModel) Init() tea.Cmd { return nil }

// ----------------------------------------------------------------------------
// Update
// ----------------------------------------------------------------------------

func (m TokenListModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		switch m.state {
		case tokenListStateBrowse:
			return m.updateBrowse(msg)
		case tokenListStateConfDel:
			return m.updateConfirmDelete(msg)
		case tokenListStateEdit:
			return m.updateEdit(msg)
		case tokenListStateAssign:
			return m.updateAssign(msg)
		}
	}
	return m, nil
}

func (m TokenListModel) updateBrowse(msg tea.KeyMsg) (TokenListModel, tea.Cmd) {
	m.notice = ""
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.all)-1 {
			m.cursor++
		}
	case "d":
		if len(m.all) > 0 {
			m.state = tokenListStateConfDel
		}
	case "e":
		if len(m.all) > 0 {
			// Pre-populate edit buffer with the current Notes value.
			m.editBuf = []rune(m.all[m.cursor].Notes)
			m.editPos = len(m.editBuf)
			m.state = tokenListStateEdit
		}
	case "a":
		if len(m.all) > 0 {
			// Refresh alert config before opening picker.
			if m.dataDir != "" {
				if cfg, err := alert.Load(m.dataDir); err == nil {
					m.alertCfg = cfg
				}
			}
			m.assignCursor = 0
			m.state = tokenListStateAssign
		}
	case "esc":
		return m, func() tea.Msg { return TokenListDoneMsg{} }
	}
	return m, nil
}


func (m TokenListModel) updateConfirmDelete(msg tea.KeyMsg) (TokenListModel, tea.Cmd) {
	switch msg.String() {
	case "y", "enter":
		if len(m.all) == 0 {
			m.state = tokenListStateBrowse
			return m, nil
		}
		tok := m.all[m.cursor]
		var err error
		if m.st != nil {
			err = m.st.DeleteToken(tok.ID)
		}
		if err != nil {
			m.notice = ErrorStyle.Render("Delete failed: " + err.Error())
		} else {
			m.notice = lipgloss.NewStyle().Foreground(ColorPrimary).Render(
				fmt.Sprintf("Deleted token %s (%s)", tok.ID, tok.Type))
			// Update deployed_tokens.json so any running headless watcher
			// stops watching the deleted token's path immediately.
			_ = watch.ReconcileSnapshot(m.st, m.dataDir)
		}
		m.reload()
		m.state = tokenListStateBrowse
	case "n", "esc":
		m.state = tokenListStateBrowse
	}
	return m, nil
}

// updateEdit handles key input while editing the Notes field.
// IMPORTANT: only non-printable keys (enter, esc, backspace, ctrl+*, arrows)
// are used for control — no single-letter shortcuts — so paste works correctly.
func (m TokenListModel) updateEdit(msg tea.KeyMsg) (TokenListModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.editBuf = nil
		m.editPos = 0
		m.state = tokenListStateBrowse
	case "enter":
		if len(m.all) == 0 {
			m.state = tokenListStateBrowse
			return m, nil
		}
		tok := m.all[m.cursor]
		tok.Notes = strings.TrimSpace(string(m.editBuf))
		var err error
		if m.st != nil {
			err = m.st.UpdateToken(tok)
		}
		m.editBuf = nil
		m.editPos = 0
		if err != nil {
			m.notice = ErrorStyle.Render("Edit failed: " + err.Error())
		} else {
			m.notice = lipgloss.NewStyle().Foreground(ColorPrimary).Render(
				fmt.Sprintf("Notes updated for %s", tok.ID))
		}
		m.reload()
		m.state = tokenListStateBrowse
	case "backspace", "ctrl+h":
		if m.editPos > 0 {
			m.editBuf = append(m.editBuf[:m.editPos-1], m.editBuf[m.editPos:]...)
			m.editPos--
		}
	case "delete":
		if m.editPos < len(m.editBuf) {
			m.editBuf = append(m.editBuf[:m.editPos], m.editBuf[m.editPos+1:]...)
		}
	case "left", "ctrl+b":
		if m.editPos > 0 {
			m.editPos--
		}
	case "right", "ctrl+f":
		if m.editPos < len(m.editBuf) {
			m.editPos++
		}
	case "ctrl+a", "home":
		m.editPos = 0
	case "ctrl+e", "end":
		m.editPos = len(m.editBuf)
	default:
		if len(msg.Runes) > 0 {
			r := msg.Runes
			nb := make([]rune, 0, len(m.editBuf)+len(r))
			nb = append(nb, m.editBuf[:m.editPos]...)
			nb = append(nb, r...)
			nb = append(nb, m.editBuf[m.editPos:]...)
			m.editBuf = nb
			m.editPos += len(r)
		}
	}
	return m, nil
}

// ----------------------------------------------------------------------------
// View
// ----------------------------------------------------------------------------

func (m TokenListModel) View() string {
	if m.width < MinTermWidth {
		return NarrowTermMsg
	}

	boxW := ScreenBoxWidth(m.width, 92)

	var content string
	if m.state == tokenListStateConfDel && len(m.all) > 0 {
		content = m.viewConfirmDelete(boxW)
	} else if m.state == tokenListStateEdit && len(m.all) > 0 {
		content = m.viewEdit(boxW)
	} else if m.state == tokenListStateAssign && len(m.all) > 0 {
		content = m.viewAssign(boxW)
	} else {
		content = m.viewTable(boxW)
	}
	return PlaceScreen(m.width, m.height, content)
}

func (m TokenListModel) viewTable(boxW int) string {
	if len(m.all) == 0 {
		content := MutedStyle.Render("  No tokens yet. Generate some first (option 1).")
		box := renderBoxInner("Deployed Tokens", content, boxW, ColorBorder)
		footer := HelpTextStyle.Render("esc back   ? help")
		return lipgloss.JoinVertical(lipgloss.Left, box, footer)
	}

	// Column widths.
	const (
		typeW      = 18
		fileW      = 20
		locationW  = 28
		triggeredW = 12
	)

	// Header row.
	header := MutedStyle.Render(
		fmt.Sprintf("  %-*s  %-*s  %-*s  %s",
			typeW, "Type",
			fileW, "File",
			locationW, "Location",
			"Triggered",
		),
	)
	divider := MutedStyle.Render("  " + strings.Repeat("─", boxW-6))

	var rows strings.Builder
	rows.WriteString(header + "\n")
	rows.WriteString(divider + "\n")

	for i, tok := range m.all {
		isCursor := i == m.cursor

		typeStr := truncate(tok.Type, typeW)
		fileStr := truncate(tok.Filename, fileW)

		loc := tok.DeployedPath
		if loc == "" {
			loc = MutedStyle.Render("(not deployed)")
		} else {
			loc = truncate(loc, locationW)
		}

		triggered := "no"
		if tok.Triggered {
			triggered = WarningStyle.Render("yes " + G.Warn)
		}

		marker := "  "
		if isCursor {
			marker = G.Cursor + " "
		}

		line := fmt.Sprintf("%s%-*s  %-*s  %-*s  %s",
			marker,
			typeW, typeStr,
			fileW, fileStr,
			locationW, loc,
			triggered,
		)

		if isCursor {
			rows.WriteString(SelectedItemStyle().Render(line) + "\n")
		} else {
			rows.WriteString(NormalItemStyle.Render(line) + "\n")
		}
	}

	var sb strings.Builder
	sb.WriteString(strings.TrimRight(rows.String(), "\n"))

	if m.notice != "" {
		sb.WriteString("\n\n" + m.notice)
	}

	box := renderBoxInner("Deployed Tokens", sb.String(), boxW, ColorBorder)
	footer := HelpTextStyle.Render(G.NavUp + "/" + G.NavDown + " browse   d delete   e edit notes   a assign channel   esc back")
	return lipgloss.JoinVertical(lipgloss.Left, box, footer)
}

func (m TokenListModel) viewConfirmDelete(boxW int) string {
	tok := m.all[m.cursor]
	var sb strings.Builder
	sb.WriteString(WarningStyle.Render("  Delete this token record?") + "\n\n")
	sb.WriteString(MutedStyle.Render("  ID:   ") + NormalItemStyle.Render(tok.ID) + "\n")
	sb.WriteString(MutedStyle.Render("  Type: ") + NormalItemStyle.Render(tok.Type) + "\n")
	if tok.DeployedPath != "" {
		sb.WriteString(MutedStyle.Render("  Path: ") + NormalItemStyle.Render(tok.DeployedPath) + "\n")
		sb.WriteString("\n" + MutedStyle.Render("  Note: the deployed file is NOT removed from disk.") + "\n")
	}

	content := strings.TrimRight(sb.String(), "\n")
	box := renderBoxInner("Delete Token", content, boxW, ColorDanger)
	footer := HelpTextStyle.Render("y/enter confirm   n/esc cancel")
	return lipgloss.JoinVertical(lipgloss.Left, box, footer)
}

func (m TokenListModel) viewEdit(boxW int) string {
	tok := m.all[m.cursor]

	// Render edit buffer with block cursor at insertion point.
	var editDisplay string
	if len(m.editBuf) == 0 {
		editDisplay = lipgloss.NewStyle().Foreground(ColorTextMuted).Render("│")
	} else {
		before := string(m.editBuf[:m.editPos])
		cur := lipgloss.NewStyle().Background(ColorPrimary).Foreground(ColorBackground).Render(" ")
		after := ""
		if m.editPos < len(m.editBuf) {
			after = string(m.editBuf[m.editPos:])
		}
		editDisplay = before + cur + after
	}

	var sb strings.Builder
	sb.WriteString(MutedStyle.Render("  ID:    ") + NormalItemStyle.Render(tok.ID) + "\n")
	sb.WriteString(MutedStyle.Render("  Type:  ") + NormalItemStyle.Render(tok.Type) + "\n\n")
	sb.WriteString(MutedStyle.Render("  Notes: ") + SelectedItemStyle().Render(editDisplay) + "\n")

	content := strings.TrimRight(sb.String(), "\n")
	box := renderBoxInner("Edit Token Notes", content, boxW, ColorPrimary)
	footer := HelpTextStyle.Render("enter save   esc cancel")
	return lipgloss.JoinVertical(lipgloss.Left, box, footer)
}

// truncate shortens s to n chars, appending … if clipped.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	suffix := G.Ellipsis
	cutAt := n - len([]rune(suffix))
	if cutAt < 0 {
		cutAt = 0
	}
	return string(runes[:cutAt]) + suffix
}

// ----------------------------------------------------------------------------
// Assign channel state
// ----------------------------------------------------------------------------

// assignOptions returns the channel picker options for the current token.
// Index 0 is always "Remove assignment (use default)".
// Remaining entries are from alertCfg.Channels.
func (m TokenListModel) assignOptions() []string {
	opts := []string{"Remove assignment (use default)"}
	for _, ch := range m.alertCfg.Channels {
		label := ch.Label
		if label == "" {
			label = ch.Type
		}
		suffix := ""
		if ch.ID == m.alertCfg.DefaultID {
			suffix += " " + G.Star
		}
		if len(m.all) > 0 && ch.ID == m.all[m.cursor].AlertChannelID {
			suffix += " " + G.OK
		}
		opts = append(opts, label+suffix)
	}
	return opts
}

func (m TokenListModel) updateAssign(msg tea.KeyMsg) (TokenListModel, tea.Cmd) {
	opts := m.assignOptions()
	maxIdx := len(opts) - 1
	switch msg.String() {
	case "up", "k":
		if m.assignCursor > 0 {
			m.assignCursor--
		}
	case "down", "j":
		if m.assignCursor < maxIdx {
			m.assignCursor++
		}
	case "enter":
		if len(m.all) == 0 {
			m.state = tokenListStateBrowse
			return m, nil
		}
		tok := m.all[m.cursor]
		if m.assignCursor == 0 {
			// Remove assignment.
			tok.AlertChannelID = ""
		} else {
			chIdx := m.assignCursor - 1
			if chIdx < len(m.alertCfg.Channels) {
				tok.AlertChannelID = m.alertCfg.Channels[chIdx].ID
			}
		}
		var err error
		if m.st != nil {
			err = m.st.UpdateToken(tok)
		}
		if err != nil {
			m.notice = ErrorStyle.Render("Assign failed: " + err.Error())
		} else {
			m.notice = lipgloss.NewStyle().Foreground(ColorPrimary).Render(
				fmt.Sprintf("Channel assignment updated for %s", tok.ID))
		}
		m.reload()
		m.state = tokenListStateBrowse
	case "esc":
		m.state = tokenListStateBrowse
	}
	return m, nil
}

func (m TokenListModel) viewAssign(boxW int) string {
	if len(m.all) == 0 {
		m.state = tokenListStateBrowse
		return m.viewTable(boxW)
	}
	tok := m.all[m.cursor]
	opts := m.assignOptions()

	var sb strings.Builder
	sb.WriteString(MutedStyle.Render(fmt.Sprintf("  Token: %s (%s)", tok.ID, tok.Type)) + "\n\n")

	if len(m.alertCfg.Channels) == 0 {
		sb.WriteString(MutedStyle.Render("  No alert channels configured yet.\n"))
		sb.WriteString(MutedStyle.Render("  Configure channels in Alert Settings first."))
		box := renderBoxInner("Assign Alert Channel", sb.String(), boxW, ColorBorder)
		footer := HelpTextStyle.Render("esc back")
		return lipgloss.JoinVertical(lipgloss.Left, box, footer)
	}

	for i, opt := range opts {
		marker := "  "
		if i == m.assignCursor {
			marker = G.Cursor + " "
		}
		line := marker + opt
		if i == m.assignCursor {
			sb.WriteString(SelectedItemStyle().Render(line) + "\n")
		} else {
			sb.WriteString(NormalItemStyle.Render(line) + "\n")
		}
	}

	content := strings.TrimRight(sb.String(), "\n")
	box := renderBoxInner("Assign Alert Channel", content, boxW, ColorPrimary)
	footer := HelpTextStyle.Render(G.NavUp + "/" + G.NavDown + " choose   enter confirm   esc cancel   " + G.Star + " = default   " + G.OK + " = current")
	return lipgloss.JoinVertical(lipgloss.Left, box, footer)
}

