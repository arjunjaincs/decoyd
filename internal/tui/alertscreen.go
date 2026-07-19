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
	alertStateList          alertState = iota // channel list (entry point when channels exist)
	alertStateConfirmDelete                   // confirm before deleting a channel
	alertStateForm                            // form: add or edit one channel
	alertStateSending                         // test-send in progress
	alertStateDone                            // showing result (success or error)
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

	// List state.
	listCursor int // cursor within channel list

	// editingID tracks which channel is being edited (empty = adding a new channel).
	// Used by autosaveCredentials / doTestSend to update the correct record.
	editingID string

	// channel selector (form state)
	channelIdx int // index into alert.Channels

	// savedChannels caches the last-used ChannelConfig for each channel type
	// within this session. Populated from disk at startup and updated after
	// every successful test-send. Used by loadChannelFields to restore the
	// correct credentials when the user cycles back to a previously configured
	// channel type, instead of showing a blank field.
	savedChannels map[string]alert.ChannelConfig

	// text input buffers (same []rune + cursor pattern as deployscreen.go)
	primaryBuf   []rune
	primaryPos   int
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
// Braille frames are used when the terminal supports Unicode; ASCII rotation otherwise.
var spinnerFrames = func() []string {
	if HasUnicode {
		return []string{"\u280b", "\u2819", "\u2838", "\u2826", "\u2807", "\u280b"}
	}
	return []string{"-", "\\", "|", "/", "-", "\\"}
}()

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
		width:         width,
		height:        height,
		dataDir:       dataDir,
		savedChannels: make(map[string]alert.ChannelConfig),
		state:         alertStateForm, // default for empty config
	}
	// Pre-populate from saved config if available.
	cfg, err := alert.Load(dataDir)
	if err == nil {
		for _, ch := range cfg.Channels {
			m.savedChannels[ch.Type] = ch
		}
		if len(cfg.Channels) > 0 {
			// Channels exist: start on the list screen.
			m.state = alertStateList
			ch, _ := cfg.DefaultChannel()
			for i, c := range alert.Channels {
				if c.Type == ch.Type {
					m.channelIdx = i
					break
				}
			}
			m.loadChannelFields()
		}
	}
	return m
}

