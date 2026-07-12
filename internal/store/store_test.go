package store_test

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/arjunjaincs/decoyd/internal/store"
	"github.com/arjunjaincs/decoyd/internal/tokens"
)

// openTestStore creates a fresh store in a temp directory for testing.
func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open() error: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// makeToken returns a Token with all fields set, including Notes.
func makeToken(t *testing.T) tokens.Token {
	t.Helper()
	id, err := tokens.NewID()
	if err != nil {
		t.Fatalf("NewID() error: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second) // JSON round-trip truncates sub-second
	triggered := now.Add(5 * time.Minute)
	return tokens.Token{
		ID:             id,
		Type:           tokens.TypeGitHubPAT,
		Value:          "ghp_testvalue123456",
		Filename:       ".github_token",
		CreatedAt:      now,
		DeployedPath:   "/home/user/.github_token",
		AlertChannelID: "chan-abc",
		Triggered:      true,
		TriggeredAt:    &triggered,
		Notes:          "prod server decoy — do not delete",
	}
}

// ── SaveToken / GetToken round-trip ──────────────────────────────────────────

func TestStore_RoundTrip_AllFields(t *testing.T) {
	st := openTestStore(t)
	want := makeToken(t)

	if err := st.SaveToken(want); err != nil {
		t.Fatalf("SaveToken() error: %v", err)
	}

	got, err := st.GetToken(want.ID)
	if err != nil {
		t.Fatalf("GetToken() error: %v", err)
	}

	// Compare every field explicitly.
	if got.ID != want.ID {
		t.Errorf("ID: got %q, want %q", got.ID, want.ID)
	}
	if got.Type != want.Type {
		t.Errorf("Type: got %q, want %q", got.Type, want.Type)
	}
	if got.Value != want.Value {
		t.Errorf("Value: got %q, want %q", got.Value, want.Value)
	}
	if got.Filename != want.Filename {
		t.Errorf("Filename: got %q, want %q", got.Filename, want.Filename)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("CreatedAt: got %v, want %v", got.CreatedAt, want.CreatedAt)
	}
	if got.DeployedPath != want.DeployedPath {
		t.Errorf("DeployedPath: got %q, want %q", got.DeployedPath, want.DeployedPath)
	}
	if got.AlertChannelID != want.AlertChannelID {
		t.Errorf("AlertChannelID: got %q, want %q", got.AlertChannelID, want.AlertChannelID)
	}
	if got.Triggered != want.Triggered {
		t.Errorf("Triggered: got %v, want %v", got.Triggered, want.Triggered)
	}
	if got.TriggeredAt == nil {
		t.Error("TriggeredAt: got nil, want non-nil")
	} else if !got.TriggeredAt.Equal(*want.TriggeredAt) {
		t.Errorf("TriggeredAt: got %v, want %v", got.TriggeredAt, want.TriggeredAt)
	}
	if got.Notes != want.Notes {
		t.Errorf("Notes: got %q, want %q", got.Notes, want.Notes)
	}
}

// TestStore_GetToken_NotFound ensures ErrNotFound is returned for missing IDs.
func TestStore_GetToken_NotFound(t *testing.T) {
	st := openTestStore(t)
	_, err := st.GetToken("deadbeefdeadbeef")
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetToken(missing) = %v; want ErrNotFound", err)
	}
}

// ── ListTokens ────────────────────────────────────────────────────────────────

func TestStore_ListTokens_Empty(t *testing.T) {
	st := openTestStore(t)
	ts, err := st.ListTokens()
	if err != nil {
		t.Fatalf("ListTokens() error: %v", err)
	}
	if len(ts) != 0 {
		t.Errorf("ListTokens() len = %d; want 0", len(ts))
	}
}

func TestStore_ListTokens_MultipleRecords(t *testing.T) {
	st := openTestStore(t)
	const n = 5
	for i := 0; i < n; i++ {
		tok := makeToken(t)
		tok.Type = tokens.TypeGitHubPAT
		if err := st.SaveToken(tok); err != nil {
			t.Fatalf("SaveToken() #%d error: %v", i, err)
		}
	}
	ts, err := st.ListTokens()
	if err != nil {
		t.Fatalf("ListTokens() error: %v", err)
	}
	if len(ts) != n {
		t.Errorf("ListTokens() len = %d; want %d", len(ts), n)
	}
}

// ── UpdateToken ───────────────────────────────────────────────────────────────

func TestStore_UpdateToken_OverwritesExisting(t *testing.T) {
	st := openTestStore(t)
	tok := makeToken(t)
	if err := st.SaveToken(tok); err != nil {
		t.Fatalf("SaveToken() error: %v", err)
	}

	tok.Notes = "updated notes"
	tok.Triggered = false
	if err := st.UpdateToken(tok); err != nil {
		t.Fatalf("UpdateToken() error: %v", err)
	}

	got, err := st.GetToken(tok.ID)
	if err != nil {
		t.Fatalf("GetToken() after update error: %v", err)
	}
	if got.Notes != "updated notes" {
		t.Errorf("Notes after update = %q; want %q", got.Notes, "updated notes")
	}
	if got.Triggered {
		t.Error("Triggered after update = true; want false")
	}
}

// ── DeleteToken ───────────────────────────────────────────────────────────────

func TestStore_DeleteToken(t *testing.T) {
	st := openTestStore(t)
	tok := makeToken(t)
	if err := st.SaveToken(tok); err != nil {
		t.Fatalf("SaveToken() error: %v", err)
	}

	if err := st.DeleteToken(tok.ID); err != nil {
		t.Fatalf("DeleteToken() error: %v", err)
	}

	_, err := st.GetToken(tok.ID)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetToken() after delete = %v; want ErrNotFound", err)
	}
}

