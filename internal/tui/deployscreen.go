package tui

import (
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/arjunjaincs/decoyd/internal/deploy"
	"github.com/arjunjaincs/decoyd/internal/store"
	"github.com/arjunjaincs/decoyd/internal/tokens"
	"github.com/arjunjaincs/decoyd/internal/watch"
)

// ----------------------------------------------------------------------------
// Messages
// ----------------------------------------------------------------------------

// DeployScreenDoneMsg is sent when the user finishes the Deploy screen.
type DeployScreenDoneMsg struct{}

// ----------------------------------------------------------------------------
// deployState
// ----------------------------------------------------------------------------

type deployState int

const (
	deployStatePickToken    deployState = iota // select which token to deploy
	deployStatePickPath                        // pick destination (preset or custom)
	deployStateCustomPath                      // typing a custom path
	deployStateConfirm                         // confirm before writing
	deployStateDone                            // result shown
	deployStateConfirmDelete                   // confirming record deletion from picker
)

// ----------------------------------------------------------------------------
// DeployModel
// ----------------------------------------------------------------------------

// DeployModel is the bubbletea model for the Deploy screen.
type DeployModel struct {
	width   int
	height  int
	state   deployState
	st      *store.Store
	dataDir string // for writing deployed_tokens.json after deploy/delete

	// Token list (show all so user can re-deploy elsewhere)
	allTokens []tokens.Token
	tokenCur  int // cursor in allTokens

	// Path picker
	presets   []deploy.PresetDir
	pathCur   int  // cursor in presets; len(presets) = custom input
	customBuf []rune
	customPos int

	// Result
	result    string // success or error description
	resultErr bool

	// Delete confirm
	deleteErr string // non-empty if last delete attempt failed
}

// NewDeployModel creates a fresh DeployModel, loading tokens from the store.
func NewDeployModel(width, height int, st *store.Store, dataDir string) DeployModel {
	m := DeployModel{
		width:   width,
		height:  height,
		st:      st,
		dataDir: dataDir,
	}
	// Load presets (soft failure — show empty list).
	presets, _ := deploy.PresetDirs()
	m.presets = presets

	// Load tokens (soft failure).
	if st != nil {
		ts, _ := st.ListTokens()
		m.allTokens = ts
	}
	return m
}

// Init satisfies tea.Model.
func (m DeployModel) Init() tea.Cmd { return nil }

// customIdx is the virtual index beyond presets that means "custom path".
func (m DeployModel) customIdx() int { return len(m.presets) }

// ----------------------------------------------------------------------------
// Update
// ----------------------------------------------------------------------------

func (m DeployModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height

	case tea.KeyMsg:
		switch m.state {
		case deployStatePickToken:
			return m.updatePickToken(msg)
		case deployStatePickPath:
			return m.updatePickPath(msg)
		case deployStateCustomPath:
			return m.updateCustomPath(msg)
		case deployStateConfirm:
			return m.updateConfirm(msg)
		case deployStateConfirmDelete:
			return m.updateConfirmDelete(msg)
		case deployStateDone:
			return m, func() tea.Msg { return DeployScreenDoneMsg{} }
		}
	}
	return m, nil
}

func (m DeployModel) updatePickToken(msg tea.KeyMsg) (DeployModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.tokenCur > 0 {
			m.tokenCur--
		}
	case "down", "j":
		if m.tokenCur < len(m.allTokens)-1 {
			m.tokenCur++
		}
	case "enter":
		if len(m.allTokens) == 0 {
			return m, nil
		}
		m.state = deployStatePickPath
	case "d":
		if len(m.allTokens) > 0 {
			m.deleteErr = ""
			m.state = deployStateConfirmDelete
		}
	case "esc":
		return m, func() tea.Msg { return DeployScreenDoneMsg{} }
	}
	return m, nil
}

// updateConfirmDelete handles the delete confirmation prompt.
func (m DeployModel) updateConfirmDelete(msg tea.KeyMsg) (DeployModel, tea.Cmd) {
	switch msg.String() {
	case "y", "enter":
		if len(m.allTokens) == 0 {
			m.state = deployStatePickToken
			return m, nil
		}
		tok := m.allTokens[m.tokenCur]
		var err error
		if m.st != nil {
			err = m.st.DeleteToken(tok.ID)
		}
		if err != nil {
			m.deleteErr = "Delete failed: " + err.Error()
		} else {
			m.deleteErr = ""
			// Reload token list and clamp cursor.
			if m.st != nil {
				ts, _ := m.st.ListTokens()
				m.allTokens = ts
			}
			if m.tokenCur >= len(m.allTokens) && m.tokenCur > 0 {
				m.tokenCur = len(m.allTokens) - 1
			}
			// Update deployed_tokens.json so any running headless watcher
			// stops watching the deleted token's path immediately.
			_ = watch.ReconcileSnapshot(m.st, m.dataDir)
		}
		m.state = deployStatePickToken
	case "n", "esc":
		m.state = deployStatePickToken
	}
	return m, nil
}

