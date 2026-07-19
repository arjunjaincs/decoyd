// deployscreen_delete_test.go — tests for the 'd' delete flow on the deploy token picker.
package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/arjunjaincs/decoyd/internal/tokens"
)

// newDeployModelWithTokens returns a DeployModel pre-loaded with synthetic
// tokens (no real store — st is nil, matching how we test with nil stores
// for the key-routing paths that don't actually persist).
func newDeployModelWithTokens(toks []tokens.Token) DeployModel {
	m := NewDeployModel(testWidth, testHeight, nil, "")
	m.allTokens = toks
	return m
}

// TestDeploy_DKeyEntersConfirmDelete verifies that pressing 'd' on the token
// picker transitions to deployStateConfirmDelete.
func TestDeploy_DKeyEntersConfirmDelete(t *testing.T) {
	toks := []tokens.Token{
		{ID: "tok001", Type: tokens.TypeAWSCredentials, DeployedPath: "/tmp/creds"},
	}
	m := newDeployModelWithTokens(toks)
	if m.state != deployStatePickToken {
		t.Fatalf("precondition: state = %v; want deployStatePickToken", m.state)
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	dm := updated.(DeployModel)
	if dm.state != deployStateConfirmDelete {
		t.Errorf("'d' → state %v; want deployStateConfirmDelete", dm.state)
	}
}

// TestDeploy_DKeyNoOpWhenEmpty verifies that 'd' on an empty token list does
// NOT transition to deployStateConfirmDelete (no token to delete).
func TestDeploy_DKeyNoOpWhenEmpty(t *testing.T) {
	m := newDeployModelWithTokens(nil)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	dm := updated.(DeployModel)
	if dm.state != deployStatePickToken {
		t.Errorf("'d' on empty list → state %v; want deployStatePickToken", dm.state)
	}
}

// TestDeploy_ConfirmDelete_EscCancels verifies that pressing esc/n in the
// confirm-delete screen returns to deployStatePickToken without deleting.
func TestDeploy_ConfirmDelete_EscCancels(t *testing.T) {
	toks := []tokens.Token{
		{ID: "tok002", Type: tokens.TypeGitHubPAT},
	}
	m := newDeployModelWithTokens(toks)

	// Enter confirm-delete.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = updated.(DeployModel)
	if m.state != deployStateConfirmDelete {
		t.Fatalf("precondition failed: state = %v", m.state)
	}

	// Cancel with esc.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(DeployModel)
	if m.state != deployStatePickToken {
		t.Errorf("esc → state %v; want deployStatePickToken", m.state)
	}
	// Token list must be unchanged.
	if len(m.allTokens) != 1 {
		t.Errorf("after cancel: %d tokens; want 1", len(m.allTokens))
	}
}

// TestDeploy_ConfirmDelete_NilStoreYConfirm verifies that pressing y/enter in
// confirm-delete (with nil store) transitions back to deployStatePickToken.
// With nil st, no actual bbolt call is made — this tests the state machine path.
func TestDeploy_ConfirmDelete_NilStoreYConfirm(t *testing.T) {
	toks := []tokens.Token{
		{ID: "tok003", Type: tokens.TypeSlackToken},
	}
	m := newDeployModelWithTokens(toks)

	// Enter confirm-delete.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = updated.(DeployModel)

	// Confirm with y (nil store: no actual delete, but state machine still progresses).
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = updated.(DeployModel)
	if m.state != deployStatePickToken {
		t.Errorf("y confirm → state %v; want deployStatePickToken", m.state)
	}
}

// TestDeploy_ConfirmDelete_ViewNoPanic verifies that viewConfirmDelete does not
// panic for a normal token with and without a DeployedPath.
func TestDeploy_ConfirmDelete_ViewNoPanic(t *testing.T) {
	for _, tc := range []struct {
		name string
		tok  tokens.Token
	}{
		{"with_path", tokens.Token{ID: "tok004", Type: tokens.TypeAWSCredentials, DeployedPath: "/tmp/creds"}},
		{"no_path", tokens.Token{ID: "tok005", Type: tokens.TypeGitHubPAT}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("viewConfirmDelete panicked: %v", r)
				}
			}()
			m := newDeployModelWithTokens([]tokens.Token{tc.tok})
			m.state = deployStateConfirmDelete
			_ = m.View()
		})
	}
}
