package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/arjunjaincs/decoyd/internal/alert"
)

// ----------------------------------------------------------------------------
// Messages
// ----------------------------------------------------------------------------

// AlertScreenDoneMsg is sent when the user exits the Alert Settings screen.
// The root model navigates back to the main menu.
type AlertScreenDoneMsg struct{}

// alertTestResultMsg carries the outcome of an async test-send.
type alertTestResultMsg struct {
	err error
}

// ----------------------------------------------------------------------------
// State machine
// ----------------------------------------------------------------------------

type alertState int

const (
	alertStateForm        alertState = iota // user is filling in the form
	alertStateSending                       // test-send in progress
	alertStateDone                          // showing result (success or error)
)

// ----------------------------------------------------------------------------
// Field indices
// ----------------------------------------------------------------------------

// Field cursor positions within the form.
const (
	alertFieldChannel   = 0 // channel selector — Enter cycles
	alertFieldPrimary   = 1 // WebhookURL / BotToken / ServerURL depending on channel
	alertFieldSecondary = 2 // ChatID (Telegram) / Topic (ntfy) — hidden for other channels
	alertFieldSend      = 3 // "Send test alert" virtual button
)

// ----------------------------------------------------------------------------
// AlterModel
// ----------------------------------------------------------------------------

// AlertModel is the bubbletea model for the Alert Settings screen.
type AlertModel struct {
	state   alertState
	width   int
	height  int
	dataDir string // path to the data directory, for Load/Save

	// channel selector
	channelIdx int // index into alert.Channels

	// text input buffers (same []rune + cursor pattern as deployscreen.go)
	primaryBuf  []rune
	primaryPos  int
	secondaryBuf []rune
	secondaryPos int

	// cursor within the form fields
	fieldCursor int // 0..alertFieldSend

	// result of the last test-send (displayed in alertStateDone)
	resultMsg string
	resultErr bool

	// spinner frame counter (used while alertStateSending)
	spinFrame int

	// tick counter for spinner
	spinTick int
}

// spinnerFrames is the spinner used while the test send is in flight.
var spinnerFrames = []string{"⠋", "⠙", "⠸", "⠴", "⠦", "⠇"}

type alertSpinTickMsg struct{}

func tickAlertSpin() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg {
		return alertSpinTickMsg{}
	})
}

// ----------------------------------------------------------------------------
// Constructor
// ----------------------------------------------------------------------------

// NewAlertModel creates the Alert Settings model, pre-loaded from disk if a
// config file exists.
func NewAlertModel(width, height int, dataDir string) AlertModel {
	m := AlertModel{
		width:   width,
		height:  height,
		dataDir: dataDir,
	}
	// Pre-populate from saved config if available.
	cfg, err := alert.Load(dataDir)
	if err == nil && len(cfg.Channels) > 0 {
		ch := cfg.Channels[cfg.DefaultIndex]
		// Find channel index by type.
		for i, c := range alert.Channels {
			if c.Type == ch.Type {
				m.channelIdx = i
				break
			}
		}
		switch ch.Type {
		case alert.ChannelTelegram:
			m.primaryBuf = []rune(ch.BotToken)
			m.primaryPos = len(m.primaryBuf)
			m.secondaryBuf = []rune(ch.ChatID)
			m.secondaryPos = len(m.secondaryBuf)
		case alert.ChannelNtfy:
			srv := ch.ServerURL
			if srv == "" {
				srv = "https://ntfy.sh"
			}
			m.primaryBuf = []rune(srv)
			m.primaryPos = len(m.primaryBuf)
			m.secondaryBuf = []rune(ch.Topic)
			m.secondaryPos = len(m.secondaryBuf)
		default:
			m.primaryBuf = []rune(ch.WebhookURL)
			m.primaryPos = len(m.primaryBuf)
		}
	}
	return m
}

// SetSize satisfies the Sizable interface used by propagateSize.
func (m AlertModel) SetSize(w, h int) AlertModel {
	m.width = w
	m.height = h
	return m
}

// Init satisfies tea.Model.
func (m AlertModel) Init() tea.Cmd {
	return nil
}

// ----------------------------------------------------------------------------
// channelType returns the type string of the currently selected channel.
// ----------------------------------------------------------------------------

