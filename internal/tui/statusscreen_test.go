// statusscreen_test.go — tests for StatusModel and TriggerDetailModel routing.
package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/arjunjaincs/decoyd/internal/triglog"
	"github.com/arjunjaincs/decoyd/internal/watch"
)

// ----------------------------------------------------------------------------
// Root routing tests
// ----------------------------------------------------------------------------

// TestMenuAction3_RoutesToScreenStatus verifies that MenuActionMsg{Index: 3}
// sets current screen to ScreenStatus (the Phase 4 placeholder was live since
// Phase 0 but unrouted; this is the first real implementation).
func TestMenuAction3_RoutesToScreenStatus(t *testing.T) {
	root := newTestRoot(false)
	updated, _ := root.Update(MenuActionMsg{Index: 3})
	m := updated.(RootModel)
	if m.current != ScreenStatus {
		t.Errorf("MenuActionMsg{3} → screen %v; want ScreenStatus", m.current)
	}
}

// TestStatusDoneMsg_ReturnsToMenu verifies that StatusDoneMsg navigates back to ScreenMainMenu.
func TestStatusDoneMsg_ReturnsToMenu(t *testing.T) {
	root := newTestRoot(false)
	// Navigate to status first.
	updated, _ := root.Update(MenuActionMsg{Index: 3})
	root = updated.(RootModel)
	if root.current != ScreenStatus {
		t.Fatalf("precondition: not on ScreenStatus")
	}

	updated2, _ := root.Update(StatusDoneMsg{})
	m := updated2.(RootModel)
	if m.current != ScreenMainMenu {
		t.Errorf("StatusDoneMsg → screen %v; want ScreenMainMenu", m.current)
	}
}

// TestShowTriggerDetailMsg_RoutesToDetail verifies that ShowTriggerDetailMsg
// navigates to ScreenTriggerDetail and stores the event.
func TestShowTriggerDetailMsg_RoutesToDetail(t *testing.T) {
	root := newTestRoot(false)
	updated, _ := root.Update(MenuActionMsg{Index: 3})
	root = updated.(RootModel)

	ev := triglog.TriggerEvent{
		ID:          "abc12345",
		TokenID:     "deadbeef00000000",
		TokenType:   "aws_credentials",
		Path:        "/home/user/.aws/credentials",
		TriggeredAt: time.Now(),
		EventType:   "access",
		Status:      triglog.TriggerSent,
	}
	updated2, _ := root.Update(ShowTriggerDetailMsg{Event: ev})
	m := updated2.(RootModel)
	if m.current != ScreenTriggerDetail {
		t.Errorf("ShowTriggerDetailMsg → screen %v; want ScreenTriggerDetail", m.current)
	}
	if m.triggerDetail.event.ID != ev.ID {
		t.Errorf("triggerDetail.event.ID = %q; want %q", m.triggerDetail.event.ID, ev.ID)
	}
}

// TestTriggerDetailDoneMsg_ReturnsToStatus verifies that TriggerDetailDoneMsg
// navigates back to ScreenStatus (not ScreenMainMenu).
func TestTriggerDetailDoneMsg_ReturnsToStatus(t *testing.T) {
	root := newTestRoot(false)
	updated, _ := root.Update(MenuActionMsg{Index: 3})
	root = updated.(RootModel)
	ev := triglog.TriggerEvent{ID: "ev01", TokenID: "tok01", TriggeredAt: time.Now()}
	updated2, _ := root.Update(ShowTriggerDetailMsg{Event: ev})
	root = updated2.(RootModel)
	if root.current != ScreenTriggerDetail {
		t.Fatalf("precondition: not on ScreenTriggerDetail")
	}

	updated3, _ := root.Update(TriggerDetailDoneMsg{})
	m := updated3.(RootModel)
	if m.current != ScreenStatus {
		t.Errorf("TriggerDetailDoneMsg → screen %v; want ScreenStatus", m.current)
	}
}

// TestStatusModel_EscEmitsStatusDoneMsg verifies that pressing esc on the
// status screen emits StatusDoneMsg (so root can navigate back to menu).
func TestStatusModel_EscEmitsStatusDoneMsg(t *testing.T) {
	dir := t.TempDir()
	m := NewStatusModel(testWidth, testHeight, dir, nil)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc returned nil cmd; expected StatusDoneMsg cmd")
	}
	msg := cmd()
	if _, ok := msg.(StatusDoneMsg); !ok {
		t.Errorf("esc cmd returned %T; want StatusDoneMsg", msg)
	}
}

