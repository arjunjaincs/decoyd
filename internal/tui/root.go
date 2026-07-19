package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/arjunjaincs/decoyd/internal/store"
	"github.com/arjunjaincs/decoyd/internal/watch"
)


// ----------------------------------------------------------------------------
// Screen enum
// ----------------------------------------------------------------------------

// Screen identifies which TUI screen is currently active.
type Screen int

const (
	ScreenSplash        Screen = iota // First-run welcome
	ScreenMainMenu                    // Main navigation menu
	ScreenGenerate                    // Phase 1: generate a token
	ScreenDeploy                      // Phase 2: deploy a token to disk
	ScreenTokenList                   // Phase 2: list / manage tokens
	ScreenAlertSettings               // Phase 3: alert channel configuration
	ScreenStatus                      // Phase 4: watcher status + trigger dashboard
	ScreenTriggerDetail               // Phase 4: individual trigger event detail
)

// ----------------------------------------------------------------------------
// RootModel
// ----------------------------------------------------------------------------

// RootModel is the top-level bubbletea model. It owns the screen state machine,
// routes messages to the active sub-model, and composites the help overlay.
type RootModel struct {
	// current is the active screen.
	current Screen

	// sub-models
	splash        SplashModel
	mainMenu      MainMenuModel
	generate      GenerateModel
	deploy        DeployModel
	tokenList     TokenListModel
	alertScreen   AlertModel
	statusScreen  StatusModel
	triggerDetail TriggerDetailModel
	help          HelpModel

	// showHelp is true when the help overlay is active.
	showHelp bool

	// width and height track the current terminal dimensions.
	width  int
	height int

	// st is the open token store, shared with sub-models that need persistence.
	st *store.Store

	// dataDir is the OS-specific data directory, passed to sub-models that
	// need to load/save files (alert config, trigger log, snapshot, lock).
	dataDir string

	// watcher is the TUI-embedded watcher, set when the TUI owns the watcher.
	// Nil when headless or when the lock was held by another process.
	watcher *watch.Watcher
}

// NewRootModel creates the root model.
// isFirstRun controls whether to start on the splash screen (true) or the
// main menu (false). st must be an open store (may be nil in tests that do
// not exercise the generate screen). dataDir is the OS data directory used
// by the alert screen to persist alert_config.json.
func NewRootModel(isFirstRun bool, width, height int, st *store.Store, dataDir string) RootModel {
	screen := ScreenMainMenu
	if isFirstRun {
		screen = ScreenSplash
	}

	return RootModel{
		current:      screen,
		splash:       NewSplashModel(width, height),
		mainMenu:     NewMainMenuModel(width, height),
		generate:     NewGenerateModel(width, height, st),
		deploy:       NewDeployModel(width, height, st, dataDir),
		tokenList:    NewTokenListModel(width, height, st, dataDir),
		alertScreen:  NewAlertModel(width, height, dataDir),
		statusScreen: NewStatusModel(width, height, dataDir, nil),
		help:         NewHelpModel(width, height),
		width:        width,
		height:       height,
		st:           st,
		dataDir:      dataDir,
	}
}

// reconcileDoneMsg is the internal result of a background snapshot reconciliation.
// It carries any error for logging but is otherwise discarded — reconciliation
// is best-effort and must never block startup or crash the TUI.
type reconcileDoneMsg struct{ err error }

// reconcileCmd returns a Cmd that rebuilds deployed_tokens.json in the background.
func (m RootModel) reconcileCmd() tea.Cmd {
	return func() tea.Msg {
		return reconcileDoneMsg{err: watch.ReconcileSnapshot(m.st, m.dataDir)}
	}
}