func (m *AlertModel) channelType() string {
	if m.channelIdx < 0 || m.channelIdx >= len(alert.Channels) {
		return alert.ChannelDiscord
	}
	return alert.Channels[m.channelIdx].Type
}

// hasSecondaryField returns true when the active channel uses two credential
// fields (Telegram: BotToken + ChatID; ntfy: ServerURL + Topic).
func (m *AlertModel) hasSecondaryField() bool {
	t := m.channelType()
	return t == alert.ChannelTelegram || t == alert.ChannelNtfy
}

// maxFieldCursor returns the highest valid fieldCursor value.
// The Send button (alertFieldSend = 3) is always reachable; the secondary
// field is conditionally shown but the cursor skips it when absent.
func (m *AlertModel) maxFieldCursor() int {
	return alertFieldSend
}

// buildChannelConfig builds a ChannelConfig from current form state.
func (m *AlertModel) buildChannelConfig() alert.ChannelConfig {
	t := m.channelType()
	primary := string(m.primaryBuf)
	secondary := string(m.secondaryBuf)
	switch t {
	case alert.ChannelTelegram:
		return alert.ChannelConfig{Type: t, BotToken: primary, ChatID: secondary}
	case alert.ChannelNtfy:
		return alert.ChannelConfig{Type: t, ServerURL: primary, Topic: secondary}
	default:
		return alert.ChannelConfig{Type: t, WebhookURL: primary}
	}
}

// primaryLabel returns the label for the primary input field.
func (m *AlertModel) primaryLabel() string {
	switch m.channelType() {
	case alert.ChannelTelegram:
		return "Bot Token"
	case alert.ChannelNtfy:
		return "Server URL"
	default:
		return "Webhook URL"
	}
}

// secondaryLabel returns the label for the secondary input field.
func (m *AlertModel) secondaryLabel() string {
	switch m.channelType() {
	case alert.ChannelTelegram:
		return "Chat ID"
	case alert.ChannelNtfy:
		return "Topic"
	default:
		return ""
	}
}

// isPrimarySecret returns true when the primary field value should be masked.
func (m *AlertModel) isPrimarySecret() bool {
	// ntfy ServerURL is not a secret; everything else is.
	return m.channelType() != alert.ChannelNtfy
}

// isSecondarySecret returns true when the secondary field should be masked.
func (m *AlertModel) isSecondarySecret() bool {
	// Telegram ChatID is not secret; ntfy Topic is (acts as a shared password).
	return m.channelType() == alert.ChannelNtfy
}

// ----------------------------------------------------------------------------
// Update
// ----------------------------------------------------------------------------

func (m AlertModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case alertSpinTickMsg:
		if m.state == alertStateSending {
			m.spinFrame = (m.spinFrame + 1) % len(spinnerFrames)
			return m, tickAlertSpin()
		}
		return m, nil

	case alertTestResultMsg:
		m.state = alertStateDone
		if msg.err != nil {
			m.resultMsg = "Test send failed: " + msg.err.Error()
			m.resultErr = true
		} else {
			m.resultMsg = "✓ Test alert delivered successfully"
			m.resultErr = false
		}
		return m, nil
	}

	switch m.state {
	case alertStateForm:
		return m.updateForm(msg)
	case alertStateDone:
		return m.updateDone(msg)
	}
	return m, nil
}

func (m AlertModel) updateForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	switch km.String() {
	case "esc":
		return m, func() tea.Msg { return AlertScreenDoneMsg{} }

	case "tab", "down", "j":
		max := m.maxFieldCursor()
		if m.fieldCursor < max {
			m.fieldCursor++
			// Skip secondary if channel doesn't use it.
			if m.fieldCursor == alertFieldSecondary && !m.hasSecondaryField() {
				m.fieldCursor++
			}
		}

	case "shift+tab", "up", "k":
		if m.fieldCursor > 0 {
			m.fieldCursor--
			if m.fieldCursor == alertFieldSecondary && !m.hasSecondaryField() {
				m.fieldCursor--
			}
		}

	case "enter":
		switch m.fieldCursor {
		case alertFieldChannel:
			// Cycle forward through channel types.
			m.channelIdx = (m.channelIdx + 1) % len(alert.Channels)
			// Reset field buffers when channel type changes.
			m.primaryBuf = nil
			m.primaryPos = 0
			m.secondaryBuf = nil
			m.secondaryPos = 0
		case alertFieldSend:
			return m.doTestSend()
		}

	case "s":
		// 's' fires test-send from anywhere in the form.
		return m.doTestSend()

	default:
		// Route typing to the focused field.
		switch m.fieldCursor {
		case alertFieldPrimary:
			m.primaryBuf, m.primaryPos = handleTextInput(m.primaryBuf, m.primaryPos, km)
		case alertFieldSecondary:
			m.secondaryBuf, m.secondaryPos = handleTextInput(m.secondaryBuf, m.secondaryPos, km)
		}
	}
	return m, nil
}

