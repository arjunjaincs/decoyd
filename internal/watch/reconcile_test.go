// reconcile_test.go — tests for ReconcileSnapshot.
package watch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/arjunjaincs/decoyd/internal/store"
	"github.com/arjunjaincs/decoyd/internal/tokens"
)

// TestReconcileSnapshot_NilStoreIsNoop verifies that ReconcileSnapshot with a
// nil store returns nil and does not create or modify the snapshot file.
func TestReconcileSnapshot_NilStoreIsNoop(t *testing.T) {
	dir := t.TempDir()
	if err := ReconcileSnapshot(nil, dir); err != nil {
		t.Fatalf("ReconcileSnapshot(nil) returned error: %v", err)
	}
	// No snapshot file should be created.
	if _, err := os.Stat(filepath.Join(dir, "deployed_tokens.json")); !os.IsNotExist(err) {
		t.Error("deployed_tokens.json was created by a nil-store ReconcileSnapshot")
	}
}

// TestReconcileSnapshot_WritesDeployedTokens verifies that ReconcileSnapshot
// creates a snapshot containing only tokens with a non-empty DeployedPath.
func TestReconcileSnapshot_WritesDeployedTokens(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "decoyd.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	// Add one deployed token and one undeployed token.
	deployed := tokens.Token{
		ID:           "aabbccdd00000001",
		Type:         tokens.TypeAWSCredentials,
		DeployedPath: "/home/user/.aws/credentials",
	}
	undeployed := tokens.Token{
		ID:   "aabbccdd00000002",
		Type: tokens.TypeGitHubPAT,
	}
	if err := st.SaveToken(deployed); err != nil {
		t.Fatalf("SaveToken deployed: %v", err)
	}
	if err := st.SaveToken(undeployed); err != nil {
		t.Fatalf("SaveToken undeployed: %v", err)
	}

	if err := ReconcileSnapshot(st, dir); err != nil {
		t.Fatalf("ReconcileSnapshot: %v", err)
	}

	snap, err := ReadDeployedSnapshot(dir)
	if err != nil {
		t.Fatalf("ReadDeployedSnapshot: %v", err)
	}
	if len(snap) != 1 {
		t.Fatalf("snapshot has %d entries; want 1", len(snap))
	}
	if snap[0].ID != deployed.ID {
		t.Errorf("snapshot[0].ID = %q; want %q", snap[0].ID, deployed.ID)
	}
	if snap[0].DeployedPath != deployed.DeployedPath {
		t.Errorf("snapshot[0].DeployedPath = %q; want %q", snap[0].DeployedPath, deployed.DeployedPath)
	}
}

// TestReconcileSnapshot_IsIdempotent verifies that calling ReconcileSnapshot
// twice in a row produces the same result.
func TestReconcileSnapshot_IsIdempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "decoyd.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	tok := tokens.Token{
		ID:           "aabbccdd00000003",
		Type:         tokens.TypeSlackToken,
		DeployedPath: "/tmp/slack.token",
	}
	if err := st.SaveToken(tok); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := ReconcileSnapshot(st, dir); err != nil {
			t.Fatalf("ReconcileSnapshot iteration %d: %v", i, err)
		}
	}

	snap, err := ReadDeployedSnapshot(dir)
	if err != nil {
		t.Fatalf("ReadDeployedSnapshot: %v", err)
	}
	if len(snap) != 1 {
		t.Fatalf("after 3 calls: snapshot has %d entries; want 1", len(snap))
	}
}

// TestReconcileSnapshot_OverwritesStaleEntries verifies that deleted tokens
// are removed from the snapshot on reconcile — stale entries don't persist.
func TestReconcileSnapshot_OverwritesStaleEntries(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "decoyd.db")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	tok1 := tokens.Token{ID: "aaaa000000000001", Type: tokens.TypeAWSCredentials, DeployedPath: "/tmp/creds"}
	tok2 := tokens.Token{ID: "aaaa000000000002", Type: tokens.TypeEnvSecrets, DeployedPath: "/tmp/.env"}
	if err := st.SaveToken(tok1); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveToken(tok2); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileSnapshot(st, dir); err != nil {
		t.Fatal(err)
	}

	// Delete tok2 from store.
	if err := st.DeleteToken(tok2.ID); err != nil {
		t.Fatal(err)
	}

	// Reconcile again — tok2 must be gone from snapshot.
	if err := ReconcileSnapshot(st, dir); err != nil {
		t.Fatal(err)
	}
	snap, err := ReadDeployedSnapshot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap) != 1 {
		t.Fatalf("after delete+reconcile: snapshot has %d entries; want 1", len(snap))
	}
	if snap[0].ID != tok1.ID {
		t.Errorf("snapshot[0].ID = %q; want %q", snap[0].ID, tok1.ID)
	}
}
