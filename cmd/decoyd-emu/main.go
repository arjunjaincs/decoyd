// Package main implements decoyd-emu, a benign read-only credential emulator.
//
// ╔═══════════════════════════════════════════════════════════════════════╗
// ║  BENIGN TEST TOOL — decoyd-emu                                       ║
// ║  This program reads credential files and prints their names and byte  ║
// ║  counts only. It does NOT exfiltrate, modify, or transmit any data.  ║
// ║  It is used exclusively to trigger Decoyd decoy file alerts in tests. ║
// ╚═══════════════════════════════════════════════════════════════════════╝
//
// Usage:
//
//	decoyd-emu [path ...]         Read the given paths.
//	decoyd-emu                    Read default credential locations.
//
// Output: one line per file: "name: N bytes" or "name: (not found)"
// No network access. No data transmission. Read-only.
package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// defaultPaths lists well-known credential file locations.
// These are the same paths that Decoyd monitors with decoy files.
var defaultPaths = []string{
	filepath.Join(homeDir(), ".npmrc"),
	filepath.Join(homeDir(), ".pypirc"),
	filepath.Join(homeDir(), ".docker", "config.json"),
	filepath.Join(homeDir(), ".git-credentials"),
	filepath.Join(homeDir(), ".netrc"),
	filepath.Join(homeDir(), ".ssh", "id_rsa"),
	filepath.Join(homeDir(), ".config", "gcloud", "application_default_credentials.json"),
	filepath.Join(homeDir(), ".terraform.d", "credentials.tfrc.json"),
	filepath.Join(homeDir(), ".vault-token"),
	filepath.Join(homeDir(), ".cursor", "mcp.json"),
}

func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

func main() {
	fmt.Println("=== decoyd-emu: BENIGN TEST TOOL — read-only credential location scanner ===")
	fmt.Println()

	paths := os.Args[1:]
	if len(paths) == 0 {
		paths = defaultPaths
	}

	for _, p := range paths {
		info, err := os.Lstat(p) // #nosec G703 -- benign test tool reading user-specified path
		if os.IsNotExist(err) {
			fmt.Printf("%-55s (not found)\n", filepath.Base(p))
			continue
		}
		if err != nil {
			fmt.Printf("%-55s (error: %v)\n", filepath.Base(p), err)
			continue
		}
		if !info.Mode().IsRegular() {
			fmt.Printf("%-55s (not a regular file — skip)\n", filepath.Base(p))
			continue
		}
		// Read the file to trigger a file-access event (open + read).
		// We discard the contents immediately — no data is stored or sent.
		data, err := os.ReadFile(p) // #nosec G703 G304 -- benign test tool reading user-specified path
		if err != nil {
			fmt.Printf("%-55s (read error: %v)\n", filepath.Base(p), err)
			continue
		}
		n := len(data)
		data = nil // discard immediately — we report byte count only
		fmt.Printf("%-55s %d bytes\n", filepath.Base(p), n)
	}

	fmt.Println()
	fmt.Println("=== decoyd-emu: scan complete ===")
}