func (m AlertModel) updateDone(msg tea.Msg) (tea.Model, tea.Cmd) {
	_, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	// Any key: reset to form (keeps entered values intact).
	m.state = alertStateForm
	return m, nil
}

// doTestSend validates the form and fires an async test-send.
func (m AlertModel) doTestSend() (AlertModel, tea.Cmd) {
	primary := strings.TrimSpace(string(m.primaryBuf))
	secondary := strings.TrimSpace(string(m.secondaryBuf))

	// Replace trimmed values back into buffers so the form reflects what was used.
	m.primaryBuf = []rune(primary)
	m.primaryPos = len(m.primaryBuf)
	m.secondaryBuf = []rune(secondary)
	m.secondaryPos = len(m.secondaryBuf)

	// Validate before touching the network.
	if primary == "" {
		m.state = alertStateDone
		m.resultMsg = "Test send failed: " + m.primaryLabel() + " is required"
		m.resultErr = true
		return m, nil
	}
	// URL-based channels: require https:// or http:// prefix so the user gets a
	// helpful message instead of the raw 'unsupported protocol scheme' net error.
	chType := m.channelType()
	if chType != alert.ChannelTelegram && chType != alert.ChannelNtfy {
		if !strings.HasPrefix(primary, "https://") && !strings.HasPrefix(primary, "http://") {
			m.state = alertStateDone
			m.resultMsg = "Test send failed: Webhook URL must start with https://"
			m.resultErr = true
			return m, nil
		}
	}

	cfg := m.buildChannelConfig()
	a, err := alert.NewAlerter(cfg)
	if err != nil {
		m.state = alertStateDone
		m.resultMsg = "Test send failed: " + err.Error()
		m.resultErr = true
		return m, nil
	}

	// Save the config before sending so it persists even if the send fails.
	if m.dataDir != "" {
		alertCfg := alert.AlertConfig{
			Channels:     []alert.ChannelConfig{cfg},
			DefaultIndex: 0,
		}
		_ = alert.Save(m.dataDir, alertCfg)
	}

	m.state = alertStateSending

	return m, tea.Batch(
		tickAlertSpin(),
		func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			testPayload := alert.AlertPayload{
				TokenID:     "test-0000000000000000",
				TokenType:   "test",
				Path:        "(test send)",
				TriggeredAt: time.Now().UTC(),
				Detail:      "This is a test alert from Decoyd.",
			}
			err := a.Send(ctx, testPayload)
			return alertTestResultMsg{err: err}
		},
	)
}

// ----------------------------------------------------------------------------
// handleTextInput — shared rune-buffer editor (mirrors deployscreen.go)
// ----------------------------------------------------------------------------

func handleTextInput(buf []rune, pos int, km tea.KeyMsg) ([]rune, int) {
	switch km.String() {
	case "backspace", "ctrl+h":
		if pos > 0 {
			buf = append(buf[:pos-1], buf[pos:]...)
			pos--
		}
	case "delete":
		if pos < len(buf) {
			buf = append(buf[:pos], buf[pos+1:]...)
		}
	case "left":
		if pos > 0 {
			pos--
		}
	case "right":
		if pos < len(buf) {
			pos++
		}
	case "home", "ctrl+a":
		pos = 0
	case "end", "ctrl+e":
		pos = len(buf)
	default:
		if len(km.Runes) > 0 {
			r := km.Runes
			nb := make([]rune, 0, len(buf)+len(r))
			nb = append(nb, buf[:pos]...)
			nb = append(nb, r...)
			nb = append(nb, buf[pos:]...)
			buf = nb
			pos += len(r)
		}
	}
	return buf, pos
}

// ----------------------------------------------------------------------------
// View
// ----------------------------------------------------------------------------

