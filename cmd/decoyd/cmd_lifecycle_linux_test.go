//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestO4_SystemInstallNeverWritesUserDirOrHome(t *testing.T) {
	fakeHome := t.TempDir()
	userDataDir := t.TempDir()
	t.Setenv("HOME", fakeHome)

	// Even failed non-root `install --system --data-dir <dir>` must not create anything in $HOME or userDataDir.
	if err := run([]string{"install", "--system", "--data-dir", userDataDir}); err == nil {
		t.Fatal("expected error for non-root install --system")
	}
	if entries, err := os.ReadDir(fakeHome); err != nil || len(entries) != 0 {
		t.Fatalf("install --system touched HOME (%v): %v", err, entries)
	}
	if entries, err := os.ReadDir(userDataDir); err != nil || len(entries) != 0 {
		t.Fatalf("install --system touched userDataDir (%v): %v", err, entries)
	}

	// Simulate full install -> sync -> uninstall inside isolated temp system roots.
	sysRoot := t.TempDir()
	paths := systemInstallPaths{
		binPath:     filepath.Join(sysRoot, "usr/local/bin/decoyd"),
		unitPath:    filepath.Join(sysRoot, "etc/systemd/system/decoyd.service"),
		dataDir:     filepath.Join(sysRoot, "var/lib/decoyd"),
		configDir:   filepath.Join(sysRoot, "etc/decoyd"),
		alertConfig: filepath.Join(sysRoot, "etc/decoyd/alert_config.json"),
		tokensFile:  filepath.Join(sysRoot, "var/lib/decoyd/deployed_tokens.json"),
		serviceName: "decoyd.service",
	}

	fakeBin := filepath.Join(t.TempDir(), "decoyd-bin")
	if err := os.WriteFile(fakeBin, []byte("binary"), 0o755); err != nil {
		t.Fatalf("write fakeBin: %v", err)
	}

	if err := installSystemWith(paths, fakeBin, false); err != nil {
		t.Fatalf("installSystemWith: %v", err)
	}
	if st, err := os.Stat(paths.dataDir); err != nil || st.Mode().Perm() != 0o755 {
		t.Fatalf("dataDir perm = %v (err=%v); want 0755 for O4 unprivileged events read", st, err)
	}
	if entries, err := os.ReadDir(fakeHome); err != nil || len(entries) != 0 {
		t.Fatalf("installSystemWith touched HOME: %v", entries)
	}
	if entries, err := os.ReadDir(userDataDir); err != nil || len(entries) != 0 {
		t.Fatalf("installSystemWith touched userDataDir: %v", entries)
	}

	// Populate userDataDir for sync, then verify O4 one-way sync + no mutations back.
	alertJSON := `{"channels":[{"id":"wh","type":"webhook","webhook_url":"http://127.0.0.1:9/hook","enabled":true}]}`
	snapJSON := `[{"id":"tok1","type":"npmrc","deployed_path":"/tmp/x"}]`
	if err := os.WriteFile(filepath.Join(userDataDir, "alert_config.json"), []byte(alertJSON), 0o600); err != nil {
		t.Fatalf("write user alert config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userDataDir, "deployed_tokens.json"), []byte(snapJSON), 0o600); err != nil {
		t.Fatalf("write user snapshot: %v", err)
	}

	if err := syncSystemWith(paths, userDataDir, false); err != nil {
		t.Fatalf("syncSystemWith: %v", err)
	}
	if st, err := os.Stat(paths.alertConfig); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("synced alertConfig mode = %v (err=%v); want 0600", st, err)
	}
	if st, err := os.Stat(paths.tokensFile); err != nil || st.Mode().Perm() != 0o644 {
		t.Fatalf("synced tokensFile mode = %v (err=%v); want 0644", st, err)
	}
	if entries, err := os.ReadDir(fakeHome); err != nil || len(entries) != 0 {
		t.Fatalf("syncSystemWith touched HOME: %v", entries)
	}

	// Uninstall must remove unit, binary, dataDir, and configDir cleanly.
	if err := uninstallSystemWith(paths, false); err != nil {
		t.Fatalf("uninstallSystemWith: %v", err)
	}
	for _, p := range []string{paths.unitPath, paths.binPath, paths.dataDir, paths.configDir} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("path still exists after uninstall: %s", p)
		}
	}
}

func TestO6_CmdWatchTier1ErrorsWhenUnavailable(t *testing.T) {
	if os.Geteuid() == 0 {
		return
	}
	dataDir := t.TempDir()
	err := cmdWatch(dataDir, true)
	if err == nil {
		t.Fatal("cmdWatch(dataDir, true) succeeded without privileges; want O6 error")
	}
	if !strings.Contains(err.Error(), "--tier1") && !strings.Contains(err.Error(), "fanotify") {
		t.Errorf("err = %q; want mention of --tier1 / fanotify", err.Error())
	}
}