// loadChannelFields sets primaryBuf/secondaryBuf from the savedChannels cache
// for the currently selected channel type.
// If no saved config exists for that type, the fields are cleared so the user
// sees an empty form (not stale credentials from a previously visited channel).
func (m *AlertModel) loadChannelFields() {
	ch, ok := m.savedChannels[m.channelType()]
	if !ok {
		m.primaryBuf = nil
		m.primaryPos = 0
		m.secondaryBuf = nil
		m.secondaryPos = 0
		return
	}
	m.secondaryBuf = nil
	m.secondaryPos = 0
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
			m.resultMsg = G.OK + " Test alert delivered successfully"
			m.resultErr = false
		}
		return m, nil
	}

	switch m.state {
	case alertStateList:
		return m.updateList(msg)
	case alertStateConfirmDelete:
		return m.updateConfirmDeleteChannel(msg)
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

	// ── Keys that always work regardless of focused field ─────────────────
	switch km.String() {
	case "esc":
		// Auto-save whatever is in the text fields before leaving the screen.
		m.autosaveCredentials()
		return m, func() tea.Msg { return AlertScreenDoneMsg{} }

	case "tab":
		max := m.maxFieldCursor()
		if m.fieldCursor < max {
			// Auto-save when leaving a text field so credentials are persisted
			// even if the user never fires a test-send (e.g. while offline).
			if m.fieldCursor == alertFieldPrimary || m.fieldCursor == alertFieldSecondary {
				m.autosaveCredentials()
			}
			m.fieldCursor++
			if m.fieldCursor == alertFieldSecondary && !m.hasSecondaryField() {
				m.fieldCursor++
			}
		}
		return m, nil

	case "shift+tab":
		if m.fieldCursor > 0 {
			// Same auto-save when tabbing backwards out of a text field.
			if m.fieldCursor == alertFieldPrimary || m.fieldCursor == alertFieldSecondary {
				m.autosaveCredentials()
			}
			m.fieldCursor--
			if m.fieldCursor == alertFieldSecondary && !m.hasSecondaryField() {
				m.fieldCursor--
			}
		}
		return m, nil
	}

	// ── Text input fields: all other keys go into the buffer ──────────────
	// Do NOT check j/k/s/down/up shortcuts here — those characters appear
	// in URLs and tokens and would be eaten during a paste operation.
	if m.fieldCursor == alertFieldPrimary || m.fieldCursor == alertFieldSecondary {
		switch km.String() {
		case "enter":
			// Enter advances to the next field. Auto-save on the way out.
			m.autosaveCredentials()
			next := m.fieldCursor + 1
			if next == alertFieldSecondary && !m.hasSecondaryField() {
				next++
			}
			if next <= m.maxFieldCursor() {
				m.fieldCursor = next
			}
		default:
			if m.fieldCursor == alertFieldPrimary {
				m.primaryBuf, m.primaryPos = handleTextInput(m.primaryBuf, m.primaryPos, km)
			} else {
				m.secondaryBuf, m.secondaryPos = handleTextInput(m.secondaryBuf, m.secondaryPos, km)
			}
		}
		return m, nil
	}

	// ── Non-text rows (Channel selector, Send button) ─────────────────────
	switch km.String() {
	case "down", "j":
		max := m.maxFieldCursor()
		if m.fieldCursor < max {
			m.fieldCursor++
			if m.fieldCursor == alertFieldSecondary && !m.hasSecondaryField() {
				m.fieldCursor++
			}
		}

	case "up", "k":
		if m.fieldCursor > 0 {
			m.fieldCursor--
			if m.fieldCursor == alertFieldSecondary && !m.hasSecondaryField() {
				m.fieldCursor--
			}
		}

	case "enter":
		switch m.fieldCursor {
		case alertFieldChannel:
			// Cycle forward through channel types and restore saved credentials
			// for the new type (empty form if never configured in this session).
			m.channelIdx = (m.channelIdx + 1) % len(alert.Channels)
			m.loadChannelFields()
		case alertFieldSend:
			return m.doTestSend()
		}

	case "s":
		// 's' fires test-send only when NOT inside a text field (handled above).
		return m.doTestSend()
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

// autosaveCredentials persists the current form fields to savedChannels and
// disk without firing a test-send. Called on Tab/Enter/Esc so the user's
// credentials survive navigation even if they never trigger a test.
//
// Guards:
//   - primaryBuf must be non-empty after trim (skip empty forms)
//   - No network call is made here
func (m *AlertModel) autosaveCredentials() {
	primary := strings.TrimSpace(string(m.primaryBuf))
	if primary == "" {
		return // nothing worth saving
	}
	cfg := m.buildChannelConfig()
	if m.savedChannels == nil {
		m.savedChannels = make(map[string]alert.ChannelConfig)
	}
	m.savedChannels[cfg.Type] = cfg
	if m.dataDir != "" {
		existing, _ := alert.Load(m.dataDir)
		existing = m.upsertChannel(existing, cfg)
		_ = alert.Save(m.dataDir, existing)
	}
}

// upsertChannel inserts or updates cfg in alertCfg based on editingID.
// When editingID is empty, cfg is appended as a new entry.
// When editingID is set, the existing entry with that ID is replaced.
func (m *AlertModel) upsertChannel(alertCfg alert.AlertConfig, cfg alert.ChannelConfig) alert.AlertConfig {
	if m.editingID == "" {
		// New channel: append.
		alertCfg.Channels = append(alertCfg.Channels, cfg)
		return alertCfg
	}
	// Edit: replace the matching entry, preserve its ID.
	cfg.ID = m.editingID
	for i, ch := range alertCfg.Channels {
		if ch.ID == m.editingID {
			alertCfg.Channels[i] = cfg
			return alertCfg
		}
	}
	// editingID not found (stale): append as new.
	alertCfg.Channels = append(alertCfg.Channels, cfg)
	return alertCfg
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

	// Cache this channel's config so cycling back to it restores credentials.
	if m.savedChannels == nil {
		m.savedChannels = make(map[string]alert.ChannelConfig)
	}
	m.savedChannels[cfg.Type] = cfg

	// Persist to disk so the config survives a restart.
	if m.dataDir != "" {
		existing, _ := alert.Load(m.dataDir)
		existing = m.upsertChannel(existing, cfg)
		_ = alert.Save(m.dataDir, existing)
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
	case alertStateList:
		return m.viewList()
	case alertStateConfirmDelete:
		return m.viewConfirmDeleteChannel()
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
		sb.WriteString(SelectedItemStyle().Render(G.Cursor+" "+sendLabel))
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
		prefix = G.Cursor + " "
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
	content := fmt.Sprintf("%s  Sending test alert%s", frame, G.Ellipsis)
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

// ----------------------------------------------------------------------------
// List state — multi-channel management
// ----------------------------------------------------------------------------

func (m AlertModel) updateList(msg tea.Msg) (tea.Model, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	cfg, _ := alert.Load(m.dataDir)
	channels := cfg.Channels
	switch km.String() {
	case "up", "k":
		if m.listCursor > 0 {
			m.listCursor--
		}
	case "down", "j":
		if m.listCursor < len(channels)-1 {
			m.listCursor++
		}
	case "a":
		// Add new channel — open blank form.
		m.editingID = ""
		m.primaryBuf = nil
		m.primaryPos = 0
		m.secondaryBuf = nil
		m.secondaryPos = 0
		m.channelIdx = 0
		m.fieldCursor = alertFieldChannel
		m.state = alertStateForm
	case "enter":
		// Edit selected channel — pre-populate form.
		if len(channels) > 0 && m.listCursor < len(channels) {
			ch := channels[m.listCursor]
			m.editingID = ch.ID
			// Set channelIdx to match channel type.
			for i, c := range alert.Channels {
				if c.Type == ch.Type {
					m.channelIdx = i
					break
				}
			}
			// Pre-populate fields.
			m.primaryBuf = nil
			m.primaryPos = 0
			m.secondaryBuf = nil
			m.secondaryPos = 0
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
			m.fieldCursor = alertFieldPrimary
			m.state = alertStateForm
		}
	case "d":
		if len(channels) > 0 {
			m.state = alertStateConfirmDelete
		}
	case "s":
		// Set selected as default.
		if len(channels) > 0 && m.listCursor < len(channels) {
			cfg.DefaultID = channels[m.listCursor].ID
			_ = alert.Save(m.dataDir, cfg)
		}
	case "esc":
		return m, func() tea.Msg { return AlertScreenDoneMsg{} }
	}
	return m, nil
}

func (m AlertModel) updateConfirmDeleteChannel(msg tea.Msg) (tea.Model, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch km.String() {
	case "y", "enter":
		cfg, err := alert.Load(m.dataDir)
		if err == nil && m.listCursor < len(cfg.Channels) {
			deletedID := cfg.Channels[m.listCursor].ID
			cfg.Channels = append(cfg.Channels[:m.listCursor], cfg.Channels[m.listCursor+1:]...)
			// If deleted channel was the default, reset to first (Save will fix it).
			if cfg.DefaultID == deletedID {
				cfg.DefaultID = ""
			}
			_ = alert.Save(m.dataDir, cfg)
			if m.listCursor > 0 && m.listCursor >= len(cfg.Channels) {
				m.listCursor = len(cfg.Channels) - 1
			}
		}
		// If no channels left, go back to form.
		cfg2, _ := alert.Load(m.dataDir)
		if len(cfg2.Channels) == 0 {
			m.state = alertStateForm
		} else {
			m.state = alertStateList
		}
	case "n", "esc":
		m.state = alertStateList
	}
	return m, nil
}

func (m AlertModel) viewList() string {
	cfg, _ := alert.Load(m.dataDir)
	channels := cfg.Channels

	var sb strings.Builder
	if len(channels) == 0 {
		sb.WriteString(MutedStyle.Render("  No channels configured. Press 'a' to add one."))
	} else {
		for i, ch := range channels {
			label := ch.Label
			if label == "" {
				label = ch.Type
			}
			suffix := ""
			if ch.ID == cfg.DefaultID {
				suffix += " " + G.Star
			}
			marker := "  "
			if i == m.listCursor {
				marker = G.Cursor + " "
			}
			line := fmt.Sprintf("%s%-20s  %s%s", marker, label, ch.Type, suffix)
			if i == m.listCursor {
				sb.WriteString(SelectedItemStyle().Render(line) + "\n")
			} else {
				sb.WriteString(NormalItemStyle.Render(line) + "\n")
			}
		}
	}

	content := strings.TrimRight(sb.String(), "\n")
	help := HelpTextStyle.Render(G.NavUp+"/"+G.NavDown+" browse   enter edit   a add   d delete   s set default   esc back   "+G.Star+" = default")
	return lipgloss.JoinVertical(lipgloss.Left,
		RenderBox("Alert Channels", content, m.width),
		help,
	)
}

func (m AlertModel) viewConfirmDeleteChannel() string {
	cfg, _ := alert.Load(m.dataDir)
	if m.listCursor >= len(cfg.Channels) {
		m.state = alertStateList
		return m.viewList()
	}
	ch := cfg.Channels[m.listCursor]
	label := ch.Label
	if label == "" {
		label = ch.Type
	}

	var sb strings.Builder
	sb.WriteString(WarningStyle.Render("  Delete this alert channel?") + "\n\n")
	sb.WriteString(MutedStyle.Render("  Label: ") + NormalItemStyle.Render(label) + "\n")
	sb.WriteString(MutedStyle.Render("  Type:  ") + NormalItemStyle.Render(ch.Type) + "\n")
	if ch.ID == cfg.DefaultID {
		sb.WriteString("\n" + MutedStyle.Render("  Note: this is the default channel. Another will be promoted.") + "\n")
	}

	content := strings.TrimRight(sb.String(), "\n")
	help := HelpTextStyle.Render("y/enter confirm   n/esc cancel")
	boxW := m.width - 2
	if boxW < 10 {
		boxW = 10
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		renderBoxInner("Delete Channel", content, boxW, ColorDanger),
		help,
	)
}