func (m AlertModel) View() string {
	switch m.state {
	case alertStateSending:
		return m.viewSending()
	case alertStateDone:
		return m.viewDone()
	default:
		return m.viewForm()
	}
}

func (m AlertModel) viewForm() string {
	var sb strings.Builder

	// ── Channel selector ──────────────────────────────────────────────────
	chanLabel := ""
	if m.channelIdx >= 0 && m.channelIdx < len(alert.Channels) {
		chanLabel = alert.Channels[m.channelIdx].Label
	}
	sb.WriteString(m.renderField(alertFieldChannel, "Channel", chanLabel, false, false))
	sb.WriteString("\n")

	// ── Primary field ─────────────────────────────────────────────────────
	primaryDisplay := m.fieldDisplay(m.primaryBuf, m.primaryPos,
		m.fieldCursor == alertFieldPrimary, m.isPrimarySecret())
	sb.WriteString(m.renderField(alertFieldPrimary, m.primaryLabel(), primaryDisplay, false, false))
	sb.WriteString("\n")

	// ── Secondary field (conditional) ────────────────────────────────────
	if m.hasSecondaryField() {
		secDisplay := m.fieldDisplay(m.secondaryBuf, m.secondaryPos,
			m.fieldCursor == alertFieldSecondary, m.isSecondarySecret())
		sb.WriteString(m.renderField(alertFieldSecondary, m.secondaryLabel(), secDisplay, false, false))
		sb.WriteString("\n")
	}

	sb.WriteString("\n")

	// ── Send button ───────────────────────────────────────────────────────
	sendLabel := "[ Send test alert ]"
	if m.fieldCursor == alertFieldSend {
		sb.WriteString(SelectedItemStyle().Render("▸ " + sendLabel))
	} else {
		sb.WriteString(NormalItemStyle.Render("  " + sendLabel))
	}
	sb.WriteString("\n")

	content := sb.String()
	help := HelpTextStyle.Render("tab next field   enter confirm/cycle   s send test   esc back")
	return lipgloss.JoinVertical(lipgloss.Left,
		RenderBox("Alert Settings", content, m.width),
		help,
	)
}

// renderField renders one labelled form row, with cursor marker if focused.
func (m AlertModel) renderField(fieldIdx int, label, value string, isSecret, _ bool) string {
	focused := m.fieldCursor == fieldIdx
	prefix := "  "
	if focused {
		prefix = "▸ "
	}
	labelStr := MutedStyle.Render(label+":")
	var valueStr string
	if focused {
		valueStr = SelectedItemStyle().Render(value)
	} else {
		valueStr = NormalItemStyle.Render(value)
	}
	return fmt.Sprintf("%s%s  %s", prefix, labelStr, valueStr)
}

// fieldDisplay renders the visible string for a text input field.
// When focused: shows actual value + a cursor character at the insertion point.
// When unfocused and secret: masks all but last 4 chars.
func (m AlertModel) fieldDisplay(buf []rune, pos int, focused bool, isSecret bool) string {
	if len(buf) == 0 {
		if focused {
			return lipgloss.NewStyle().Foreground(ColorTextMuted).Render("│")
		}
		return MutedStyle.Render("(empty)")
	}
	if focused {
		// Show the real value with a block cursor at pos.
		before := string(buf[:pos])
		cursor := lipgloss.NewStyle().Background(ColorPrimary).Foreground(ColorBackground).Render(" ")
		after := ""
		if pos < len(buf) {
			after = string(buf[pos:])
		}
		return before + cursor + after
	}
	if isSecret {
		return alert.MaskSecret(string(buf))
	}
	return string(buf)
}

func (m AlertModel) viewSending() string {
	frame := spinnerFrames[m.spinFrame%len(spinnerFrames)]
	content := fmt.Sprintf("%s  Sending test alert…", frame)
	return RenderBox("Alert Settings", content, m.width)
}

func (m AlertModel) viewDone() string {
	var content string
	if m.resultErr {
		content = ErrorStyle.Render(m.resultMsg)
	} else {
		content = lipgloss.NewStyle().Foreground(ColorPrimary).Bold(true).Render(m.resultMsg)
	}
	content += "\n\n" + MutedStyle.Render("press any key to continue")
	return RenderBox("Alert Settings", content, m.width)
}
