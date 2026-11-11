//go:build linux

// cmd_install_system_linux.go — system-wide installation for Tier 1 (root-only).
//
// Design (O3, O4, T6):
//   - Requires effective UID == 0 (root); errors immediately otherwise.
//   - Copies the binary to /usr/local/bin/decoyd (0755, root:root).
//   - Renders the system unit into /etc/systemd/system/decoyd.service.
//   - Creates /var/lib/decoyd/ (0755, root:root) for non-secret daemon state
//     (triggers.jsonl 0644 so unprivileged decoyd events can read it).
//   - Creates /etc/decoyd/ (0750, root:root) for root-only alert_config.json (0600).
//   - Never creates or writes into user --data-dir or $HOME (O4).
//   - sync: copies alert_config.json (0600) and optional deployed_tokens.json (0644) one-way.
//   - uninstall --system: stops/disables unit and removes unit, binary, /var/lib/decoyd, and /etc/decoyd.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// System installation paths (O3, O4).
const (
	systemBinPath     = "/usr/local/bin/decoyd"
	systemUnitPath    = "/etc/systemd/system/decoyd.service"
	systemDataDir     = "/var/lib/decoyd"
	systemTriggersDir = systemDataDir
	systemConfigDir   = "/etc/decoyd"
	systemAlertConfig = "/etc/decoyd/alert_config.json"
	systemTokensFile  = "/var/lib/decoyd/deployed_tokens.json"
	systemServiceName = "decoyd.service"
)

type systemInstallPaths struct {
	binPath     string
	unitPath    string
	dataDir     string
	configDir   string
	alertConfig string
	tokensFile  string
	serviceName string
}

func defaultSystemInstallPaths() systemInstallPaths {
	return systemInstallPaths{
		binPath:     systemBinPath,
		unitPath:    systemUnitPath,
		dataDir:     systemDataDir,
		configDir:   systemConfigDir,
		alertConfig: systemAlertConfig,
		tokensFile:  systemTokensFile,
		serviceName: systemServiceName,
	}
}

// systemUnitContent returns the rendered systemd unit for the system service.
// This is a pure function used in production and in golden-file tests (T6).
func systemUnitContent(execPath string) string {
	return fmt.Sprintf(`[Unit]
Description=Decoyd Canary Token Monitor (Tier 1)
Documentation=https://github.com/arjunjaincs/decoyd
After=network.target local-fs.target

[Service]
Type=simple
ExecStart=%s watch --tier1 --data-dir %s
Restart=on-failure
RestartSec=5s
User=root

# Capabilities required for Tier 1 fanotify-based monitoring (O3):
#   CAP_SYS_ADMIN:        fanotify_init(2) requires this for FAN_CLASS_NOTIF.
#   CAP_SYS_PTRACE:       read /proc/<pid>/exe for processes owned by other users.
#   CAP_DAC_READ_SEARCH:  traverse 0700 home directories to find token files.
CapabilityBoundingSet=CAP_SYS_ADMIN CAP_SYS_PTRACE CAP_DAC_READ_SEARCH
AmbientCapabilities=CAP_SYS_ADMIN CAP_SYS_PTRACE CAP_DAC_READ_SEARCH

# Security hardening.
NoNewPrivileges=yes
ProtectSystem=strict
ReadWritePaths=%s

# State and config directories (created by systemd if missing).
# StateDirectoryMode=0755 lets unprivileged 'decoyd events' read triggers.jsonl (0644, non-secret per O4).
StateDirectory=decoyd
StateDirectoryMode=0755
ConfigurationDirectory=decoyd
ConfigurationDirectoryMode=0750

[Install]
WantedBy=multi-user.target
`, execPath, systemDataDir, systemDataDir)
}

// cmdInstallSystem performs the system-wide installation.
// Called when "decoyd install --system [--data-dir DIR]" is used.
func cmdInstallSystem() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("decoyd install --system requires root privileges (run with sudo)")
	}

	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("resolve symlinks: %w", err)
	}

	return installSystemWith(defaultSystemInstallPaths(), execPath, true)
}

func installSystemWith(paths systemInstallPaths, execPath string, runSystemctl bool) error {
	if err := installBinary(execPath, paths.binPath); err != nil {
		return fmt.Errorf("install binary: %w", err)
	}
	fmt.Println("binary installed:", paths.binPath)

	unitContent := systemUnitContent(paths.binPath)
	// #nosec G301 -- systemd unit directory (/etc/systemd/system) must be 0755
	if err := os.MkdirAll(filepath.Dir(paths.unitPath), 0o755); err != nil {
		return fmt.Errorf("create unit dir: %w", err)
	}
	if err := os.WriteFile(paths.unitPath, []byte(unitContent), 0o644); err != nil { // #nosec G306 -- 0644 is standard for non-secret systemd .service unit files
		return fmt.Errorf("write unit file %s: %w", paths.unitPath, err)
	}
	fmt.Println("unit file written:", paths.unitPath)

	// #nosec G301 -- 0755 lets unprivileged decoyd events traverse /var/lib/decoyd to read triggers.jsonl (0644, non-secret per O4)
	if err := os.MkdirAll(paths.dataDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", paths.dataDir, err)
	}
	// Ensure mode even under restrictive root umask.
	_ = os.Chmod(paths.dataDir, 0o755) // #nosec G302 -- 0755 is required for unprivileged traversal to triggers.jsonl (O4/O8)
	fmt.Println("data directory created:", paths.dataDir)

	if err := os.MkdirAll(paths.configDir, 0o750); err != nil {
		return fmt.Errorf("create %s: %w", paths.configDir, err)
	}
	fmt.Println("config directory created:", paths.configDir)

	if runSystemctl {
		if err := runCmd("systemctl", "daemon-reload"); err != nil {
			fmt.Fprintln(os.Stderr, "warning: systemctl daemon-reload failed:", err)
			fmt.Fprintln(os.Stderr, "  Run 'sudo systemctl daemon-reload' manually.")
		}
		if err := runCmd("systemctl", "enable", "--now", paths.serviceName); err != nil {
			fmt.Fprintln(os.Stderr, "warning: systemctl enable failed:", err)
			fmt.Fprintln(os.Stderr, "  Run 'sudo systemctl enable --now decoyd.service' manually.")
		}
	}

	fmt.Println("\ndecoyd system service installed.")
	fmt.Println("Run 'sudo decoyd sync' to copy your alert configuration.")
	fmt.Println("Run 'journalctl -u decoyd.service -f' to follow logs.")
	return nil
}

