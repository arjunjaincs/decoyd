//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"
)

const systemdUnitTemplate = `[Unit]
Description=Decoyd Canary Token Monitor
Documentation=https://github.com/arjunjaincs/decoyd
After=network.target

[Service]
Type=simple
ExecStart={{.ExecPath}} watch
Restart=on-failure
RestartSec=5s
# Keep the bbolt database accessible to the running user only.
UMask=0077
# Security hardening: directives that work without user namespaces.
# ProtectSystem/PrivateTmp require user namespaces (disabled in many
# WSL and container environments) and are intentionally omitted.
NoNewPrivileges=yes
LockPersonality=yes
RestrictRealtime=yes
RestrictSUIDSGID=yes
SystemCallArchitectures=native
MemoryDenyWriteExecute=yes

[Install]
WantedBy=default.target
`

// cmdInstall writes and enables a systemd user service unit for
// 'decoyd watch'. Requires systemd ≥ 236 (user units).
func cmdInstall(dataDir string) error {
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("resolve symlinks: %w", err)
	}

	// User systemd unit directory.
	unitDir := filepath.Join(os.Getenv("HOME"), ".config", "systemd", "user")
	if err := os.MkdirAll(unitDir, 0o700); err != nil { // #nosec G703 -- unitDir is $HOME/.config/systemd/user for user-scoped systemd unit
		return fmt.Errorf("create systemd user dir: %w", err)
	}
	unitPath := filepath.Join(unitDir, "decoyd.service")

	f, err := os.OpenFile(unitPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) // #nosec G304 G703 -- unitPath is $HOME/.config/systemd/user/decoyd.service with fixed filename
	if err != nil {
		return fmt.Errorf("write unit file: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = f.Close()
		}
	}()

	tmpl := template.Must(template.New("unit").Parse(systemdUnitTemplate))
	if err := tmpl.Execute(f, struct{ ExecPath string }{ExecPath: quoteExecStart(execPath)}); err != nil {
		return fmt.Errorf("render unit: %w", err)
	}
	closed = true
	if err := f.Close(); err != nil {
		return fmt.Errorf("close unit file: %w", err)
	}

	fmt.Println("unit file written:", unitPath)

	// Enable and start the service via systemctl.
	for _, args := range [][]string{
		{"--user", "daemon-reload"},
		{"--user", "enable", "--now", "decoyd.service"},
	} {
		cmd := exec.Command("systemctl", args...) // #nosec G204 -- fixed arguments, no user input
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("systemctl %v: %w", args, err)
		}
	}

	fmt.Println("decoyd watch is now running as a systemd user service.")
	fmt.Println("Check status:  systemctl --user status decoyd.service")
	fmt.Println("Stop:          systemctl --user stop decoyd.service")
	fmt.Println("Disable:       systemctl --user disable decoyd.service")
	return nil
}
