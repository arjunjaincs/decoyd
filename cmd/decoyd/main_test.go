// main_test.go — tests for CLI command functions in package main.
package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arjunjaincs/decoyd/internal/store"
	"github.com/arjunjaincs/decoyd/internal/tokens"
)

// openTestStore opens a fresh bbolt store in t.TempDir() and registers cleanup.
func openTestStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st, dir
}

// saveToken saves a token to the store and returns it.
func saveToken(t *testing.T, st *store.Store, tok tokens.Token) tokens.Token {
	t.Helper()
	if err := st.SaveToken(tok); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}
	return tok
}

// captureStdout redirects os.Stdout to a buffer for the duration of f(), then
// restores it and returns whatever was printed.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	f()
	w.Close()
	os.Stdout = orig
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("io.Copy: %v", err)
	}
	return buf.String()
}

// -------------------------------------------------------------------
// cmdRemove — no purge (existing behavior)
// -------------------------------------------------------------------

// TestCmdRemove_NoPurge_LeavesFile verifies that cmdRemove without purge
// removes the record from the store but does NOT delete the deployed file.
func TestCmdRemove_NoPurge_LeavesFile(t *testing.T) {
	st, dataDir := openTestStore(t)

	// Create a real file to deploy to.
	filePath := filepath.Join(dataDir, "id_ed25519")
	if err := os.WriteFile(filePath, []byte("fake ssh key"), 0o600); err != nil {
		t.Fatalf("setup write file: %v", err)
	}

	tok := saveToken(t, st, tokens.Token{
		ID:           "remove-nopurge-001",
		Type:         tokens.TypeSSHKey,
		DeployedPath: filePath,
	})

	out := captureStdout(t, func() {
		if err := cmdRemove(st, dataDir, tok.ID, false); err != nil {
			t.Fatalf("cmdRemove: %v", err)
		}
	})

	// Record must be gone from store.
	if _, err := st.GetToken(tok.ID); err == nil {
		t.Error("token still in store after cmdRemove; want removed")
	}

	// File must still exist.
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Error("cmdRemove deleted the deployed file; want it kept")
	}

	// Output should mention --purge hint.
	if !strings.Contains(out, "--purge") {
		t.Errorf("output = %q; want '--purge' hint", out)
	}
}

// -------------------------------------------------------------------
// cmdRemove — purge mode
// -------------------------------------------------------------------

// TestCmdRemove_Purge_DeletesFile verifies that --purge removes both the
// record and the deployed file.
func TestCmdRemove_Purge_DeletesFile(t *testing.T) {
	st, dataDir := openTestStore(t)

	filePath := filepath.Join(dataDir, "creds")
	if err := os.WriteFile(filePath, []byte("fake creds"), 0o600); err != nil {
		t.Fatalf("setup write file: %v", err)
	}

	tok := saveToken(t, st, tokens.Token{
		ID:           "remove-purge-001",
		Type:         tokens.TypeAWSCredentials,
		DeployedPath: filePath,
	})

	out := captureStdout(t, func() {
		if err := cmdRemove(st, dataDir, tok.ID, true); err != nil {
			t.Fatalf("cmdRemove --purge: %v", err)
		}
	})

	// Record must be gone.
	if _, err := st.GetToken(tok.ID); err == nil {
		t.Error("token still in store after purge; want removed")
	}

	// File must be gone.
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Error("file still exists after --purge; want deleted")
	}

	// Output should confirm deletion.
	if !strings.Contains(out, "Deleted deployed file") {
		t.Errorf("output = %q; want 'Deleted deployed file'", out)
	}
}

// TestCmdRemove_Purge_MissingFile_NoError verifies that --purge on a token
// whose deployed file is already gone does NOT return an error — it prints a
// "was already gone" note instead.
func TestCmdRemove_Purge_MissingFile_NoError(t *testing.T) {
	st, dataDir := openTestStore(t)

	tok := saveToken(t, st, tokens.Token{
		ID:           "remove-purge-missing",
		Type:         tokens.TypeAWSCredentials,
		DeployedPath: filepath.Join(dataDir, "nonexistent"),
	})

	var cmdErr error
	out := captureStdout(t, func() {
		cmdErr = cmdRemove(st, dataDir, tok.ID, true)
	})

	if cmdErr != nil {
		t.Errorf("cmdRemove --purge (missing file) returned error: %v", cmdErr)
	}
	if !strings.Contains(out, "already gone") {
		t.Errorf("output = %q; want 'already gone' note", out)
	}
}

// TestCmdRemove_Purge_NoDeployedPath verifies that --purge on a token without
// a recorded DeployedPath prints an informational note and returns nil.
func TestCmdRemove_Purge_NoDeployedPath(t *testing.T) {
	st, dataDir := openTestStore(t)

	tok := saveToken(t, st, tokens.Token{
		ID:   "remove-purge-nopath",
		Type: tokens.TypeGitHubPAT,
	})

	var cmdErr error
	out := captureStdout(t, func() {
		cmdErr = cmdRemove(st, dataDir, tok.ID, true)
	})

	if cmdErr != nil {
		t.Errorf("cmdRemove --purge (no path) returned error: %v", cmdErr)
	}
	if !strings.Contains(out, "no deployed file") {
		t.Errorf("output = %q; want 'no deployed file' note", out)
	}
}

// TestCmdRemove_NotFound_FriendlyError verifies that removing a non-existent
// ID returns the plain-English "no token with ID" message, not a raw DB error.
func TestCmdRemove_NotFound_FriendlyError(t *testing.T) {
	st, dataDir := openTestStore(t)

	err := cmdRemove(st, dataDir, "does-not-exist", false)
	if err == nil {
		t.Fatal("expected error for missing ID; got nil")
	}
	if !strings.Contains(err.Error(), "no token with ID") {
		t.Errorf("error = %q; want 'no token with ID' message", err.Error())
	}
}