// TestTriggerDetailModel_EscEmitsDoneMsg verifies that pressing esc on the
// detail screen emits TriggerDetailDoneMsg.
func TestTriggerDetailModel_EscEmitsDoneMsg(t *testing.T) {
	ev := triglog.TriggerEvent{ID: "x", TokenID: "y", TriggeredAt: time.Now()}
	m := NewTriggerDetailModel(testWidth, testHeight, ev)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc returned nil cmd; expected TriggerDetailDoneMsg cmd")
	}
	msg := cmd()
	if _, ok := msg.(TriggerDetailDoneMsg); !ok {
		t.Errorf("esc cmd returned %T; want TriggerDetailDoneMsg", msg)
	}
}

// ----------------------------------------------------------------------------
// Watcher-state display tests
// ----------------------------------------------------------------------------

// TestStatusModel_WatcherStateNotRunning verifies that the status screen
// shows "not running" when no watcher.pid file exists (HeadlessNotRunning).
func TestStatusModel_WatcherStateNotRunning(t *testing.T) {
	dir := t.TempDir()
	m := NewStatusModel(testWidth, testHeight, dir, nil)
	view := m.View()
	if !strings.Contains(view, "not running") {
		t.Errorf("expected 'not running' in view when no watcher.pid; got:\n%s", view)
	}
}

// TestStatusModel_WatcherStateHeadlessRunning verifies that the status screen
// shows "running (headless" when watcher.pid contains the current process's
// own PID (a reliably live process).
func TestStatusModel_WatcherStateHeadlessRunning(t *testing.T) {
	dir := t.TempDir()
	pid := os.Getpid()

	// Write our own PID to watcher.pid to simulate a live headless watcher.
	pidPath := filepath.Join(dir, "watcher.pid")
	content := fmt.Sprintf("%d\n", pid)
	if err := os.WriteFile(pidPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	m := NewStatusModel(testWidth, testHeight, dir, nil)
	view := m.View()
	// HeadlessRunning: should show "running (headless"
	if !strings.Contains(view, "running (headless") {
		t.Errorf("expected 'running (headless' in view with live pid; got:\n%s", view)
	}
}

// TestStatusModel_WatcherStateTUIEmbedded verifies that the status screen shows
// "running (TUI-embedded" when WatcherRef is non-nil and its Status() is Running.
// We use a real Watcher started against a temp dir so Status().Running is true.
func TestStatusModel_WatcherStateTUIEmbedded(t *testing.T) {
	dir := t.TempDir()

	// Write empty snapshot so loadTokens() doesn't error.
	if err := watch.WriteDeployedSnapshot(dir, nil); err != nil {
		t.Fatal(err)
	}

	w, err := watch.New(nil, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer w.Stop()

	m := NewStatusModel(testWidth, testHeight, dir, w)
	view := m.View()
	if !strings.Contains(view, "running (TUI-embedded)") {
		t.Errorf("expected 'running (TUI-embedded)' in view; got:\n%s", view)
	}
}

// TestStatusModel_WatcherStateStale verifies that the status screen shows
// "stale lock" when watcher.pid exists with a guaranteed-dead PID.
func TestStatusModel_WatcherStateStale(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "watcher.pid")
	// PID 2147483647 is virtually guaranteed to not exist.
	if err := os.WriteFile(pidPath, []byte("2147483647\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Only run if the stale PID is actually dead on this machine.
	state, _ := watch.HeadlessWatcherState(dir)
	if state != watch.HeadlessStale {
		t.Skip("PID 2147483647 appears alive on this machine — skipping stale test")
	}

	m := NewStatusModel(testWidth, testHeight, dir, nil)
	view := m.View()
	if !strings.Contains(view, "stale lock") {
		t.Errorf("expected 'stale lock' in view; got:\n%s", view)
	}
}

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

// formatInt converts an int to its decimal string (stdlib-free, for test use).
func formatInt(n int) string {
	return fmt.Sprintf("%d", n)
}