// Init satisfies tea.Model. Delegates to the active sub-model's Init.
// On first call (Splash or MainMenu) it also fires a background snapshot
// reconciliation so the headless watcher sees up-to-date token paths.
func (m RootModel) Init() tea.Cmd {
	switch m.current {
	case ScreenSplash:
		return tea.Batch(m.splash.Init(), m.reconcileCmd())
	case ScreenMainMenu:
		return tea.Batch(m.mainMenu.Init(), m.reconcileCmd())
	case ScreenGenerate:
		return m.generate.Init()
	case ScreenDeploy:
		return m.deploy.Init()
	case ScreenTokenList:
		return m.tokenList.Init()
	case ScreenStatus:
		return m.statusScreen.Init()
	case ScreenTriggerDetail:
		return m.triggerDetail.Init()
	}
	return nil
}

// Update is the central message router for the TUI.
func (m RootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	// ── Terminal resize ──────────────────────────────────────────────────────
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Propagate to all sub-models so they each hold current dimensions.
		m.splash = propagateSize(m.splash, msg)
		m.mainMenu = propagateSize(m.mainMenu, msg)
		m.generate = propagateSize(m.generate, msg)
		m.deploy = propagateSize(m.deploy, msg)
		m.tokenList = propagateSize(m.tokenList, msg)
		m.alertScreen = propagateSize(m.alertScreen, msg)
		m.statusScreen = propagateSize(m.statusScreen, msg)
		m.triggerDetail = propagateSize(m.triggerDetail, msg)
		m.help = propagateSize(m.help, msg)
		return m, nil

	// ── Global quit ──────────────────────────────────────────────────────────
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "q":
			// Quit from main menu; other screens handle q themselves
			// (or ignore it) — root intercepts here only on main menu.
			if m.current == ScreenMainMenu && !m.showHelp {
				return m, tea.Quit
			}
		case "?":
			// Toggle help overlay (available on every screen).
			m.showHelp = !m.showHelp
			return m, nil
		case "esc":
			if m.showHelp {
				m.showHelp = false
				return m, nil
			}
		}

	// ── Splash done ──────────────────────────────────────────────────────────
	case SplashDoneMsg:
		m.current = ScreenMainMenu
		return m, m.mainMenu.Init()

	// ── Main menu action ─────────────────────────────────────────────────────
	case MenuActionMsg:
		switch msg.Index {
		case 0: // Generate a decoy
			// Reset the generate screen so previous selections are cleared.
			m.generate = NewGenerateModel(m.width, m.height, m.st)
			m.current = ScreenGenerate
			return m, m.generate.Init()
		case 1: // Deploy existing decoys
			m.deploy = NewDeployModel(m.width, m.height, m.st, m.dataDir)
			m.current = ScreenDeploy
			return m, m.deploy.Init()
		case 2: // Alert settings (Phase 3)
			m.alertScreen = NewAlertModel(m.width, m.height, m.dataDir)
			m.current = ScreenAlertSettings
			return m, m.alertScreen.Init()
		case 3: // Status / dashboard (Phase 4)
			m.statusScreen = NewStatusModel(m.width, m.height, m.dataDir, m.watcher)
			m.current = ScreenStatus
			return m, m.statusScreen.Init()
		case 4: // Quit
			return m, tea.Quit
		}
		return m, nil

	// ── Generate screen done ─────────────────────────────────────────────────
	case GenScreenDoneMsg:
		m.current = ScreenMainMenu
		return m, m.mainMenu.Init()

	// ── Deploy screen done ───────────────────────────────────────────────────
	case DeployScreenDoneMsg:
		m.current = ScreenMainMenu
		return m, m.mainMenu.Init()

	// ── Token list done ──────────────────────────────────────────────────────
	case TokenListDoneMsg:
		m.current = ScreenMainMenu
		return m, m.mainMenu.Init()

	// ── Alert settings done ───────────────────────────────────────────────────
	case AlertScreenDoneMsg:
		m.current = ScreenMainMenu
		return m, m.mainMenu.Init()

	// ── Status dashboard done ─────────────────────────────────────────────────
	case StatusDoneMsg:
		m.current = ScreenMainMenu
		return m, m.mainMenu.Init()

	// ── Show trigger detail ───────────────────────────────────────────────────
	case ShowTriggerDetailMsg:
		m.triggerDetail = NewTriggerDetailModel(m.width, m.height, msg.Event)
		m.current = ScreenTriggerDetail
		return m, m.triggerDetail.Init()

	// ── Trigger detail done ───────────────────────────────────────────────────
	case TriggerDetailDoneMsg:
		m.current = ScreenStatus
		return m, m.statusScreen.Init()

	// ── Help hide ─────────────────────────────────────────────────────────────
	case HideHelpMsg:
		m.showHelp = false
		return m, nil

	// ── Snapshot reconciliation (background, best-effort) ──────────────────
	case reconcileDoneMsg:
		// Errors are silently dropped — reconciliation is non-fatal.
		// A future enhancement could surface persistent failures in the status screen.
		return m, nil
	}

	// ── Delegate to active sub-model ─────────────────────────────────────────
	// Only delegate if the help overlay is NOT showing (it captures input when open).
	if m.showHelp {
		newHelp, cmd := m.help.Update(msg)
		m.help = newHelp.(HelpModel)
		return m, cmd
	}

	switch m.current {
	case ScreenSplash:
		newSplash, cmd := m.splash.Update(msg)
		m.splash = newSplash.(SplashModel)
		return m, cmd

	case ScreenMainMenu:
		newMenu, cmd := m.mainMenu.Update(msg)
		m.mainMenu = newMenu.(MainMenuModel)
		return m, cmd

	case ScreenGenerate:
		newGen, cmd := m.generate.Update(msg)
		m.generate = newGen.(GenerateModel)
		return m, cmd

	case ScreenDeploy:
		newDeploy, cmd := m.deploy.Update(msg)
		m.deploy = newDeploy.(DeployModel)
		return m, cmd

	case ScreenTokenList:
		newList, cmd := m.tokenList.Update(msg)
		m.tokenList = newList.(TokenListModel)
		return m, cmd

	case ScreenAlertSettings:
		newAlert, cmd := m.alertScreen.Update(msg)
		m.alertScreen = newAlert.(AlertModel)
		return m, cmd

	case ScreenStatus:
		newStatus, cmd := m.statusScreen.Update(msg)
		m.statusScreen = newStatus.(StatusModel)
		return m, cmd

	case ScreenTriggerDetail:
		newDetail, cmd := m.triggerDetail.Update(msg)
		m.triggerDetail = newDetail.(TriggerDetailModel)
		return m, cmd
	}

	return m, nil
}

