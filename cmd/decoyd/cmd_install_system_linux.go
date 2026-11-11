//go:build linux

// cmd_install_system_linux.go — system-wide installation for Tier 1 (root-only).
//
// Design (O3, O4, T6):
//   - Requires effective UID == 0 (root); errors immediately otherwise.
//   - Copies the binary to /usr/local/bin/decoyd (0755, root:root).
//   - Renders the system unit from a pure function into /etc/systemd/system/decoyd.service.
//   - Creates /var/lib/decoyd/ (0700, root:root) for daemon state.
//   - Creates /etc/decoyd/ (0750, root:root) for config.
//   - Never writes into the user-supplied DIR (data dir is for user-mode only).
//   - sync: copies alert config one-way into /etc/decoyd/alerts.json (0600, root).
//   - uninstall --system: stops and removes the system service and binary.
package main

import (
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
	systemConfigDir   = "/etc/decoyd"
	systemAlertConfig = "/etc/decoyd/alerts.json"
)

// systemUnitContent returns the rendered systemd unit for the system service.
// This is a pure function used in production and in golden-file tests (T6).
// Fields in the unit satisfy O3 (CapabilityBoundingSet) and O4 (data/config dirs).
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
StateDirectory=decoyd
StateDirectoryMode=0700
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

	// Resolve the running binary path.
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("resolve symlinks: %w", err)
	}

	// Copy binary to /usr/local/bin/decoyd.
	if err := installBinary(execPath, systemBinPath); err != nil {
		return fmt.Errorf("install binary: %w", err)
	}
	fmt.Println("binary installed:", systemBinPath)

	// Render unit file.
	unitContent := systemUnitContent(systemBinPath)
	if err := os.WriteFile(systemUnitPath, []byte(unitContent), 0o644); err != nil { // #nosec G306 -- 0644 is correct for a systemd .service file (non-secret unit config)
		return fmt.Errorf("write unit file %s: %w", systemUnitPath, err)
	}
	fmt.Println("unit file written:", systemUnitPath)

	// Create /var/lib/decoyd/ (0700, root).
	if err := os.MkdirAll(systemDataDir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", systemDataDir, err)
	}
	fmt.Println("data directory created:", systemDataDir)

	// Create /etc/decoyd/ (0750, root).
	if err := os.MkdirAll(systemConfigDir, 0o750); err != nil {
		return fmt.Errorf("create %s: %w", systemConfigDir, err)
	}
	fmt.Println("config directory created:", systemConfigDir)

	// Reload systemd and enable the service.
	if err := runCmd("systemctl", "daemon-reload"); err != nil {
		fmt.Fprintln(os.Stderr, "warning: systemctl daemon-reload failed:", err)
		fmt.Fprintln(os.Stderr, "  Run 'sudo systemctl daemon-reload' manually.")
	}
	if err := runCmd("systemctl", "enable", "--now", "decoyd.service"); err != nil {
		fmt.Fprintln(os.Stderr, "warning: systemctl enable failed:", err)
		fmt.Fprintln(os.Stderr, "  Run 'sudo systemctl enable --now decoyd.service' manually.")
	}

	fmt.Println("\ndecoyd system service installed.")
	fmt.Println("Run 'sudo decoyd sync' to copy your alert configuration.")
	fmt.Println("Run 'journalctl -u decoyd.service -f' to follow logs.")
	return nil
}

// cmdSync copies the user-mode alert configuration into /etc/decoyd/alerts.json.
// This is a one-way copy; the daemon reads from /etc/decoyd/ only.
func cmdSync(userDataDir string) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("decoyd sync requires root privileges (run with sudo)")
	}

	srcPath := filepath.Join(userDataDir, "alerts.json")
	src, err := os.Open(srcPath) // #nosec G304 -- srcPath is filepath.Join(userDataDir, "alerts.json")
	if err != nil {
		return fmt.Errorf("open source config %s: %w", srcPath, err)
	}
	defer src.Close()

	// Write to /etc/decoyd/alerts.json with 0600 (root-only readable).
	dst, err := os.OpenFile(systemAlertConfig, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) // #nosec G304 -- systemAlertConfig is a constant root-owned /etc path
	if err != nil {
		return fmt.Errorf("write %s: %w", systemAlertConfig, err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("copy config: %w", err)
	}
	_ = dst.Close()

	fmt.Println("alert config synced to", systemAlertConfig)
	if err := runCmd("systemctl", "try-restart", "decoyd.service"); err != nil {
		fmt.Fprintln(os.Stderr, "warning: restart failed:", err)
		fmt.Fprintln(os.Stderr, "  Run 'sudo systemctl restart decoyd.service' manually.")
	}
	return nil
}

// cmdUninstallSystem removes the system service installed by cmdInstallSystem.
func cmdUninstallSystem() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("decoyd uninstall --system requires root privileges (run with sudo)")
	}

	_ = runCmd("systemctl", "disable", "--now", "decoyd.service")
	_ = runCmd("systemctl", "daemon-reload")

	for _, path := range []string{systemUnitPath, systemBinPath} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "warning: remove %s: %v\n", path, err)
		} else if err == nil {
			fmt.Println("removed:", path)
		}
	}

	fmt.Println("decoyd system service uninstalled.")
	fmt.Println("Note: /var/lib/decoyd/ and /etc/decoyd/ are preserved. Remove manually if desired.")
	return nil
}

// installBinary copies src to dst (O_CREATE|O_EXCL) with 0755 permissions.
// If dst already exists, it is replaced atomically via a temp file.
func installBinary(src, dst string) error {
	in, err := os.Open(src) // #nosec G304 -- src is os.Executable() path, not user input
	if err != nil {
		return err
	}
	defer in.Close()

	dir := filepath.Dir(dst)
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
	if err := tmp.Chmod(0o755); err != nil {
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
