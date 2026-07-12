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
	alertStateForm                            // editing / configuring one channel
	alertStateSending                         // test-send in progress
	alertStateDone                            // showing result (success or error)
	alertStateConfirmDelete                   // confirm deletion of a channel
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

	// -- Channel list (alertStateList) ----------------------------------------
	// channelList is the full list of saved channels, mirroring alert_config.json.
	channelList []alert.ChannelConfig
	// listCursor is the selected row in the channel list view.
	listCursor int
	// defaultIdx is the DefaultIndex of the saved AlertConfig.
	defaultIdx int

	// -- Form editing (alertStateForm) ----------------------------------------
	// editingID is the ID of the channel being edited. Empty = new channel.
	editingID  string
	channelIdx int // index into alert.Channels (type selector)

	// savedChannels caches the last-used ChannelConfig for each channel type
	// within this session. Keyed by channel TYPE (for cycling the type selector).
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
		state:         alertStateForm, // overridden below if channels exist
		width:         width,
		height:        height,
		dataDir:       dataDir,
		savedChannels: make(map[string]alert.ChannelConfig),
	}
	// Pre-populate from saved config if available.
	cfg, err := alert.Load(dataDir)
	if err == nil {
		m.channelList = cfg.Channels
		m.defaultIdx = cfg.DefaultIndex
		for _, ch := range cfg.Channels {
			m.savedChannels[ch.Type] = ch
		}
		if len(cfg.Channels) > 0 {
			// Start on the channel list when channels already exist.
			m.state = alertStateList
			// Pre-load the default channel into the form fields (for the
			// case where the user immediately presses 'n' to add a new one).
			ch := cfg.Channels[cfg.DefaultIndex]
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

// buildChannelConfig builds a ChannelConfig from current form state,
// preserving the editingID so existing channels are updated, not duplicated.
func (m *AlertModel) buildChannelConfig() alert.ChannelConfig {
	t := m.channelType()
	primary := string(m.primaryBuf)
	secondary := string(m.secondaryBuf)
	var cfg alert.ChannelConfig
	switch t {
	case alert.ChannelTelegram:
		cfg = alert.ChannelConfig{Type: t, BotToken: primary, ChatID: secondary}
	case alert.ChannelNtfy:
		cfg = alert.ChannelConfig{Type: t, ServerURL: primary, Topic: secondary}
	default:
		cfg = alert.ChannelConfig{Type: t, WebhookURL: primary}
	}
	cfg.ID = m.editingID // empty = new channel, Save() will assign an ID
	return cfg
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
	case alertStateList:
		return m.updateChannelList(msg)
	case alertStateForm:
		return m.updateForm(msg)
	case alertStateDone:
		return m.updateDone(msg)
	case alertStateConfirmDelete:
		return m.updateConfirmDeleteChannel(msg)
	}
	return m, nil
}

// updateChannelList handles input on the channel list screen.
func (m AlertModel) updateChannelList(msg tea.Msg) (AlertModel, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch km.String() {
	case "esc":
		return m, func() tea.Msg { return AlertScreenDoneMsg{} }
	case "up", "k":
		if m.listCursor > 0 {
			m.listCursor--
		}
	case "down", "j":
		if m.listCursor < len(m.channelList) { // +1 for "Add new"
			m.listCursor++
		}
	case "enter":
		if m.listCursor == len(m.channelList) {
			// "Add new channel" row selected.
			m.editingID = ""
			m.primaryBuf = nil
			m.primaryPos = 0
			m.secondaryBuf = nil
			m.secondaryPos = 0
			m.channelIdx = 0
			m.fieldCursor = alertFieldChannel
			m.state = alertStateForm
		} else if m.listCursor < len(m.channelList) {
			// Edit existing channel.
			ch := m.channelList[m.listCursor]
			m.editingID = ch.ID
			// Set channel type selector.
			for i, c := range alert.Channels {
				if c.Type == ch.Type {
					m.channelIdx = i
					break
				}
			}
			// Populate form fields from this channel's config.
			m.loadChannelFromConfig(ch)
			m.fieldCursor = alertFieldPrimary
			m.state = alertStateForm
		}
	case "n":
		// Shortcut: add new channel.
		m.editingID = ""
		m.primaryBuf = nil
		m.primaryPos = 0
		m.secondaryBuf = nil
		m.secondaryPos = 0
		m.channelIdx = 0
		m.fieldCursor = alertFieldChannel
		m.state = alertStateForm
	case "d":
		if len(m.channelList) > 0 && m.listCursor < len(m.channelList) {
			m.state = alertStateConfirmDelete
		}
	case "*":
		// Set selected channel as default.
		if m.listCursor < len(m.channelList) {
			m.defaultIdx = m.listCursor
			m.saveChannelList()
		}
	}
	return m, nil
}

// updateConfirmDeleteChannel handles y/n for channel deletion.
func (m AlertModel) updateConfirmDeleteChannel(msg tea.Msg) (AlertModel, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch km.String() {
	case "y", "enter":
		if m.listCursor < len(m.channelList) {
			m.channelList = append(m.channelList[:m.listCursor], m.channelList[m.listCursor+1:]...)
			// Clamp defaultIdx if it now points past the end.
			if m.defaultIdx >= len(m.channelList) && len(m.channelList) > 0 {
				m.defaultIdx = len(m.channelList) - 1
			} else if len(m.channelList) == 0 {
				m.defaultIdx = 0
			}
			if m.listCursor >= len(m.channelList) && m.listCursor > 0 {
				m.listCursor--
			}
			m.saveChannelList()
		}
		m.state = alertStateList
	case "n", "esc":
		m.state = alertStateList
	}
	return m, nil
}

// loadChannelFromConfig populates primaryBuf/secondaryBuf from ch.
func (m *AlertModel) loadChannelFromConfig(ch alert.ChannelConfig) {
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

// saveChannelList persists the current channelList and defaultIdx to disk.
func (m *AlertModel) saveChannelList() {
	if m.dataDir == "" {
		return
	}
	cfg := alert.AlertConfig{
		Channels:     m.channelList,
		DefaultIndex: m.defaultIdx,
	}
	_ = alert.Save(m.dataDir, cfg)
	// Reload list in case Save() backfilled any IDs.
	if reloaded, err := alert.Load(m.dataDir); err == nil {
		m.channelList = reloaded.Channels
	}
}

// upsertChannelInList adds ch to channelList or updates the entry with
// matching ID. Returns the (possibly ID-assigned) channel.
func (m *AlertModel) upsertChannelInList(ch alert.ChannelConfig) alert.ChannelConfig {
	if ch.ID != "" {
		for i := range m.channelList {
			if m.channelList[i].ID == ch.ID {
				m.channelList[i] = ch
				return ch
			}
		}
	}
	// New channel: append and let Save() assign an ID.
	m.channelList = append(m.channelList, ch)
	m.saveChannelList() // assigns IDs via Save()
	// Retrieve the ID that was assigned.
	if len(m.channelList) > 0 {
		return m.channelList[len(m.channelList)-1]
	}
	return ch
}


func (m AlertModel) updateForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	// ── Keys that always work regardless of focused field ─────────────────
	switch km.String() {
	case "esc":
		// Auto-save before leaving. If there are saved channels, go back to
		// the list; otherwise exit the screen entirely.
		m.autosaveCredentials()
		if len(m.channelList) > 0 {
			m.state = alertStateList
			return m, nil
		}
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

// autosaveCredentials persists the current form fields to the channel list and
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
	saved := m.upsertChannelInList(cfg)
	m.editingID = saved.ID // track so next save updates same channel
	m.saveChannelList()
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

	// Persist to disk: upsert this channel by ID into the full channel list.
	saved := m.upsertChannelInList(cfg)
	m.editingID = saved.ID // track so next save updates same channel
	m.saveChannelList()

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
		return m.viewChannelList()
	case alertStateSending:
		return m.viewSending()
	case alertStateDone:
		return m.viewDone()
	case alertStateConfirmDelete:
		return m.viewConfirmDeleteChannel()
	default:
		return m.viewForm()
	}
}

// viewChannelList renders the multi-channel list (alertStateList).
func (m AlertModel) viewChannelList() string {
	var sb strings.Builder
	sb.WriteString("\n")
	for i, ch := range m.channelList {
		cursor := "  "
		row := NormalItemStyle
		if i == m.listCursor {
			cursor = SelectedItemStyle().Render("▸") + " "
			row = SelectedItemStyle()
		}
		// Channel type label.
		typeLabel := ch.Type
		for _, c := range alert.Channels {
			if c.Type == ch.Type {
				typeLabel = c.Label
				break
			}
		}
		// Masked credential hint.
		hint := ""
		switch ch.Type {
		case alert.ChannelTelegram:
			hint = alert.MaskSecret(ch.BotToken)
		case alert.ChannelNtfy:
			hint = ch.Topic
		default:
			hint = alert.MaskSecret(ch.WebhookURL)
		}
		defaultMark := ""
		if i == m.defaultIdx {
			defaultMark = MutedStyle.Render(" ★")
		}
		line := fmt.Sprintf("%-26s %s", typeLabel, hint)
		sb.WriteString(cursor + row.Render(line) + defaultMark + "\n")
	}
	// "Add new" row.
	addCursor := "  "
	addStyle := MutedStyle
	if m.listCursor == len(m.channelList) {
		addCursor = SelectedItemStyle().Render("▸") + " "
		addStyle = SelectedItemStyle()
	}
	sb.WriteString(addCursor + addStyle.Render("+ Add new channel") + "\n")

	content := sb.String()
	help := HelpTextStyle.Render("↑/↓ browse   enter edit   n add new   d delete   * set default   esc back")
	return lipgloss.JoinVertical(lipgloss.Left,
		RenderBox("Alert Settings", content, m.width),
		help,
	)
}

// viewConfirmDeleteChannel renders the y/n delete confirmation overlay.
func (m AlertModel) viewConfirmDeleteChannel() string {
	var sb strings.Builder
	if m.listCursor < len(m.channelList) {
		ch := m.channelList[m.listCursor]
		typeLabel := ch.Type
		for _, c := range alert.Channels {
			if c.Type == ch.Type {
				typeLabel = c.Label
				break
			}
		}
		sb.WriteString(fmt.Sprintf("\n  Delete %s?\n\n", typeLabel))
		if m.listCursor == m.defaultIdx {
			sb.WriteString(MutedStyle.Render("  ⚠  This is your default channel.\n\n"))
		}
	}
	sb.WriteString("  ") 
	sb.WriteString(lipgloss.NewStyle().Foreground(ColorDanger).Render("y"))
	sb.WriteString(MutedStyle.Render(" confirm   "))
	sb.WriteString(MutedStyle.Render("n / esc cancel"))
	sb.WriteString("\n")
	return RenderBox("Delete Channel", sb.String(), m.width)
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