// View renders the current screen, compositing the help overlay on top when active.
func (m RootModel) View() string {
	// Narrow terminal guard — applies globally.
	if m.width > 0 && m.width < MinTermWidth {
		return WarningStyle.Render(NarrowTermMsg)
	}

	// Render the base screen.
	var base string
	switch m.current {
	case ScreenSplash:
		base = m.splash.View()
	case ScreenMainMenu:
		base = m.mainMenu.View()
	case ScreenGenerate:
		base = m.generate.View()
	case ScreenDeploy:
		base = m.deploy.View()
	case ScreenTokenList:
		base = m.tokenList.View()
	case ScreenAlertSettings:
		base = m.alertScreen.View()
	case ScreenStatus:
		base = m.statusScreen.View()
	case ScreenTriggerDetail:
		base = m.triggerDetail.View()
	default:
		base = ""
	}

	if !m.showHelp {
		return base
	}

	// Composite: render the help overlay centered over the base view.
	overlay := m.help.View()

	// Place the overlay centered on screen.
	// WithWhitespaceBackground dims the backdrop to ColorBackground.
	return lipgloss.Place(
		m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		overlay,
		lipgloss.WithWhitespaceBackground(ColorBackground),
	)
}

// ----------------------------------------------------------------------------
// propagateSize helpers
// ----------------------------------------------------------------------------

// propagateSize sends a WindowSizeMsg to sub-models by re-running their Update.
// Each sub-model holds its own width/height for self-contained rendering.
func propagateSize[T tea.Model](model T, msg tea.WindowSizeMsg) T {
	updated, _ := model.Update(msg)
	return updated.(T)
}
