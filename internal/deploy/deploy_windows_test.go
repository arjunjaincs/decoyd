//go:build windows

package deploy

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/arjunjaincs/decoyd/internal/tokens"
)

func makeWinTestToken(tokenType string) tokens.Token {
	return tokens.Token{
		ID:        "deadbeefdeadbeef",
		Type:      tokenType,
		Value:     "test-value-contents\n",
		Filename:  "testfile.txt",
		CreatedAt: time.Now().UTC(),
	}
}

func createJunction(t *testing.T, link, target string) {
	t.Helper()
	// #nosec G204 -- test calls fixed Windows cmd /C mklink /J with t.TempDir paths
	out, err := exec.Command("cmd", "/C", "mklink", "/J", link, target).CombinedOutput()
	if err != nil {
		t.Fatalf("mklink /J %q %q failed: %v\noutput: %s", link, target, err, string(out))
	}
}

// TestDeploy_WindowsSymlinkOrJunctionRefused verifies that pre-placing a Windows
// symlink (or NTFS junction fallback via mklink /J) at the destination causes
// deploy and openExclNoFollow to refuse rather than follow the reparse point.
func TestDeploy_WindowsSymlinkOrJunctionRefused(t *testing.T) {
	dir := t.TempDir()
	tok := makeWinTestToken(tokens.TypeAWSCredentials)
	dest := filepath.Join(dir, tok.Filename)

	targetFile := filepath.Join(dir, "real_file.txt")
	if err := os.WriteFile(targetFile, []byte("real"), 0o600); err != nil {
		t.Fatalf("write real_file: %v", err)
	}

	usedSymlink := false
	if err := os.Symlink(targetFile, dest); err == nil {
		usedSymlink = true
		t.Logf("created Windows symlink at %s", dest)
	} else {
		t.Logf("os.Symlink unavailable (%v); falling back to mklink /J", err)
		targetDir := filepath.Join(dir, "real_dir")
		if mkErr := os.Mkdir(targetDir, 0o700); mkErr != nil {
			t.Fatalf("mkdir real_dir: %v", mkErr)
		}
		createJunction(t, dest, targetDir)
	}

	if _, err := DeployToFile(tok, dir, Options{}); err == nil {
		t.Fatalf("DeployToFile succeeded when reparse point existed at %s; want error", dest)
	} else {
		t.Logf("DeployToFile correctly refused reparse point: %v", err)
	}

	if usedSymlink {
		got, rerr := os.ReadFile(targetFile)
		if rerr != nil {
			t.Fatalf("read targetFile: %v", rerr)
		}
		if string(got) != "real" {
			t.Fatalf("targetFile overwritten via symlink: got %q", string(got))
		}
	}

	// Explicitly verify NTFS junction refusal via openExclNoFollow (both live and
	// dangling junctions must return ErrReparsePoint rather than opening/clobbering).
	juncTarget := filepath.Join(dir, "junc_target")
	if err := os.Mkdir(juncTarget, 0o700); err != nil {
		t.Fatalf("mkdir junc_target: %v", err)
	}
	juncPath := filepath.Join(dir, "junc_dest")
	createJunction(t, juncPath, juncTarget)

	_, jerr := openExclNoFollow(juncPath, 0o600)
	if !errors.Is(jerr, ErrReparsePoint) {
		t.Fatalf("openExclNoFollow(live junction) = %v; want ErrReparsePoint", jerr)
	}

	if err := os.Remove(juncTarget); err != nil {
		t.Fatalf("remove junc_target: %v", err)
	}
	_, derr := openExclNoFollow(juncPath, 0o600)
	if !errors.Is(derr, ErrReparsePoint) {
		t.Fatalf("openExclNoFollow(dangling junction) = %v; want ErrReparsePoint", derr)
	}
}

// TestDeploy_WindowsOEXCLRefusal verifies that existing regular files at the
// destination are refused without overwriting via both DeployToFile and openExclNoFollow.
func TestDeploy_WindowsOEXCLRefusal(t *testing.T) {
	dir := t.TempDir()
	tok := makeWinTestToken(tokens.TypeEnvSecrets)
	dest := filepath.Join(dir, tok.Filename)

	const sentinel = "keep-original-contents"
	if err := os.WriteFile(dest, []byte(sentinel), 0o600); err != nil {
		t.Fatalf("seed dest: %v", err)
	}

	if _, err := DeployToFile(tok, dir, Options{}); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("DeployToFile error = %v; want ErrAlreadyExists", err)
	}

	if _, err := openExclNoFollow(dest, 0o600); !errors.Is(err, os.ErrExist) {
		t.Fatalf("openExclNoFollow(existing) error = %v; want os.ErrExist", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(got) != sentinel {
		t.Fatalf("dest content overwritten: got %q, want %q", string(got), sentinel)
	}
}
