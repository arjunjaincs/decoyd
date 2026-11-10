//go:build !linux

// cmd_install_system_notlinux.go — stubs for system-install commands on
// non-Linux platforms. System installation (Tier 1, systemd) is Linux-only.
package main

import "fmt"

func cmdInstallSystem() error {
	return fmt.Errorf("decoyd install --system is only supported on Linux")
}

func cmdUninstallSystem() error {
	return fmt.Errorf("decoyd uninstall --system is only supported on Linux")
}

func cmdSync(_ string) error {
	return fmt.Errorf("decoyd sync is only supported on Linux")
}
