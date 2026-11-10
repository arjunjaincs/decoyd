//go:build linux

package main

import (
	"strings"
	"testing"
)

// TestSystemUnitTemplate_Render verifies the system unit contains the required
// security directives (O3: CapabilityBoundingSet; NoNewPrivileges; ExecStart path).
func TestSystemUnitTemplate_Render(t *testing.T) {
	const testExecPath = "/usr/local/bin/decoyd"
	unit := systemUnitContent(testExecPath)

	requiredStrings := []struct {
		needle string
		label  string
	}{
		{"CapabilityBoundingSet=CAP_SYS_ADMIN CAP_SYS_PTRACE CAP_DAC_READ_SEARCH", "CapabilityBoundingSet"},
		{"AmbientCapabilities=CAP_SYS_ADMIN CAP_SYS_PTRACE CAP_DAC_READ_SEARCH", "AmbientCapabilities"},
		{"NoNewPrivileges=yes", "NoNewPrivileges"},
		{"ProtectSystem=strict", "ProtectSystem"},
		{testExecPath + " watch --tier1", "ExecStart path with --tier1"},
		{"WantedBy=multi-user.target", "multi-user.target"},
		{"/var/lib/decoyd", "ReadWritePaths"},
	}

	for _, req := range requiredStrings {
		if !strings.Contains(unit, req.needle) {
			t.Errorf("unit missing %s: want %q in:\n%s", req.label, req.needle, unit)
		}
	}
}

// TestSystemUnitTemplate_NoPERMCapabilities ensures the unit does not request
// capabilities beyond the approved set.
func TestSystemUnitTemplate_NoPERMCapabilities(t *testing.T) {
	unit := systemUnitContent("/usr/local/bin/decoyd")

	bannedCaps := []string{
		"CAP_NET_ADMIN",
		"CAP_SYS_MODULE",
		"CAP_SETUID",
		"CAP_SETGID",
		"CAP_CHOWN",
	}
	for _, cap := range bannedCaps {
		if strings.Contains(unit, cap) {
			t.Errorf("unit contains unapproved capability %q", cap)
		}
	}
}

// TestSystemUnitTemplate_DataDirIsSystemDataDir ensures the unit uses the
// correct system data directory (not a user-writable path).
func TestSystemUnitTemplate_DataDirIsSystemDataDir(t *testing.T) {
	unit := systemUnitContent("/usr/local/bin/decoyd")

	// Must reference /var/lib/decoyd, not any user home directory.
	if !strings.Contains(unit, systemDataDir) {
		t.Errorf("unit does not reference systemDataDir %q", systemDataDir)
	}
	// Must NOT reference /home or /root as the data directory.
	for _, userPath := range []string{"/home/", "$HOME", "~/"} {
		if strings.Contains(unit, "--data-dir "+userPath) {
			t.Errorf("unit references user path %q for data-dir", userPath)
		}
	}
}
