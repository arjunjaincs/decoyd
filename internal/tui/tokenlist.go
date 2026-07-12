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
	tokenListStateBrowse  tokenListState = iota // browsing the list
	tokenListStateConfDel                       // confirming a delete
	tokenListStateEdit                          // editing the Notes field of the selected token
	tokenListStateAssign                        // picking an alert channel to assign
)

// ----------------------------------------------------------------------------
// TokenListModel
// ----------------------------------------------------------------------------

// TokenListModel is the bubbletea model for the "Deployed Tokens" list screen.
type TokenListModel struct {
	width  int
	height int
	st     *store.Store
	dataDir string
	all    []tokens.Token
	cursor int
	state  tokenListState
	notice string // brief status line shown below the table
	// Edit state — holds the in-progress rune buffer for the Notes field.
	editBuf []rune
	editPos int
	// Assign state — list of configured channels for the picker.
	assignChannels []alert.ChannelConfig
	assignCursor   int
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

// buildTokenSnapshot converts the in-memory token slice to the deployed-token
// snapshot format expected by watch.WriteDeployedSnapshot.
func buildTokenSnapshot(toks []tokens.Token) []watch.DeployedToken {
	var out []watch.DeployedToken
	for _, t := range toks {
		if t.DeployedPath == "" {
			continue
		}
		out = append(out, watch.DeployedToken{
			ID:             t.ID,
			Type:           t.Type,
			DeployedPath:   t.DeployedPath,
			AlertChannelID: t.AlertChannelID,
		})
	}
	return out
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
			// Load configured channels and enter the assign picker.
			cfg, _ := alert.Load(m.dataDir)
			m.assignChannels = cfg.Channels
			// Set cursor to the channel already assigned to this token (if any).
			m.assignCursor = len(cfg.Channels) // default: "Remove assignment" row
			assignedID := m.all[m.cursor].AlertChannelID
			for i, ch := range cfg.Channels {
				if ch.ID == assignedID {
					m.assignCursor = i
					break
				}
			}
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
			// Sync deployed_tokens.json so the headless watcher drops this
			// path without needing a restart.
			if m.dataDir != "" && m.st != nil {
				if ts, lerr := m.st.ListTokens(); lerr == nil {
					snap := buildTokenSnapshot(ts)
					_ = watch.WriteDeployedSnapshot(m.dataDir, snap)
				}
			}
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

	boxW := m.width - 2
	if boxW < 10 {
		boxW = 10
	}

	switch m.state {
	case tokenListStateConfDel:
		if len(m.all) > 0 {
			return m.viewConfirmDelete(boxW)
		}
	case tokenListStateEdit:
		if len(m.all) > 0 {
			return m.viewEdit(boxW)
		}
	case tokenListStateAssign:
		if len(m.all) > 0 {
			return m.viewAssign(boxW)
		}
	}

	return m.viewTable(boxW)
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
			triggered = WarningStyle.Render("yes ⚠")
		}

		marker := "  "
		if isCursor {
			marker = "▸ "
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
	footer := HelpTextStyle.Render("↑/↓ browse   d delete   e edit notes   a assign channel   esc back")
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

// updateAssign handles the channel-picker state.
// IMPORTANT: uses only non-printable keys for control so paste works correctly.
func (m TokenListModel) updateAssign(msg tea.KeyMsg) (TokenListModel, tea.Cmd) {
	// Total rows = len(assignChannels) + 1 ("Remove assignment" row).
	nRows := len(m.assignChannels) + 1
	switch msg.String() {
	case "esc":
		m.assignChannels = nil
		m.state = tokenListStateBrowse
	case "up", "k":
		if m.assignCursor > 0 {
			m.assignCursor--
		}
	case "down", "j":
		if m.assignCursor < nRows-1 {
			m.assignCursor++
		}
	case "enter":
		if len(m.all) == 0 {
			m.state = tokenListStateBrowse
			return m, nil
		}
		tok := m.all[m.cursor]
		if m.assignCursor < len(m.assignChannels) {
			// Assign the selected channel.
			tok.AlertChannelID = m.assignChannels[m.assignCursor].ID
		} else {
			// "Remove assignment" row — clear the field.
			tok.AlertChannelID = ""
		}
		var err error
		if m.st != nil {
			err = m.st.UpdateToken(tok)
		}
		m.assignChannels = nil
		m.state = tokenListStateBrowse
		if err != nil {
			m.notice = ErrorStyle.Render("Assign failed: " + err.Error())
		} else {
			if tok.AlertChannelID == "" {
				m.notice = MutedStyle.Render("Alert channel assignment removed")
			} else {
				m.notice = lipgloss.NewStyle().Foreground(ColorPrimary).Render(
					"Channel assigned to token " + tok.ID)
			}
		}
		m.reload()
	}
	return m, nil
}

func (m TokenListModel) viewAssign(boxW int) string {
	if len(m.all) == 0 {
		return m.viewTable(boxW)
	}
	tok := m.all[m.cursor]

	var sb strings.Builder
	sb.WriteString(MutedStyle.Render(fmt.Sprintf("  Token: %s (%s)\n\n", tok.ID, tok.Type)))

	if len(m.assignChannels) == 0 {
		sb.WriteString(MutedStyle.Render("  No alert channels configured.\n"))
		sb.WriteString(MutedStyle.Render("  Configure one in Alert Settings first.\n"))
	} else {
		// Load current config to know the default index.
		alertCfg, _ := alert.Load(m.dataDir)
		for i, ch := range m.assignChannels {
			cursor := "  "
			rowStyle := NormalItemStyle
			if i == m.assignCursor {
				cursor = SelectedItemStyle().Render("▸") + " "
				rowStyle = SelectedItemStyle()
			}
			typeLabel := ch.Type
			for _, c := range alert.Channels {
				if c.Type == ch.Type {
					typeLabel = c.Label
					break
				}
			}
			// Hint: masked credential.
			hint := ""
			switch ch.Type {
			case alert.ChannelTelegram:
				hint = alert.MaskSecret(ch.BotToken)
			case alert.ChannelNtfy:
				hint = ch.Topic
			default:
				hint = alert.MaskSecret(ch.WebhookURL)
			}
			assigned := ""
			if tok.AlertChannelID == ch.ID {
				assigned = MutedStyle.Render(" ✓ current")
			}
			isDefault := false
			if i < len(alertCfg.Channels) && i == alertCfg.DefaultIndex {
				isDefault = true
			}
			defaultMark := ""
			if isDefault {
				defaultMark = MutedStyle.Render(" ★")
			}
			line := fmt.Sprintf("%-22s %s", typeLabel, hint)
			sb.WriteString(cursor + rowStyle.Render(line) + assigned + defaultMark + "\n")
		}
	}

	// "Remove assignment" row.
	removeIdx := len(m.assignChannels)
	removeCursor := "  "
	removeStyle := MutedStyle
	if m.assignCursor == removeIdx {
		removeCursor = SelectedItemStyle().Render("▸") + " "
		removeStyle = SelectedItemStyle()
	}
	sb.WriteString(removeCursor + removeStyle.Render("✕  Remove assignment (use default)") + "\n")

	content := strings.TrimRight(sb.String(), "\n")
	box := renderBoxInner("Assign Alert Channel", content, boxW, ColorPrimary)
	footer := HelpTextStyle.Render("↑/↓ browse   enter confirm   esc cancel")
	return lipgloss.JoinVertical(lipgloss.Left, box, footer)
}

// truncate shortens s to n chars, appending … if clipped.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}
