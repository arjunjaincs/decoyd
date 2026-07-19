package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/arjunjaincs/decoyd/internal/store"
	"github.com/arjunjaincs/decoyd/internal/tokens"
)

// ----------------------------------------------------------------------------
// Messages
// ----------------------------------------------------------------------------

// GenScreenDoneMsg is sent when the user is finished with the Generate screen
// and the root model should navigate back to the main menu.
type GenScreenDoneMsg struct{}

// ----------------------------------------------------------------------------
// Flat item list (built once from Categories at init time)
// ----------------------------------------------------------------------------

// flatItem is a single selectable row in the generate checklist.
type flatItem struct {
	typeDef  tokens.TypeDef
	catIndex int // index into tokens.Categories (used for category headers)
}

// genFlatItems is the ordered list of all selectable token types.
// notesIdx (== len(genFlatItems)) is the virtual cursor position for the label field.
var genFlatItems = func() []flatItem {
	var items []flatItem
	for ci, cat := range tokens.Categories {
		for _, td := range cat.Types {
			items = append(items, flatItem{typeDef: td, catIndex: ci})
		}
	}
	return items
}()

// notesIdx is the cursor position of the label (notes) input field.
var notesIdx = len(genFlatItems) // = 8

// ----------------------------------------------------------------------------
// genState
// ----------------------------------------------------------------------------

type genState int

const (
	genStateSelect genState = iota // browsing the checklist
	genStateDone                   // results screen
)

// ----------------------------------------------------------------------------
// genResult — one entry per token that was attempted
// ----------------------------------------------------------------------------

type genResult struct {
	typeDef  tokens.TypeDef
	tokenID  string
	filename string
	err      error
}

// ----------------------------------------------------------------------------
// GenerateModel
// ----------------------------------------------------------------------------

// GenerateModel is the bubbletea model for the Generate screen.
type GenerateModel struct {
	width      int
	height     int
	cursor     int          // 0..notesIdx  (notesIdx = notes field)
	selected   map[int]bool // selected flat item indices
	notes      []rune       // label/notes text buffer
	noteCursor int          // insert position inside notes
	state      genState
	results    []genResult
	inlineErr  string // shown briefly when user presses Enter with nothing selected
	st         *store.Store
}

// NewGenerateModel creates a GenerateModel for the given terminal dimensions.
// st must be an open store; it is used to persist generated tokens.
func NewGenerateModel(width, height int, st *store.Store) GenerateModel {
	return GenerateModel{
		width:    width,
		height:   height,
		selected: make(map[int]bool),
		st:       st,
	}
}

// Init satisfies tea.Model — no I/O needed at startup.
func (m GenerateModel) Init() tea.Cmd { return nil }

// ----------------------------------------------------------------------------
// Update
// ----------------------------------------------------------------------------

func (m GenerateModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// In the done state, any key returns to the main menu.
	if m.state == genStateDone {
		if _, ok := msg.(tea.KeyMsg); ok {
			return m, func() tea.Msg { return GenScreenDoneMsg{} }
		}
		if wm, ok := msg.(tea.WindowSizeMsg); ok {
			m.width, m.height = wm.Width, wm.Height
		}
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tea.KeyMsg:
		// Clear inline error on any interaction.
		m.inlineErr = ""

		if m.cursor == notesIdx {
			return m.updateNotesInput(msg)
		}
		return m.updateChecklist(msg)
	}
	return m, nil
}

// updateChecklist handles navigation and selection in the token list.
func (m GenerateModel) updateChecklist(msg tea.KeyMsg) (GenerateModel, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < notesIdx {
			m.cursor++
		}
	case " ":
		m.selected[m.cursor] = !m.selected[m.cursor]
	case "enter":
		return m.doGenerate()
	case "esc":
		return m, func() tea.Msg { return GenScreenDoneMsg{} }
	}
	return m, nil
}

