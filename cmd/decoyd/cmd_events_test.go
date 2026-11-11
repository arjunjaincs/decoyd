package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arjunjaincs/decoyd/internal/attribution"
	"github.com/arjunjaincs/decoyd/internal/triglog"
)

func TestParseGlobalFlags_DataDir(t *testing.T) {
	dir, explicit, rest, err := parseGlobalFlags([]string{"watch", "--tier1", "--data-dir", "/tmp/decoyd-test"})
	if err != nil {
		t.Fatalf("parseGlobalFlags: %v", err)
	}
	if !explicit || dir != filepath.Clean("/tmp/decoyd-test") {
		t.Fatalf("got (%q, %v); want (%q, true)", dir, explicit, filepath.Clean("/tmp/decoyd-test"))
	}
	if len(rest) != 2 || rest[0] != "watch" || rest[1] != "--tier1" {
		t.Fatalf("rest = %v; want [watch --tier1]", rest)
	}

	dir2, exp2, rest2, err := parseGlobalFlags([]string{"--data-dir=/tmp/d2", "events", "--json"})
	if err != nil {
		t.Fatalf("parseGlobalFlags = syntax: %v", err)
	}
	if !exp2 || dir2 != filepath.Clean("/tmp/d2") || len(rest2) != 2 {
		t.Fatalf("got (%q, %v, %v)", dir2, exp2, rest2)
	}

	if _, _, _, err := parseGlobalFlags([]string{"watch", "--data-dir"}); err == nil {
		t.Fatal("expected error for trailing --data-dir without argument")
	}
}

func TestCmdEvents_SystemDirFallbackWithoutHomeDir(t *testing.T) {
	userDir := filepath.Join(t.TempDir(), "nonexistent-user-decoyd")
	sysDir := t.TempDir()

	ev := triglog.TriggerEvent{
		ID:          "ev-sys-1",
		TokenID:     "tok-sys",
		TokenType:   "npmrc",
		Path:        "/tmp/.npmrc",
		EventType:   "access",
		WatcherTier: "tier1",
		Status:      triglog.TriggerSent,
		TriggeredAt: time.Now().UTC(),
		Attribution: &attribution.Attribution{
			Accessor: attribution.ProcessInfo{
				PID: 4242,
				Exe: "/usr/bin/cat",
			},
			NpmPackage: "evil-test-pkg",
		},
	}
	if err := triglog.Append(sysDir, ev); err != nil {
		t.Fatalf("triglog.Append: %v", err)
	}

	out := captureStdout(t, func() {
		if err := cmdEventsWithOptions(userDir, sysDir, true, []string{"--json"}); err != nil {
			t.Fatalf("cmdEventsWithOptions: %v", err)
		}
	})
	for _, want := range []string{"ev-sys-1", "4242", "/usr/bin/cat", "evil-test-pkg"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(userDir); !os.IsNotExist(err) {
		t.Fatalf("cmdEventsWithOptions created user dir %s; must remain non-existent", userDir)
	}
}
