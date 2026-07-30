// tokenlist_purge_test.go — tests for the two-step delete/purge flow in TokenListModel.
package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/arjunjaincs/decoyd/internal/tokens"
)

// newTokenListWithTokens returns a TokenListModel pre-loaded with synthetic
// tokens and no real store (st is nil).  This exercises the state-machine
// paths that do not need actual bbolt persistence.
func newTokenListWithTokens(toks []tokens.Token) TokenListModel {
	m := NewTokenListModel(testWidth, testHeight, nil, "")
	m.all = toks
	return m
}

// sendKey is a helper that sends a single rune key to a model and returns the
// updated TokenListModel.
func sendKey(m TokenListModel, key string) TokenListModel {
	var msg tea.Msg
	switch key {
	case "esc":
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	case "enter":
		msg = tea.KeyMsg{Type: tea.KeyEnter}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
	updated, _ := m.Update(msg)
	return updated.(TokenListModel)
}

// -------------------------------------------------------------------
// First confirmation screen (confDel)
// -------------------------------------------------------------------

// TestTokenList_DKeyEntersConfirmDelete verifies that 'd' on a non-empty list
// enters tokenListStateConfDel.
func TestTokenList_DKeyEntersConfirmDelete(t *testing.T) {
	m := newTokenListWithTokens([]tokens.Token{
		{ID: "t1", Type: tokens.TypeAWSCredentials},
	})
	m = sendKey(m, "d")
	if m.state != tokenListStateConfDel {
		t.Errorf("'d' → state %v; want tokenListStateConfDel", m.state)
	}
}

// TestTokenList_DKeyNoOpWhenEmpty verifies that 'd' on an empty list does not
// transition to confDel.
func TestTokenList_DKeyNoOpWhenEmpty(t *testing.T) {
	m := newTokenListWithTokens(nil)
	m = sendKey(m, "d")
	if m.state != tokenListStateBrowse {
		t.Errorf("'d' on empty list → state %v; want tokenListStateBrowse", m.state)
	}
}

// TestTokenList_ConfirmDelete_EscCancels verifies esc returns to browse.
func TestTokenList_ConfirmDelete_EscCancels(t *testing.T) {
	m := newTokenListWithTokens([]tokens.Token{
		{ID: "t2", Type: tokens.TypeGitHubPAT},
	})
	m = sendKey(m, "d")
	if m.state != tokenListStateConfDel {
		t.Fatalf("precondition: state = %v", m.state)
	}
	m = sendKey(m, "esc")
	if m.state != tokenListStateBrowse {
		t.Errorf("esc → state %v; want tokenListStateBrowse", m.state)
	}
}

// -------------------------------------------------------------------
// Transition to purge confirmation (confPurge)
// -------------------------------------------------------------------

// TestTokenList_ConfirmDelete_WithPath_TransitionsToPurge verifies that
// pressing 'y' on the first confirm, for a token that HAS a DeployedPath,
// advances to tokenListStateConfPurge instead of deleting immediately.
func TestTokenList_ConfirmDelete_WithPath_TransitionsToPurge(t *testing.T) {
	m := newTokenListWithTokens([]tokens.Token{
		{ID: "t3", Type: tokens.TypeSSHKey, DeployedPath: "/tmp/id_ed25519"},
	})
	m = sendKey(m, "d")  // → confDel
	m = sendKey(m, "y")  // has path → should go to confPurge, not browse
	if m.state != tokenListStateConfPurge {
		t.Errorf("'y' with DeployedPath → state %v; want tokenListStateConfPurge", m.state)
	}
}

// TestTokenList_ConfirmDelete_NoPath_DeletesDirectly verifies that pressing 'y'
// on the first confirm for a token WITHOUT a DeployedPath goes straight to
// browse (no second confirmation needed — no file to delete).
func TestTokenList_ConfirmDelete_NoPath_DeletesDirectly(t *testing.T) {
	m := newTokenListWithTokens([]tokens.Token{
		{ID: "t4", Type: tokens.TypeGitHubPAT, DeployedPath: ""},
	})
	m = sendKey(m, "d")  // → confDel
	m = sendKey(m, "y")  // no path → should go straight to browse
	if m.state != tokenListStateBrowse {
		t.Errorf("'y' without path → state %v; want tokenListStateBrowse", m.state)
	}
}

// -------------------------------------------------------------------
// Second confirmation screen (confPurge)
// -------------------------------------------------------------------

// TestTokenList_ConfirmPurge_EscCancelsAll verifies that esc from the purge
// confirmation returns to browse without deleting anything.
func TestTokenList_ConfirmPurge_EscCancelsAll(t *testing.T) {
	m := newTokenListWithTokens([]tokens.Token{
		{ID: "t5", Type: tokens.TypeSSHKey, DeployedPath: "/tmp/id_ed25519"},
	})
	m = sendKey(m, "d")  // confDel
	m = sendKey(m, "y")  // confPurge (has path)
	if m.state != tokenListStateConfPurge {
		t.Fatalf("precondition: state = %v", m.state)
	}
	m = sendKey(m, "esc") // cancel both
	if m.state != tokenListStateBrowse {
		t.Errorf("esc from confPurge → state %v; want tokenListStateBrowse", m.state)
	}
	// Still has 1 token (nothing deleted with nil store in list).
	if len(m.all) != 1 {
		t.Errorf("after esc: %d tokens; want 1 (nothing should have been deleted)", len(m.all))
	}
}

// TestTokenList_ConfirmPurge_NKeepsFile verifies that pressing 'n' at the
// purge screen deletes the record but keeps the file on disk.
func TestTokenList_ConfirmPurge_NKeepsFile(t *testing.T) {
	// Create a real temp file to verify it is NOT removed.
	dir := t.TempDir()
	filePath := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(filePath, []byte("fake key"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	m := newTokenListWithTokens([]tokens.Token{
		{ID: "t6", Type: tokens.TypeSSHKey, DeployedPath: filePath},
	})
	m = sendKey(m, "d")  // confDel
	m = sendKey(m, "y")  // confPurge
	m = sendKey(m, "n")  // keep file, delete record only

	if m.state != tokenListStateBrowse {
		t.Errorf("'n' → state %v; want tokenListStateBrowse", m.state)
	}
	// File must still exist.
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Errorf("'n' at purge confirm deleted the file; it should have been kept")
	}
	// Notice should mention the file was kept.
	if !strings.Contains(m.notice, "kept") {
		t.Errorf("notice = %q; want mention of 'kept'", m.notice)
	}
}

// TestTokenList_ConfirmPurge_YDeletesFile verifies that pressing 'y' at the
// purge screen deletes the file from disk.
func TestTokenList_ConfirmPurge_YDeletesFile(t *testing.T) {
	// Create a real temp file to verify it IS removed.
	dir := t.TempDir()
	filePath := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(filePath, []byte("fake key"), 0o600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	m := newTokenListWithTokens([]tokens.Token{
		{ID: "t7", Type: tokens.TypeSSHKey, DeployedPath: filePath},
	})
	m = sendKey(m, "d")  // confDel
	m = sendKey(m, "y")  // confPurge
	m = sendKey(m, "y")  // confirm purge — delete both

	if m.state != tokenListStateBrowse {
		t.Errorf("'y' purge → state %v; want tokenListStateBrowse", m.state)
	}
	// File must be gone.
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Errorf("'y' at purge confirm: file still exists; want it deleted")
	}
	// Notice should confirm deletion.
	if !strings.Contains(m.notice, filePath) {
		t.Errorf("notice = %q; want file path in message", m.notice)
	}
}

// TestTokenList_ConfirmPurge_YFileAlreadyGone verifies that if the file no
// longer exists on disk, 'y' at purge confirm succeeds gracefully.
func TestTokenList_ConfirmPurge_YFileAlreadyGone(t *testing.T) {
	m := newTokenListWithTokens([]tokens.Token{
		{ID: "t8", Type: tokens.TypeSSHKey, DeployedPath: "/nonexistent/path/id_ed25519"},
	})
	m = sendKey(m, "d")
	m = sendKey(m, "y") // confPurge
	m = sendKey(m, "y") // purge

	if m.state != tokenListStateBrowse {
		t.Errorf("'y' purge (missing file) → state %v; want tokenListStateBrowse", m.state)
	}
	// Should NOT report an error — missing file is a no-op.
	if strings.Contains(m.notice, "failed") || strings.Contains(m.notice, "error") {
		t.Errorf("notice = %q; want no error for already-missing file", m.notice)
	}
	if !strings.Contains(m.notice, "already gone") {
		t.Errorf("notice = %q; want 'already gone' message", m.notice)
	}
}

// TestTokenList_ViewConfirmPurge_NoPanic verifies that viewConfirmPurge does
// not panic for any reasonable token value.
func TestTokenList_ViewConfirmPurge_NoPanic(t *testing.T) {
	m := newTokenListWithTokens([]tokens.Token{
		{ID: "t9", Type: tokens.TypeSSHKey, DeployedPath: "/tmp/id_ed25519"},
	})
	m.state = tokenListStateConfPurge
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("viewConfirmPurge panicked: %v", r)
		}
	}()
	_ = m.View()
}
