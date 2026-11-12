//go:build linux && privileged

package watch

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arjunjaincs/decoyd/internal/triglog"
)

// TestFanotify_Privileged_ChildCatReadsDecoy starts a real Tier 1 fanotify watcher,
// reads a decoy from a child `cat` process, and asserts triggers.jsonl records an
// event with WatcherTier == "tier1", Attribution.Accessor.PID == child PID,
// and Exe ending in "/cat" plus npm_package_name when set in child env. (T2/F5)
func TestFanotify_Privileged_ChildCatReadsDecoy(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Fatalf("privileged test requires root/CAP_SYS_ADMIN (euid=%d)", os.Geteuid())
	}

	dir := t.TempDir()
	dataDir := t.TempDir()
	decoyPath := filepath.Join(dir, ".npmrc")
	if err := os.WriteFile(decoyPath, []byte("//registry.npmjs.org/:_authToken=testvalue\n"), 0o600); err != nil {
		t.Fatalf("write decoy: %v", err)
	}
	if err := WriteDeployedSnapshot(dataDir, []DeployedToken{{
		ID:           "priv-cat-tok",
		Type:         "npmrc",
		DeployedPath: decoyPath,
	}}); err != nil {
		t.Fatalf("WriteDeployedSnapshot: %v", err)
	}

	w, err := NewWithOptions(nil, dataDir, WatchOptions{Tier1: true})
	if err != nil {
		t.Fatalf("NewWithOptions(Tier1: true): %v", err)
	}
	if err := w.Start(); err != nil {
		t.Fatalf("w.Start(): %v", err)
	}
	defer w.Stop()

	if st := w.Status(); st.Tier != "tier1" {
		t.Fatalf("Status().Tier = %q; want tier1", st.Tier)
	}

	// Give poll loops a moment to enter Poll.
	time.Sleep(150 * time.Millisecond)

	// Child process `cat` reads the decoy with npm lifecycle env variables, then waits on stdin ("-")
	// until the async fanotify worker persists the attributed event so /proc/<pid>/environ stays readable.
	cmd := exec.Command("cat", decoyPath, "-")
	pw, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
	}
	cmd.Env = append(os.Environ(),
		"npm_package_name=evil-test-pkg",
		"npm_package_version=1.2.3",
		"npm_lifecycle_event=postinstall",
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start cat: %v", err)
	}
	var waited bool
	t.Cleanup(func() {
		if !waited {
			_ = pw.Close()
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		}
	})
	childPID, ok := safeIntToInt32(cmd.Process.Pid)
	if !ok {
		t.Fatalf("child pid %d out of int32 range", cmd.Process.Pid)
	}

	var matched *triglog.TriggerEvent
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		evs, err := triglog.Load(dataDir)
		if err != nil {
			t.Fatalf("triglog.Load: %v", err)
		}
		for i := range evs {
			if evs[i].TokenID == "priv-cat-tok" && evs[i].WatcherTier == "tier1" {
				matched = &evs[i]
				break
			}
		}
		if matched != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if err := pw.Close(); err != nil {
		t.Fatalf("close cat stdin: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait cat: %v", err)
	}
	waited = true

	if matched == nil {
		t.Fatal("timed out waiting for tier1 trigger event from child cat")
	}
	if matched.Attribution == nil {
		t.Fatalf("matched.Attribution is nil: %+v", *matched)
	}
	if matched.Attribution.Accessor.PID != childPID {
		t.Errorf("event PID = %d; want child PID %d", matched.Attribution.Accessor.PID, childPID)
	}
	if !strings.HasSuffix(matched.Attribution.Accessor.Exe, "/cat") {
		t.Errorf("event Exe = %q; want suffix /cat", matched.Attribution.Accessor.Exe)
	}
	if matched.Attribution.NpmPackage != "evil-test-pkg" {
		t.Errorf("event NpmPackage = %q; want evil-test-pkg", matched.Attribution.NpmPackage)
	}

	// O2 verification on live hybrid watcher: wait > 2s inotify debounce and confirm no tier2 access event leaked.
	time.Sleep(2300 * time.Millisecond)
	allEvs, err := triglog.Load(dataDir)
	if err != nil {
		t.Fatalf("triglog.Load final: %v", err)
	}
	for _, ev := range allEvs {
		if ev.EventType == "access" && ev.WatcherTier != "tier1" {
			t.Fatalf("O2 violation: found non-tier1 access event %+v while fanotify mark active", ev)
		}
	}
}
