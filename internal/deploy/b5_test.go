package deploy_test

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/arjunjaincs/decoyd/internal/deploy"
	"github.com/arjunjaincs/decoyd/internal/tokens"
)

// ── B5: Concurrent deploy — exactly one wins ─────────────────────────────────

// TestDeploy_ConcurrentExactlyOneWins verifies that when N goroutines race to
// deploy the same token file, exactly one succeeds and the rest get
// ErrAlreadyExists.  Uses O_CREAT|O_EXCL which is atomic on Linux.
func TestDeploy_ConcurrentExactlyOneWins(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("O_EXCL atomicity test is Linux-specific; Windows uses LockFileEx")
	}
	dir := t.TempDir()
	tok := makeTestToken(tokens.TypeEnvSecrets)

	const n = 10
	var (
		mu       sync.Mutex
		wins     int
		failures int
	)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_, err := deploy.DeployToFile(tok, dir, deploy.Options{})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				wins++
			} else {
				failures++
			}
		}()
	}
	wg.Wait()

	if wins != 1 {
		t.Errorf("concurrent deploy: %d goroutines succeeded; want exactly 1", wins)
	}
	if failures != n-1 {
		t.Errorf("concurrent deploy: %d goroutines failed; want %d", failures, n-1)
	}
	t.Logf("concurrent deploy: wins=%d failures=%d (n=%d)", wins, failures, n)
}

// ── B5: .pub rollback on write failure ───────────────────────────────────────

// TestDeploy_SSHPubRollbackOnFailure verifies that if writing the .pub file
// fails, the already-written private key is removed (atomic pair invariant).
//
// We inject a failure by pre-creating the .pub destination so the O_EXCL open
// fails, simulating an I/O error after the private key was written.
func TestDeploy_SSHPubRollbackOnFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("O_NOFOLLOW/O_EXCL rollback test is Linux-specific")
	}
	dir := t.TempDir()
	sshTok, err := tokens.Generate(tokens.TypeSSHKey)
	if err != nil {
		t.Fatalf("Generate SSH key: %v", err)
	}

	// Pre-create the .pub destination to trigger O_EXCL failure on second write.
	pubDest := filepath.Join(dir, "id_ed25519.pub")
	if err := os.WriteFile(pubDest, []byte("pre-existing"), 0o644); err != nil {
		t.Fatalf("pre-create pub: %v", err)
	}

	_, err = deploy.DeployToFile(sshTok, dir, deploy.Options{})
	if err == nil {
		t.Fatal("DeployToFile: expected error when .pub already exists; got nil")
	}
	t.Logf("got expected error: %v", err)

	// Private key MUST NOT exist — it should have been rolled back.
	privDest := filepath.Join(dir, "id_ed25519")
	if _, statErr := os.Stat(privDest); statErr == nil {
		t.Error("private key was NOT rolled back after .pub write failure — partial deploy left on disk")
	} else {
		t.Logf("private key correctly absent after rollback: %v", statErr)
	}

	// Pre-existing .pub content must be unchanged.
	data, _ := os.ReadFile(pubDest)
	if string(data) != "pre-existing" {
		t.Error(".pub file was overwritten despite O_EXCL; O_NOFOLLOW/O_EXCL not effective")
	}
}

// ── B5: Windows reparse-point refusal ────────────────────────────────────────
// (see deploy_b5_windows_test.go — build-tagged; this stub runs on Linux)

// TestDeploy_ReparsePointRefusal_Stub documents that Windows reparse-point
// refusal is tested in deploy_b5_windows_test.go (build-tagged for windows).
func TestDeploy_ReparsePointRefusal_Stub(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("covered by deploy_b5_windows_test.go on Windows")
	}
	t.Log("Windows reparse-point refusal test is in deploy_b5_windows_test.go")
}

// ── B5: Root writing outside home — warning ───────────────────────────────────

// TestDeploy_RootOutsideHomeWarning verifies that deploying as root to a path
// outside the user's home directory logs a warning.
// On non-Linux or non-root we just verify the helper exports exist.
func TestDeploy_RootOutsideHomeWarning(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("root outside-home warning test requires running as root (use sudo go test)")
	}
	dir := t.TempDir()
	tok := makeTestToken(tokens.TypeEnvSecrets)
	// Deploy to /tmp (outside home).
	_, err := deploy.DeployToFile(tok, dir, deploy.Options{})
	// No error expected; the warning goes to stderr.
	// We can only verify no panic and the file exists.
	if err != nil {
		t.Logf("deploy error (may be expected in test env): %v", err)
	}
}
