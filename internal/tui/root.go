package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/arjunjaincs/decoyd/internal/store"
)

// ----------------------------------------------------------------------------
// Screen enum
// ----------------------------------------------------------------------------

// Screen identifies which TUI screen is currently active.
type Screen int

const (
	ScreenSplash   Screen = iota // First-run welcome
	ScreenMainMenu               // Main navigation menu
	ScreenGenerate               // Phase 1: generate a token
	// Future screens (Phases 2–5) will be added here.
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
	splash   SplashModel
	mainMenu MainMenuModel
	generate GenerateModel
	help     HelpModel

	// showHelp is true when the help overlay is active.
	showHelp bool

	// width and height track the current terminal dimensions.
	width  int
	height int

	// st is the open token store, shared with sub-models that need persistence.
	st *store.Store
}

// NewRootModel creates the root model.
// isFirstRun controls whether to start on the splash screen (true) or the
// main menu (false).  st must be an open store (may be nil in tests that do
// not exercise the generate screen).
func NewRootModel(isFirstRun bool, width, height int, st *store.Store) RootModel {
	screen := ScreenMainMenu
	if isFirstRun {
		screen = ScreenSplash
	}

	return RootModel{
		current:  screen,
		splash:   NewSplashModel(width, height),
		mainMenu: NewMainMenuModel(width, height),
		generate: NewGenerateModel(width, height, st),
		help:     NewHelpModel(width, height),
		width:    width,
		height:   height,
		st:       st,
	}
}

// Init satisfies tea.Model. Delegates to the active sub-model's Init.
func (m RootModel) Init() tea.Cmd {
	switch m.current {
	case ScreenSplash:
		return m.splash.Init()
	case ScreenMainMenu:
		return m.mainMenu.Init()
	case ScreenGenerate:
		return m.generate.Init()
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
		case 4: // Quit
			return m, tea.Quit
		// Indices 1–3 will be routed to Phase 2–4 screens; no-op for now.
		}
		return m, nil

	// ── Generate screen done ─────────────────────────────────────────────────
	case GenScreenDoneMsg:
		m.current = ScreenMainMenu
		return m, m.mainMenu.Init()

	// ── Help hide ────────────────────────────────────────────────────────────
	case HideHelpMsg:
		m.showHelp = false
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
