//go:build linux

package watch

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arjunjaincs/decoyd/internal/triglog"
)

func TestO5_SnapshotSymlinkSkippedAndLogged(t *testing.T) {
	dir := t.TempDir()
	dataDir := t.TempDir()

	target := filepath.Join(dir, "real-target.txt")
	if err := os.WriteFile(target, []byte("secret-target"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	linkPath := filepath.Join(dir, "decoy-link.txt")
	if err := os.Symlink(target, linkPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	// Save snapshot directly without re-writing owner_uid issues, or with current UID.
	uid := currentOwnerUID()
	snap := []DeployedToken{{
		ID:           "o5-symlink-tok",
		Type:         "env_secrets",
		DeployedPath: linkPath,
		OwnerUID:     uid,
	}}
	if err := WriteDeployedSnapshot(dataDir, snap); err != nil {
		t.Fatalf("WriteDeployedSnapshot: %v", err)
	}

	var logBuf bytes.Buffer
	origOut := log.Writer()
	log.SetOutput(&logBuf)
	defer log.SetOutput(origOut)

	w := &linuxWatcher{
		dataDir: dataDir,
		cfg:     WatcherConfig{DebounceDuration: 50 * time.Millisecond, RateLimit: 5},
		inoFD:   -1,
		stopR:   -1,
		stopW:   -1,
	}
	if err := w.start(); err != nil {
		t.Fatalf("start() must not fail on O5 symlink refusal: %v", err)
	}
	defer w.stop()

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "o5-symlink-tok") || !strings.Contains(logOutput, "O5") {
		t.Fatalf("expected O5 skip log for symlink, got:\n%s", logOutput)
	}

	// Reading through the symlink must not produce any trigger events.
	if _, err := os.ReadFile(linkPath); err != nil {
		t.Fatalf("read symlink: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	evs, err := triglog.Load(dataDir)
	if err != nil {
		t.Fatalf("triglog.Load: %v", err)
	}
	if len(evs) != 0 {
		t.Fatalf("expected 0 trigger events for refused symlink, got %d: %+v", len(evs), evs)
	}
}

func TestO5_SnapshotWrongUIDSkippedAndLogged(t *testing.T) {
	dir := t.TempDir()
	dataDir := t.TempDir()

	filePath := filepath.Join(dir, "other-user.env")
	if err := os.WriteFile(filePath, []byte("SECRET=1"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	wrongUID := uint32(65534)
	if cur := currentOwnerUID(); cur != nil && *cur == wrongUID {
		wrongUID = 65533
	}
	snap := []DeployedToken{{
		ID:           "o5-uid-tok",
		Type:         "env_secrets",
		DeployedPath: filePath,
		OwnerUID:     &wrongUID,
	}}
	if err := WriteDeployedSnapshot(dataDir, snap); err != nil {
		t.Fatalf("WriteDeployedSnapshot: %v", err)
	}

	var logBuf bytes.Buffer
	origOut := log.Writer()
	log.SetOutput(&logBuf)
	defer log.SetOutput(origOut)

	w := &linuxWatcher{
		dataDir: dataDir,
		cfg:     WatcherConfig{DebounceDuration: 50 * time.Millisecond, RateLimit: 5},
		inoFD:   -1,
		stopR:   -1,
		stopW:   -1,
	}
	if err := w.start(); err != nil {
		t.Fatalf("start() must not fail on O5 uid mismatch: %v", err)
	}
	defer w.stop()

	logOutput := logBuf.String()
	if !strings.Contains(logOutput, "o5-uid-tok") || !strings.Contains(logOutput, "uid") {
		t.Fatalf("expected O5 uid mismatch skip log, got:\n%s", logOutput)
	}

	if _, err := os.ReadFile(filePath); err != nil {
		t.Fatalf("read file: %v", err)
	}
	time.Sleep(150 * time.Millisecond)

	evs, err := triglog.Load(dataDir)
	if err != nil {
		t.Fatalf("triglog.Load: %v", err)
	}
	if len(evs) != 0 {
		t.Fatalf("expected 0 trigger events for mismatched uid path, got %d", len(evs))
	}
}