func (m DeployModel) updatePickPath(msg tea.KeyMsg) (DeployModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.pathCur > 0 {
			m.pathCur--
		}
	case "down", "j":
		if m.pathCur < m.customIdx() {
			m.pathCur++
		}
	case "enter":
		if m.pathCur == m.customIdx() {
			m.state = deployStateCustomPath
			return m, nil
		}
		m.state = deployStateConfirm
	case "esc":
		m.state = deployStatePickToken
	}
	return m, nil
}

func (m DeployModel) updateCustomPath(msg tea.KeyMsg) (DeployModel, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if len(m.customBuf) == 0 {
			return m, nil // require non-empty
		}
		// Add a synthetic preset from the custom buf.
		raw := string(m.customBuf)
		resolved, err := deploy.SanitizePath(raw)
		if err != nil {
			m.result = "Invalid path: " + err.Error()
			m.resultErr = true
			m.state = deployStateDone
			return m, nil
		}
		m.presets = append(m.presets, deploy.PresetDir{Label: raw, Path: resolved})
		m.pathCur = len(m.presets) - 1
		m.state = deployStateConfirm
	case "esc":
		m.state = deployStatePickPath
	case "backspace", "ctrl+h":
		if m.customPos > 0 {
			m.customBuf = append(m.customBuf[:m.customPos-1], m.customBuf[m.customPos:]...)
			m.customPos--
		}
	case "left":
		if m.customPos > 0 {
			m.customPos--
		}
	case "right":
		if m.customPos < len(m.customBuf) {
			m.customPos++
		}
	default:
		if len(msg.Runes) > 0 {
			r := msg.Runes
			nb := make([]rune, 0, len(m.customBuf)+len(r))
			nb = append(nb, m.customBuf[:m.customPos]...)
			nb = append(nb, r...)
			nb = append(nb, m.customBuf[m.customPos:]...)
			m.customBuf = nb
			m.customPos += len(r)
		}
	}
	return m, nil
}

func (m DeployModel) updateConfirm(msg tea.KeyMsg) (DeployModel, tea.Cmd) {
	switch msg.String() {
	case "enter", "y":
		return m.doDeploy(false)
	case "d":
		return m.doDeploy(true) // dry-run
	case "esc", "n":
		m.state = deployStatePickPath
	}
	return m, nil
}

// doDeploy performs (or dry-runs) the actual file write.
func (m DeployModel) doDeploy(dryRun bool) (DeployModel, tea.Cmd) {
	if len(m.allTokens) == 0 || m.pathCur >= len(m.presets) {
		m.result = "Nothing to deploy."
		m.resultErr = true
		m.state = deployStateDone
		return m, nil
	}

	tok := m.allTokens[m.tokenCur]
	targetDir := m.presets[m.pathCur].Path

	res, err := deploy.DeployToFile(tok, targetDir, deploy.Options{DryRun: dryRun})
	if err != nil {
		if errors.Is(err, deploy.ErrAlreadyExists) {
			m.result = fmt.Sprintf("File already exists: %s\nDelete it first or choose a different directory.", res.DeployedTo)
		} else {
			m.result = "Deploy failed: " + err.Error()
		}
		m.resultErr = true
		m.state = deployStateDone
		return m, nil
	}

	// Update the store with the deployed path.
	if !dryRun && m.st != nil {
		tok.DeployedPath = res.DeployedTo
		_ = m.st.UpdateToken(tok)
		// Refresh local list.
		if ts, err := m.st.ListTokens(); err == nil {
			m.allTokens = ts
		}
		// Update deployed_tokens.json so any running headless watcher
		// sees the new path immediately (without requiring a TUI restart).
		_ = watch.ReconcileSnapshot(m.st, m.dataDir)
	}

	if dryRun {
		msg := fmt.Sprintf("[DRY RUN] Would write:\n  %s (perm %04o)", res.DeployedTo, deploy.PermForType(tok.Type))
		for _, extra := range res.ExtraFiles {
			msg += fmt.Sprintf("\n  %s (perm 0644)", extra)
		}
		msg += "\n  Nothing was written."
		m.result = msg
	} else {
		msg := fmt.Sprintf("%s Deployed!\n  %s (perm %04o)", G.OK, res.DeployedTo, deploy.PermForType(tok.Type))
		for _, extra := range res.ExtraFiles {
			msg += fmt.Sprintf("\n  %s (perm 0644)", extra)
		}
		m.result = msg
	}
	m.resultErr = false
	m.state = deployStateDone
	return m, nil
}