// TestStore_DeleteToken_NoOp confirms deleting a non-existent ID is not an error.
func TestStore_DeleteToken_NoOp(t *testing.T) {
	st := openTestStore(t)
	if err := st.DeleteToken("0000000000000000"); err != nil {
		t.Errorf("DeleteToken(missing) = %v; want nil", err)
	}
}

// ── ListByType ────────────────────────────────────────────────────────────────

func TestStore_ListByType(t *testing.T) {
	st := openTestStore(t)

	// Save 3 PAT tokens and 2 SSH tokens.
	for i := 0; i < 3; i++ {
		tok := makeToken(t)
		tok.Type = tokens.TypeGitHubPAT
		st.SaveToken(tok)
	}
	for i := 0; i < 2; i++ {
		tok := makeToken(t)
		tok.Type = tokens.TypeSSHKey
		st.SaveToken(tok)
	}

	pats, err := st.ListByType(tokens.TypeGitHubPAT)
	if err != nil {
		t.Fatalf("ListByType(PAT) error: %v", err)
	}
	if len(pats) != 3 {
		t.Errorf("ListByType(PAT) len = %d; want 3", len(pats))
	}

	sshKeys, err := st.ListByType(tokens.TypeSSHKey)
	if err != nil {
		t.Fatalf("ListByType(SSH) error: %v", err)
	}
	if len(sshKeys) != 2 {
		t.Errorf("ListByType(SSH) len = %d; want 2", len(sshKeys))
	}

	none, err := st.ListByType(tokens.TypeDNSCanary)
	if err != nil {
		t.Fatalf("ListByType(DNS) error: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("ListByType(DNS) len = %d; want 0", len(none))
	}
}

// ── SaveToken validation ──────────────────────────────────────────────────────

func TestStore_SaveToken_EmptyID(t *testing.T) {
	st := openTestStore(t)
	err := st.SaveToken(tokens.Token{}) // ID is empty
	if err == nil {
		t.Error("SaveToken(emptyID) should return an error")
	}
}

// ── Notes field round-trip ────────────────────────────────────────────────────

func TestStore_Notes_RoundTrip(t *testing.T) {
	st := openTestStore(t)
	tok := makeToken(t)
	tok.Notes = "prod server decoy — do not delete 🔑"

	if err := st.SaveToken(tok); err != nil {
		t.Fatalf("SaveToken() error: %v", err)
	}
	got, err := st.GetToken(tok.ID)
	if err != nil {
		t.Fatalf("GetToken() error: %v", err)
	}
	if got.Notes != tok.Notes {
		t.Errorf("Notes = %q; want %q", got.Notes, tok.Notes)
	}
}
