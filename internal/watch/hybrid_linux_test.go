//go:build linux

package watch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arjunjaincs/decoyd/internal/triglog"
)

// TestHybrid_O2_InotifySuppressedWhileFanotifyActive verifies that while a token's
// fanotify mark is marked active, inotify open/access events do not alert for
// that token, while modify events still fire and clearing the suppression
// restores inotify access alerts (O2 fallback).
func TestHybrid_O2_InotifySuppressedWhileFanotifyActive(t *testing.T) {
	dir := t.TempDir()
	dataDir := t.TempDir()

	decoyPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(decoyPath, []byte("TOKEN=abc\n"), 0o600); err != nil {
		t.Fatalf("write decoy: %v", err)
	}
	const tokID = "o2-suppressed-tok"
	if err := WriteDeployedSnapshot(dataDir, []DeployedToken{{
		ID:           tokID,
		Type:         "env_secrets",
		DeployedPath: decoyPath,
	}}); err != nil {
		t.Fatalf("WriteDeployedSnapshot: %v", err)
	}

	w := &linuxWatcher{
		dataDir: dataDir,
		cfg:     WatcherConfig{DebounceDuration: 50 * time.Millisecond, RateLimit: 10},
		inoFD:   -1,
		stopR:   -1,
		stopW:   -1,
	}
	w.setFanotifySuppressed(tokID, true)
	if err := w.start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer w.stop()

	// 1. Read while fanotify mark is active -> inotify open/access MUST be suppressed.
	if _, err := os.ReadFile(decoyPath); err != nil {
		t.Fatalf("read decoy: %v", err)
	}
	time.Sleep(180 * time.Millisecond)

	evs, err := triglog.Load(dataDir)
	if err != nil {
		t.Fatalf("triglog.Load: %v", err)
	}
	for _, ev := range evs {
		if ev.EventType == "access" {
			t.Fatalf("unexpected inotify access alert while fanotify mark is active: %+v", ev)
		}
	}

	// 2. Write while fanotify mark is active -> inotify still tracks modifications (O2).
	if err := os.WriteFile(decoyPath, []byte("TOKEN=def\n"), 0o600); err != nil {
		t.Fatalf("write decoy update: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	gotWrite := false
	for time.Now().Before(deadline) {
		evs, err = triglog.Load(dataDir)
		if err != nil {
			t.Fatalf("triglog.Load write: %v", err)
		}
		for _, ev := range evs {
			if ev.EventType == "write" {
				gotWrite = true
				break
			}
		}
		if gotWrite {
			break
		}
		time.Sleep(40 * time.Millisecond)
	}
	if !gotWrite {
		t.Fatal("expected inotify write event while fanotify suppressed only open/access")
	}

	// 3. Clear suppression (simulates fanotify fallback) -> inotify access resumes.
	w.setFanotifySuppressed(tokID, false)
	if _, err := os.ReadFile(decoyPath); err != nil {
		t.Fatalf("read decoy fallback: %v", err)
	}
	deadline = time.Now().Add(2 * time.Second)
	gotAccess := false
	for time.Now().Before(deadline) {
		evs, err = triglog.Load(dataDir)
		if err != nil {
			t.Fatalf("triglog.Load access: %v", err)
		}
		for _, ev := range evs {
			if ev.EventType == "access" {
				gotAccess = true
				break
			}
		}
		if gotAccess {
			break
		}
		time.Sleep(40 * time.Millisecond)
	}
	if !gotAccess {
		t.Fatal("expected inotify access alert once fanotify suppression was cleared")
	}
}

// TestO6_Tier1ErrorsWhenUnavailable verifies that WatchOptions{Tier1: true} fails
// cleanly instead of silently downgrading to Tier 2 when fanotify is unavailable.
func TestO6_Tier1ErrorsWhenUnavailable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Log("running as root; probing unavailable check via direct error condition skipped when root has CAP_SYS_ADMIN")
		return
	}
	dataDir := t.TempDir()
	_, err := NewWithOptions(nil, dataDir, WatchOptions{Tier1: true})
	if err == nil {
		t.Fatal("NewWithOptions(Tier1: true) succeeded without privileges; want O6 error")
	}
	if !strings.Contains(err.Error(), "--tier1") && !strings.Contains(err.Error(), "fanotify") {
		t.Errorf("error = %q; want mention of --tier1 / fanotify", err.Error())
	}

	// Without Tier1, must fall back cleanly to Tier 2.
	w, err := NewWithOptions(nil, dataDir, WatchOptions{Tier1: false})
	if err != nil {
		t.Fatalf("NewWithOptions(Tier1: false) failed: %v", err)
	}
	if w == nil {
		t.Fatal("expected non-nil watcher in Tier 2 fallback")
	}
}