// updateNotesInput handles text input when the label field is focused.
// IMPORTANT: do NOT add single-letter shortcut cases here — they would
// eat characters during a paste (each pasted char arrives as a KeyMsg).
// Use only non-printable keys (up-arrow, esc, enter, ctrl+*, backspace)
// for navigation / control.
func (m GenerateModel) updateNotesInput(msg tea.KeyMsg) (GenerateModel, tea.Cmd) {
	switch msg.String() {
	case "up": // arrow key only — not "k" (rune, must go to buffer)
		m.cursor = notesIdx - 1 // move back into token list
	case "esc":
		m.cursor = 0
	case "enter":
		return m.doGenerate()
	case "backspace", "ctrl+h":
		if m.noteCursor > 0 {
			m.notes = append(m.notes[:m.noteCursor-1], m.notes[m.noteCursor:]...)
			m.noteCursor--
		}
	case "delete":
		if m.noteCursor < len(m.notes) {
			m.notes = append(m.notes[:m.noteCursor], m.notes[m.noteCursor+1:]...)
		}
	case "left", "ctrl+b":
		if m.noteCursor > 0 {
			m.noteCursor--
		}
	case "right", "ctrl+f":
		if m.noteCursor < len(m.notes) {
			m.noteCursor++
		}
	case "ctrl+a", "home":
		m.noteCursor = 0
	case "ctrl+e", "end":
		m.noteCursor = len(m.notes)
	default:
		if len(msg.Runes) > 0 {
			ins := msg.Runes
			newBuf := make([]rune, 0, len(m.notes)+len(ins))
			newBuf = append(newBuf, m.notes[:m.noteCursor]...)
			newBuf = append(newBuf, ins...)
			newBuf = append(newBuf, m.notes[m.noteCursor:]...)
			m.notes = newBuf
			m.noteCursor += len(ins)
		}
	}
	return m, nil
}

// doGenerate validates selection, calls generators, persists, shows results.
func (m GenerateModel) doGenerate() (GenerateModel, tea.Cmd) {
	// Require at least one selection.
	var anySelected bool
	for _, v := range m.selected {
		if v {
			anySelected = true
			break
		}
	}
	if !anySelected {
		m.inlineErr = "Select at least one token type with Space first."
		return m, nil
	}

	notes := strings.TrimSpace(string(m.notes))
	var results []genResult

	for i, item := range genFlatItems {
		if !m.selected[i] {
			continue
		}
		t, err := tokens.Generate(item.typeDef.Key)
		if err != nil {
			results = append(results, genResult{typeDef: item.typeDef, err: err})
			continue
		}
		t.Notes = notes
		if saveErr := m.st.SaveToken(t); saveErr != nil {
			results = append(results, genResult{typeDef: item.typeDef, err: saveErr})
			continue
		}
		results = append(results, genResult{
			typeDef:  item.typeDef,
			tokenID:  t.ID,
			filename: t.Filename,
		})
	}

	m.results = results
	m.state = genStateDone
	return m, nil
}

// ----------------------------------------------------------------------------
// View
// ----------------------------------------------------------------------------

func (m GenerateModel) View() string {
	if m.width < MinTermWidth {
		return NarrowTermMsg
	}

	if m.state == genStateDone {
		return m.viewDone()
	}
	return m.viewSelect()
}