// ----------------------------------------------------------------------------
// View
// ----------------------------------------------------------------------------

func (m DeployModel) View() string {
	if m.width < MinTermWidth {
		return NarrowTermMsg
	}
	switch m.state {
	case deployStatePickToken:
		return m.viewPickToken()
	case deployStatePickPath:
		return m.viewPickPath()
	case deployStateCustomPath:
		return m.viewCustomPath()
	case deployStateConfirm:
		return m.viewConfirm()
	case deployStateDone:
		return m.viewDone()
	case deployStateConfirmDelete:
		return m.viewConfirmDelete()
	}
	return ""
}

func (m DeployModel) viewPickToken() string {
	boxW := m.width - 2
	if boxW < 10 {
		boxW = 10
	}

	if len(m.allTokens) == 0 {
		content := MutedStyle.Render("  No tokens found. Generate some first (option 1).")
		box := renderBoxInner("Deploy — Select Token", content, boxW, ColorBorder)
		return lipgloss.JoinVertical(lipgloss.Left, box,
			HelpTextStyle.Render("esc back"))
	}

	var sb strings.Builder
	for i, tok := range m.allTokens {
		marker := "  "
		if i == m.tokenCur {
			marker = G.Cursor + " "
		}
		deployed := ""
		if tok.DeployedPath != "" {
			deployed = MutedStyle.Render("  " + G.Arrow + " " + tok.DeployedPath)
		}
		triggered := ""
		if tok.Triggered {
			triggered = WarningStyle.Render(" " + G.Warn + " triggered")
		}
		label := fmt.Sprintf("%s  %s%s", tok.Type, tok.Notes, triggered)
		var row string
		if i == m.tokenCur {
			row = SelectedItemStyle().Render(marker+label) + deployed
		} else {
			row = NormalItemStyle.Render(marker+label) + deployed
		}
		sb.WriteString(row + "\n")
	}

	content := strings.TrimRight(sb.String(), "\n")
	box := renderBoxInner("Deploy — Select Token", content, boxW, ColorBorder)

	// Show delete error if the last delete attempt failed.
	var rows []string
	rows = append(rows, box)
	if m.deleteErr != "" {
		rows = append(rows, ErrorStyle.Render(m.deleteErr))
	}
	rows = append(rows, HelpTextStyle.Render(G.NavUp+"/"+G.NavDown+" navigate   enter select   d delete   esc back   ? help"))
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

// viewConfirmDelete renders the delete confirmation prompt.
func (m DeployModel) viewConfirmDelete() string {
	boxW := m.width - 2
	if boxW < 10 {
		boxW = 10
	}
	if len(m.allTokens) == 0 {
		return ""
	}
	tok := m.allTokens[m.tokenCur]

	var sb strings.Builder
	sb.WriteString(WarningStyle.Render("  Delete this token record?") + "\n\n")
	sb.WriteString(MutedStyle.Render("  Type:  ") + NormalItemStyle.Render(tok.Type) + "\n")
	sb.WriteString(MutedStyle.Render("  ID:    ") + NormalItemStyle.Render(tok.ID) + "\n")
	if tok.DeployedPath != "" {
		sb.WriteString(MutedStyle.Render("  Path:  ") + NormalItemStyle.Render(tok.DeployedPath) + "\n")
		sb.WriteString("\n")
		sb.WriteString(MutedStyle.Render("  Note: the deployed file is NOT removed from disk.") + "\n")
	}

	content := strings.TrimRight(sb.String(), "\n")
	box := renderBoxInner("Delete Token", content, boxW, ColorDanger)
	footer := HelpTextStyle.Render("y/enter confirm   n/esc cancel")
	return lipgloss.JoinVertical(lipgloss.Left, box, footer)
}

func (m DeployModel) viewPickPath() string {
	boxW := m.width - 2
	if boxW < 10 {
		boxW = 10
	}

	tok := m.allTokens[m.tokenCur]
	header := MutedStyle.Render(fmt.Sprintf("  Token: %s   File: %s\n", tok.Type, tok.Filename))

	var sb strings.Builder
	sb.WriteString(header + "\n")

	for i, p := range m.presets {
		marker := "  "
		if i == m.pathCur {
			marker = G.Cursor + " "
		}
		line := fmt.Sprintf("%s%s", marker, p.Label)
		sub := MutedStyle.Render("    " + p.Path)
		if i == m.pathCur {
			sb.WriteString(SelectedItemStyle().Render(line) + "\n" + sub + "\n")
		} else {
			sb.WriteString(NormalItemStyle.Render(line) + "\n" + sub + "\n")
		}
	}

	// Custom path option.
	customMarker := "  "
	if m.pathCur == m.customIdx() {
		customMarker = G.Cursor + " "
	}
	custLine := customMarker + "Custom path" + G.Ellipsis
	if m.pathCur == m.customIdx() {
		sb.WriteString(SelectedItemStyle().Render(custLine) + "\n")
	} else {
		sb.WriteString(NormalItemStyle.Render(custLine) + "\n")
	}

	content := strings.TrimRight(sb.String(), "\n")
	box := renderBoxInner("Deploy — Choose Destination", content, boxW, ColorBorder)
	footer := HelpTextStyle.Render(G.NavUp + "/" + G.NavDown + " navigate   enter select   esc back")
	return lipgloss.JoinVertical(lipgloss.Left, box, footer)
}

func (m DeployModel) viewCustomPath() string {
	boxW := m.width - 2
	if boxW < 10 {
		boxW = 10
	}

	tok := m.allTokens[m.tokenCur]
	header := MutedStyle.Render(fmt.Sprintf("  Token: %s   File: %s\n\n", tok.Type, tok.Filename))

	before := string(m.customBuf[:m.customPos])
	after := string(m.customBuf[m.customPos:])
	cur := lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true).Render("|")

	prompt := MutedStyle.Render("  Path: ") + NormalItemStyle.Render(before) + cur + NormalItemStyle.Render(after)
	hint := "\n\n" + MutedStyle.Render("  Tip: ~ is expanded to your home directory.")

	content := header + prompt + hint
	box := renderBoxInner("Deploy — Custom Path", content, boxW, ColorBorder)
	footer := HelpTextStyle.Render("enter confirm   esc back")
	return lipgloss.JoinVertical(lipgloss.Left, box, footer)
}

