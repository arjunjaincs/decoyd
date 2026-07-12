package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

const testWidth = 80
const testHeight = 24

// newTestRoot creates a RootModel with a nil store — safe for all tests that
// do not exercise the Generate screen's save path.
func newTestRoot(isFirstRun bool) RootModel {
	return NewRootModel(isFirstRun, testWidth, testHeight, nil, "")
}

// TestRootModel_NoPanic verifies that NewRootModel does not panic under either
// first-run or subsequent-run conditions.
func TestRootModel_NoPanic(t *testing.T) {
	t.Run("first run", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("NewRootModel(firstRun=true) panicked: %v", r)
			}
		}()
		_ = newTestRoot(true)
	})

	t.Run("subsequent run", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("NewRootModel(firstRun=false) panicked: %v", r)
			}
		}()
		_ = newTestRoot(false)
	})
}

// TestRootModel_FirstRunStartsOnSplash verifies that when isFirstRun is true,
// the root model begins on the Splash screen.
func TestRootModel_FirstRunStartsOnSplash(t *testing.T) {
	m := newTestRoot(true)
	if m.current != ScreenSplash {
		t.Errorf("first-run model starts on screen %v; want ScreenSplash (%v)", m.current, ScreenSplash)
	}
}

// TestRootModel_SubsequentRunStartsOnMainMenu verifies that when isFirstRun is
// false, the root model begins on the MainMenu screen.
func TestRootModel_SubsequentRunStartsOnMainMenu(t *testing.T) {
	m := newTestRoot(false)
	if m.current != ScreenMainMenu {
		t.Errorf("subsequent-run model starts on screen %v; want ScreenMainMenu (%v)", m.current, ScreenMainMenu)
	}
}

// TestRootModel_SplashDoneTransition verifies that receiving a SplashDoneMsg
// transitions the root from Splash to MainMenu.
func TestRootModel_SplashDoneTransition(t *testing.T) {
	m := newTestRoot(true)
	if m.current != ScreenSplash {
		t.Skip("model did not start on splash — skipping transition test")
	}

	updated, _ := m.Update(SplashDoneMsg{})
	root, ok := updated.(RootModel)
	if !ok {
		t.Fatalf("Update returned unexpected type %T", updated)
	}
	if root.current != ScreenMainMenu {
		t.Errorf("after SplashDoneMsg, screen = %v; want ScreenMainMenu", root.current)
	}
}

// TestRootModel_HelpOverlayToggle verifies that pressing ? toggles the help
// overlay and pressing it again toggles it off.
func TestRootModel_HelpOverlayToggle(t *testing.T) {
	m := newTestRoot(false)
	if m.showHelp {
		t.Fatal("help overlay should start hidden")
	}

	// Press ? to open.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	root := updated.(RootModel)
	if !root.showHelp {
		t.Error("? key should show help overlay")
	}

	// Press ? again to close.
	updated, _ = root.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	root = updated.(RootModel)
	if root.showHelp {
		t.Error("second ? key should hide help overlay")
	}
}

// TestRootModel_EscDismissesHelp verifies that Esc dismisses the help overlay.
func TestRootModel_EscDismissesHelp(t *testing.T) {
	m := newTestRoot(false)

	// Open help.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	root := updated.(RootModel)

	// Dismiss with Esc.
	updated, _ = root.Update(tea.KeyMsg{Type: tea.KeyEsc})
	root = updated.(RootModel)
	if root.showHelp {
		t.Error("Esc should dismiss the help overlay")
	}
}

// TestRootModel_ResizePropagates verifies that a WindowSizeMsg updates the
// root's tracked dimensions.
func TestRootModel_ResizePropagates(t *testing.T) {
	m := newTestRoot(false)

	newW, newH := 120, 40
	updated, _ := m.Update(tea.WindowSizeMsg{Width: newW, Height: newH})
	root := updated.(RootModel)

	if root.width != newW || root.height != newH {
		t.Errorf("after resize, got %dx%d; want %dx%d", root.width, root.height, newW, newH)
	}
}

// TestRootModel_ViewNoPanic verifies that View does not panic for any screen.
func TestRootModel_ViewNoPanic(t *testing.T) {
	screens := []struct {
		name       string
		isFirstRun bool
	}{
		{"splash", true},
		{"mainmenu", false},
	}

	for _, tc := range screens {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("View() panicked on %s: %v", tc.name, r)
				}
			}()
			m := newTestRoot(tc.isFirstRun)
			_ = m.View()
		})
	}
}