// viewSelect renders the multi-select checklist.
func (m GenerateModel) viewSelect() string {
	var sb strings.Builder

	prevCat := -1
	for i, item := range genFlatItems {
		// Insert category header whenever the category changes.
		if item.catIndex != prevCat {
			if prevCat != -1 {
				sb.WriteString("\n")
			}
			header := MutedStyle.Render("  " + tokens.Categories[item.catIndex].Name)
			sb.WriteString(header + "\n")
			prevCat = item.catIndex
		}

		isCursor := m.cursor == i
		isSelected := m.selected[i]

		// Marker: ▸ if cursor, two spaces otherwise.
		markerStr := "  "
		if isCursor {
			markerStr = G.Cursor + " "
		}

		// Checkbox.
		var checkbox string
		if isSelected {
			checkbox = lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true).Render("["+G.OK+"]")
		} else {
			checkbox = MutedStyle.Render("[ ]")
		}

		label := item.typeDef.Label

		var row string
		if isCursor {
			row = SelectedItemStyle().Render(markerStr) + checkbox + SelectedItemStyle().Render(" "+label)
		} else {
			row = NormalItemStyle.Render(markerStr+label) // marker always prints; checkbox inline
			// Rebuild for non-cursor: marker + checkbox + label.
			row = NormalItemStyle.Render(markerStr) + checkbox + NormalItemStyle.Render(" "+label)
		}
		sb.WriteString(row + "\n")
	}

	// Notes / label field.
	sb.WriteString("\n")
	notesLine := m.renderNotesField()
	sb.WriteString(notesLine + "\n")

	// Inline error (if any).
	if m.inlineErr != "" {
		sb.WriteString("\n")
		sb.WriteString(WarningStyle.Render("  " + G.Warn + " " + m.inlineErr))
		sb.WriteString("\n")
	}

	content := sb.String()
	// Trim the trailing newline so the box padding looks even.
	content = strings.TrimRight(content, "\n")

	boxWidth := m.width - 2
	if boxWidth < 10 {
		boxWidth = 10
	}
	box := renderBoxInner("Generate a Decoy", content, boxWidth, ColorBorder)

	footer := HelpTextStyle.Render(G.NavUp + "/" + G.NavDown + " navigate   space toggle   enter generate   esc back   ? help")
	return lipgloss.JoinVertical(lipgloss.Left, box, footer)
}

// renderNotesField renders the label/notes input row.
func (m GenerateModel) renderNotesField() string {
	isFocused := m.cursor == notesIdx

	prefix := "  Label (optional): "
	if isFocused {
		prefix = SelectedItemStyle().Render(G.Cursor + " Label (optional): ")
	} else {
		prefix = MutedStyle.Render(prefix)
	}

	var textPart string
	if isFocused {
		// Show a blinking-cursor-style "|" at the insert position.
		before := string(m.notes[:m.noteCursor])
		after := string(m.notes[m.noteCursor:])
		cur := lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true).Render("|")
		textPart = NormalItemStyle.Render(before) + cur + NormalItemStyle.Render(after)
	} else if len(m.notes) == 0 {
		textPart = MutedStyle.Render("—")
	} else {
		textPart = NormalItemStyle.Render(string(m.notes))
	}

	return prefix + textPart
}

// viewDone renders the results screen.
func (m GenerateModel) viewDone() string {
	var sb strings.Builder

	ok, failed := 0, 0
	for _, r := range m.results {
		if r.err != nil {
			failed++
		} else {
			ok++
		}
	}

	// Header line.
	if ok > 0 {
		sb.WriteString(lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true).
			Render(fmt.Sprintf("  %d token(s) generated and saved.", ok)) + "\n\n")
	}

	// Result rows.
	for _, r := range m.results {
		if r.err != nil {
			icon := ErrorStyle.Render(G.Fail)
			label := NormalItemStyle.Render("  " + r.typeDef.Label)
			errTxt := ErrorStyle.Render("  error: " + r.err.Error())
			sb.WriteString("  " + icon + label + "\n" + errTxt + "\n")
		} else {
			icon := lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true).Render(G.OK)
			label := NormalItemStyle.Render(r.typeDef.Label)
			idTxt := MutedStyle.Render(fmt.Sprintf("  id:%s  file:%s", r.tokenID, r.filename))
			sb.WriteString("  " + icon + " " + label + "\n" + idTxt + "\n\n")
		}
	}

	if failed > 0 {
		sb.WriteString(WarningStyle.Render(fmt.Sprintf("  %d token(s) failed — check logs.", failed)) + "\n")
	}

	sb.WriteString("\n" + MutedStyle.Render("  Deploy from the main menu " + G.Arrow + " Deploy existing decoys."))

	content := strings.TrimRight(sb.String(), "\n")

	boxWidth := m.width - 2
	if boxWidth < 10 {
		boxWidth = 10
	}
	box := renderBoxInner("Generated", content, boxWidth, ColorPrimary)

	footer := HelpTextStyle.Render("any key to return to menu")
	return lipgloss.JoinVertical(lipgloss.Left, box, footer)
}