func (m DeployModel) viewConfirm() string {
	boxW := m.width - 2
	if boxW < 10 {
		boxW = 10
	}

	tok := m.allTokens[m.tokenCur]
	dest := m.presets[m.pathCur]

	var sb strings.Builder
	sb.WriteString(MutedStyle.Render("  Token:      ") + NormalItemStyle.Render(tok.Type) + "\n")
	sb.WriteString(MutedStyle.Render("  File:       ") + NormalItemStyle.Render(tok.Filename) + "\n")
	sb.WriteString(MutedStyle.Render("  Directory:  ") + NormalItemStyle.Render(dest.Path) + "\n")
	sb.WriteString(MutedStyle.Render("  Permission: ") + NormalItemStyle.Render(fmt.Sprintf("%04o", deploy.PermForType(tok.Type))) + "\n")
	sb.WriteString("\n")
	sb.WriteString(lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true).Render("  Write this file to disk?"))

	content := sb.String()
	box := renderBoxInner("Deploy — Confirm", content, boxW, ColorBorder)
	footer := HelpTextStyle.Render("enter/y confirm   d dry-run preview   n/esc cancel")
	return lipgloss.JoinVertical(lipgloss.Left, box, footer)
}

func (m DeployModel) viewDone() string {
	boxW := m.width - 2
	if boxW < 10 {
		boxW = 10
	}

	borderColor := ColorPrimary
	if m.resultErr {
		borderColor = ColorDanger
	}

	var sb strings.Builder
	for _, line := range strings.Split(m.result, "\n") {
		if m.resultErr {
			sb.WriteString(ErrorStyle.Render("  "+line) + "\n")
		} else {
			sb.WriteString("  " + line + "\n")
		}
	}

	content := strings.TrimRight(sb.String(), "\n")
	box := renderBoxInner("Deploy", content, boxW, borderColor)
	footer := HelpTextStyle.Render("any key to return to menu")
	return lipgloss.JoinVertical(lipgloss.Left, box, footer)
}