// cmdSync copies the user-mode alert configuration one-way into /etc/decoyd/alert_config.json
// and optionally copies deployed_tokens.json into /var/lib/decoyd/deployed_tokens.json.
func cmdSync(userDataDir string) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("decoyd sync requires root privileges (run with sudo)")
	}
	return syncSystemWith(defaultSystemInstallPaths(), userDataDir, true)
}

func syncSystemWith(paths systemInstallPaths, userDataDir string, runSystemctl bool) error {
	srcPath := filepath.Join(userDataDir, "alert_config.json")
	err := copyFileAtomic(srcPath, paths.alertConfig, 0o600)
	if errors.Is(err, os.ErrNotExist) {
		legacyPath := filepath.Join(userDataDir, "alerts.json")
		if lErr := copyFileAtomic(legacyPath, paths.alertConfig, 0o600); lErr == nil {
			srcPath = legacyPath
			err = nil
		}
	}
	if err != nil {
		return fmt.Errorf("sync alert config from %s: %w", srcPath, err)
	}
	fmt.Println("alert config synced to", paths.alertConfig)

	srcTokens := filepath.Join(userDataDir, "deployed_tokens.json")
	if tErr := copyFileAtomic(srcTokens, paths.tokensFile, 0o644); tErr != nil && !errors.Is(tErr, os.ErrNotExist) {
		return fmt.Errorf("sync token snapshot from %s: %w", srcTokens, tErr)
	} else if tErr == nil {
		fmt.Println("deployed token snapshot synced to", paths.tokensFile)
	}

	if runSystemctl {
		if err := runCmd("systemctl", "try-restart", paths.serviceName); err != nil {
			fmt.Fprintln(os.Stderr, "warning: restart failed:", err)
			fmt.Fprintln(os.Stderr, "  Run 'sudo systemctl restart decoyd.service' manually.")
		}
	}
	return nil
}

// cmdUninstallSystem removes the system service, binary, and system directories
// (/var/lib/decoyd and /etc/decoyd) installed by cmdInstallSystem.
func cmdUninstallSystem() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("decoyd uninstall --system requires root privileges (run with sudo)")
	}
	return uninstallSystemWith(defaultSystemInstallPaths(), true)
}

func uninstallSystemWith(paths systemInstallPaths, runSystemctl bool) error {
	if runSystemctl {
		_ = runCmd("systemctl", "disable", "--now", paths.serviceName)
		_ = runCmd("systemctl", "daemon-reload")
	}

	for _, path := range []string{paths.unitPath, paths.binPath} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "warning: remove %s: %v\n", path, err)
		} else if err == nil {
			fmt.Println("removed:", path)
		}
	}

	for _, dir := range []string{paths.dataDir, paths.configDir} {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("remove %s: %w", dir, err)
		}
		fmt.Println("removed directory:", dir)
	}

	fmt.Println("decoyd system service uninstalled.")
	return nil
}

func copyFileAtomic(srcPath, dstPath string, mode os.FileMode) error {
	src, err := os.Open(srcPath) // #nosec G304 G703 -- srcPath is inside validated operator/user dataDir during explicit sync
	if err != nil {
		return err
	}
	defer src.Close()

	dstDir := filepath.Dir(dstPath)
	if err := os.MkdirAll(dstDir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dstDir, ".decoyd-sync-*")
	if err != nil {
		return err
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()

	if _, err := io.Copy(tmp, src); err != nil {
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dstPath)
}

// installBinary copies src to dst with 0755 permissions via atomic rename.
func installBinary(src, dst string) error {
	in, err := os.Open(src) // #nosec G304 -- src is os.Executable() path, not user input
	if err != nil {
		return err
	}
	defer in.Close()

	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil { // #nosec G301 -- target binary parent dir (/usr/local/bin)
		return fmt.Errorf("create parent dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".decoyd-install-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()

	if _, err := io.Copy(tmp, in); err != nil {
		return fmt.Errorf("copy binary: %w", err)
	}
	if err := tmp.Chmod(0o755); err != nil { // #nosec G302 -- installed executable must be 0755
		return fmt.Errorf("chmod binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return fmt.Errorf("install binary: %w", err)
	}
	return nil
}

// runCmd runs a system command and returns any error.
func runCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...) // #nosec G204 -- name is always a hardcoded system command
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
