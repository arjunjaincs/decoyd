package deploy_test

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

// ── B5: Root writing outside system directories — warning ─────────────────────

// TestDeploy_RootOutsideHomeWarning verifies via injectable UID/writer hooks
// that deploying as root outside /var/lib/decoyd and /etc/decoyd emits a warning
// in normal unprivileged CI, while system-managed paths and non-root do not.
func TestDeploy_RootOutsideHomeWarning(t *testing.T) {
	tok := makeTestToken(tokens.TypeEnvSecrets)

	t.Run("root_outside_system_dir_warns", func(t *testing.T) {
		var buf bytes.Buffer
		restore := deploy.SetRootWarnDepsForTest(func() int { return 0 }, &buf)
		defer restore()

		dir := t.TempDir()
		if _, err := deploy.DeployToFile(tok, dir, deploy.Options{}); err != nil {
			t.Fatalf("DeployToFile: %v", err)
		}
		got := buf.String()
		if !strings.Contains(got, "warning: running as root") || !strings.Contains(got, "outside system-managed directories") {
			t.Errorf("expected root warning on stderr buffer, got %q", got)
		}
	})

	t.Run("root_prefix_lookalike_warns", func(t *testing.T) {
		var buf bytes.Buffer
		restore := deploy.SetRootWarnDepsForTest(func() int { return 0 }, &buf)
		defer restore()

		lookalike := filepath.Join(t.TempDir(), "decoyd-not-system")
		_, _ = deploy.DeployToFile(tok, lookalike, deploy.Options{DryRun: true})
		if !strings.Contains(buf.String(), "warning: running as root") {
			t.Errorf("expected warning for lookalike dir %s, got %q", lookalike, buf.String())
		}
	})

	t.Run("root_inside_system_dir_silent", func(t *testing.T) {
		var buf bytes.Buffer
		restore := deploy.SetRootWarnDepsForTest(func() int { return 0 }, &buf)
		defer restore()

		sysPath := "/var/lib/decoyd/nonexistent-test-subdir"
		if _, err := os.Stat(filepath.Join(sysPath, tok.Filename)); err == nil {
			t.Skip("unexpected pre-existing file under system dir")
		}
		_, _ = deploy.DeployToFile(tok, sysPath, deploy.Options{DryRun: true})
		if buf.Len() != 0 {
			t.Errorf("expected no warning for %s, got %q", sysPath, buf.String())
		}
	})

	t.Run("non_root_silent", func(t *testing.T) {
		var buf bytes.Buffer
		restore := deploy.SetRootWarnDepsForTest(func() int { return 1000 }, &buf)
		defer restore()

		if _, err := deploy.DeployToFile(tok, t.TempDir(), deploy.Options{DryRun: true}); err != nil {
			t.Fatalf("DeployToFile(DryRun): %v", err)
		}
		if buf.Len() != 0 {
			t.Errorf("expected no warning for non-root UID, got %q", buf.String())
		}
	})
}