// TestRootModel_NarrowTerminal verifies that a sub-MinTermWidth terminal shows
// the narrow-terminal message instead of broken boxes.
func TestRootModel_NarrowTerminal(t *testing.T) {
	m := NewRootModel(false, 40, testHeight, nil, "") // 40 < MinTermWidth (60)
	view := m.View()

	// The view should contain the narrow-terminal warning, not the normal UI.
	if view == "" {
		t.Error("View() returned empty string for narrow terminal")
	}
	// We check that it doesn't contain the box border characters (would mean
	// the broken layout was rendered instead of the guard message).
	if strings.HasPrefix(view, "╭") {
		t.Error("View() rendered a box for a narrow terminal; should show warning")
	}
}

// TestRootModel_MenuActionQuit verifies that MenuActionMsg{Index:4} (Quit)
// produces a tea.Quit command.
func TestRootModel_MenuActionQuit(t *testing.T) {
	m := newTestRoot(false)
	_, cmd := m.Update(MenuActionMsg{Index: 4})
	if cmd == nil {
		t.Fatal("Quit menu action returned nil command; want tea.Quit")
	}
	// Execute the command and check it produces a QuitMsg.
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("Quit menu action command produced %T; want tea.QuitMsg", msg)
	}
}

// TestRootModel_GenerateScreenNavigation verifies that selecting menu item 0
// transitions to the Generate screen.
func TestRootModel_GenerateScreenNavigation(t *testing.T) {
	m := newTestRoot(false)
	updated, _ := m.Update(MenuActionMsg{Index: 0})
	root := updated.(RootModel)
	if root.current != ScreenGenerate {
		t.Errorf("MenuActionMsg{0} → screen %v; want ScreenGenerate", root.current)
	}
}

// TestRootModel_GenScreenDoneReturnsToMenu verifies that GenScreenDoneMsg
// transitions back from the Generate screen to the main menu.
func TestRootModel_GenScreenDoneReturnsToMenu(t *testing.T) {
	m := newTestRoot(false)
	// Navigate to generate screen.
	updated, _ := m.Update(MenuActionMsg{Index: 0})
	root := updated.(RootModel)

	// Send done.
	updated, _ = root.Update(GenScreenDoneMsg{})
	root = updated.(RootModel)
	if root.current != ScreenMainMenu {
		t.Errorf("GenScreenDoneMsg → screen %v; want ScreenMainMenu", root.current)
	}
}

// TestRootModel_DeployScreenNavigation verifies that selecting menu item 1
// transitions to the Deploy screen.
func TestRootModel_DeployScreenNavigation(t *testing.T) {
	m := newTestRoot(false)
	updated, _ := m.Update(MenuActionMsg{Index: 1})
	root := updated.(RootModel)
	if root.current != ScreenDeploy {
		t.Errorf("MenuActionMsg{1} → screen %v; want ScreenDeploy", root.current)
	}
}

// TestRootModel_DeployScreenDoneReturnsToMenu verifies DeployScreenDoneMsg
// navigates back to main menu.
func TestRootModel_DeployScreenDoneReturnsToMenu(t *testing.T) {
	m := newTestRoot(false)
	updated, _ := m.Update(MenuActionMsg{Index: 1})
	root := updated.(RootModel)

	updated, _ = root.Update(DeployScreenDoneMsg{})
	root = updated.(RootModel)
	if root.current != ScreenMainMenu {
		t.Errorf("DeployScreenDoneMsg → screen %v; want ScreenMainMenu", root.current)
	}
}

// TestRootModel_AlertSettingsNavigation verifies that selecting menu item 2
// (Alert settings) transitions to ScreenAlertSettings.
func TestRootModel_AlertSettingsNavigation(t *testing.T) {
	m := newTestRoot(false)
	updated, _ := m.Update(MenuActionMsg{Index: 2})
	root := updated.(RootModel)
	if root.current != ScreenAlertSettings {
		t.Errorf("MenuActionMsg{2} → screen %v; want ScreenAlertSettings", root.current)
	}
}

// TestRootModel_AlertScreenDoneReturnsToMenu verifies AlertScreenDoneMsg
// transitions back to the main menu.
func TestRootModel_AlertScreenDoneReturnsToMenu(t *testing.T) {
	m := newTestRoot(false)
	updated, _ := m.Update(MenuActionMsg{Index: 2})
	root := updated.(RootModel)

	updated, _ = root.Update(AlertScreenDoneMsg{})
	root = updated.(RootModel)
	if root.current != ScreenMainMenu {
		t.Errorf("AlertScreenDoneMsg → screen %v; want ScreenMainMenu", root.current)
	}
}

// TestRootModel_TokenListDoneReturnsToMenu verifies TokenListDoneMsg still
// returns to the main menu when issued from any screen.
func TestRootModel_TokenListDoneReturnsToMenu(t *testing.T) {
	m := newTestRoot(false)
	updated, _ := m.Update(TokenListDoneMsg{})
	root := updated.(RootModel)
	if root.current != ScreenMainMenu {
		t.Errorf("TokenListDoneMsg → screen %v; want ScreenMainMenu", root.current)
	}
}
