// Package main — decoyd doctor subcommand.
// Runs a series of platform-independent health checks and prints a report.
// Use doctorCheckFSType and doctorCheckInotify for platform-specific probes
// implemented in cmd_doctor_linux.go / cmd_doctor_windows.go.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/arjunjaincs/decoyd/internal/alert"
	"github.com/arjunjaincs/decoyd/internal/store"
	"github.com/arjunjaincs/decoyd/internal/watch"
)

// doctorResult is a single check result.
type doctorResult struct {
	Check   string `json:"check"`
	Status  string `json:"status"` // "ok", "warn", "fail"
	Message string `json:"message,omitempty"`
}

// cmdDoctor runs all health checks and prints a report.
// --json flag emits JSON; default is human-readable.
// Exit codes: 0=all ok, 1=warnings, 2=failures.
func cmdDoctor(dataDir string, args []string) error {
	jsonOut := false
	for _, a := range args {
		if a == "--json" || a == "-json" {
			jsonOut = true
		}
	}

	var results []doctorResult
	add := func(r doctorResult) { results = append(results, r) }

	// --- Platform ---
	add(doctorResult{
		Check:  "platform",
		Status: "ok",
		Message: fmt.Sprintf("GOOS=%s GOARCH=%s go=%s",
			runtime.GOOS, runtime.GOARCH, runtime.Version()),
	})

	// --- Data directory ---
	fi, err := os.Stat(dataDir)
	if err != nil {
		add(doctorResult{Check: "data_dir", Status: "fail",
			Message: "cannot access: " + err.Error()})
	} else {
		perm := fi.Mode().Perm()
		if perm&0o077 != 0 {
			add(doctorResult{Check: "data_dir", Status: "warn",
				Message: fmt.Sprintf("permissions %04o — should be 0700", perm)})
		} else {
			add(doctorResult{Check: "data_dir", Status: "ok",
				Message: dataDir})
		}
	}

	// --- DB lock status ---
	dbPath := filepath.Join(dataDir, "decoyd.db")
	func() {
		st, dbErr := store.Open(dbPath)
		if dbErr != nil {
			add(doctorResult{Check: "db_lock", Status: "warn",
				Message: "database appears locked (another process may be running): " + dbErr.Error()})
			return
		}
		st.Close()
		add(doctorResult{Check: "db_lock", Status: "ok", Message: "database not locked"})
	}()

	// --- Snapshot validity ---
	snapPath := filepath.Join(dataDir, "deployed_tokens.json")
	if _, snapErr := os.Stat(snapPath); os.IsNotExist(snapErr) {
		add(doctorResult{Check: "snapshot", Status: "warn",
			Message: "deployed_tokens.json not found — no tokens deployed yet or watcher not run"})
	} else {
		tokens, snapErr := watch.ReadDeployedSnapshot(dataDir)
		if snapErr != nil {
			add(doctorResult{Check: "snapshot", Status: "fail",
				Message: "cannot parse deployed_tokens.json: " + snapErr.Error()})
		} else {
			add(doctorResult{Check: "snapshot", Status: "ok",
				Message: fmt.Sprintf("%d token(s) in snapshot", len(tokens))})

			// --- Per-decoy checks ---
			for _, tok := range tokens {
				doctorCheckDecoy(dataDir, tok, add)
			}
		}
	}

	// --- Alert config ---
	alertCfg, alertErr := alert.Load(dataDir)
	if alertErr != nil {
		add(doctorResult{Check: "alert_config", Status: "fail",
			Message: "cannot load alert_config.json: " + alertErr.Error()})
	} else if len(alertCfg.Channels) == 0 {
		add(doctorResult{Check: "alert_config", Status: "warn",
			Message: "no alert channels configured — run the TUI to add one"})
	} else {
		add(doctorResult{Check: "alert_config", Status: "ok",
			Message: fmt.Sprintf("%d channel(s), defaultID=%q", len(alertCfg.Channels), alertCfg.DefaultID)})
	}

	// --- Platform-specific probes (inotify / detection tier) ---
	doctorPlatformChecks(add)

	// --- Output ---
	var maxSev int // 0=ok, 1=warn, 2=fail
	for _, r := range results {
		switch r.Status {
		case "warn":
			if maxSev < 1 {
				maxSev = 1
			}
		case "fail":
			if maxSev < 2 {
				maxSev = 2
			}
		}
	}

	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		type report struct {
			GeneratedAt string         `json:"generated_at"`
			ExitCode    int            `json:"exit_code"`
			Results     []doctorResult `json:"results"`
		}
		return enc.Encode(report{
			GeneratedAt: time.Now().UTC().Format(time.RFC3339),
			ExitCode:    maxSev,
			Results:     results,
		})
	}

	// Human-readable.
	icons := map[string]string{"ok": "✓", "warn": "⚠", "fail": "✗"}
	for _, r := range results {
		icon := icons[r.Status]
		if r.Message != "" {
			fmt.Printf("  %s  %-24s %s\n", icon, r.Check, r.Message)
		} else {
			fmt.Printf("  %s  %s\n", icon, r.Check)
		}
	}
	fmt.Println()
	switch maxSev {
	case 0:
		fmt.Println("All checks passed.")
	case 1:
		fmt.Println("Some warnings found.")
	case 2:
		fmt.Println("Some failures found.")
	}

	if maxSev == 2 {
		os.Exit(2)
	} else if maxSev == 1 {
		os.Exit(1)
	}
	return nil
}

// doctorCheckDecoy runs per-decoy file checks (existence, permissions, fstype).
func doctorCheckDecoy(dataDir string, tok watch.DeployedToken, add func(doctorResult)) {
	check := "decoy:" + tok.ID
	fi, err := os.Lstat(tok.DeployedPath)
	if err != nil {
		add(doctorResult{Check: check, Status: "fail",
			Message: "file not found: " + tok.DeployedPath})
		return
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		add(doctorResult{Check: check, Status: "warn",
			Message: "deployed path is a symlink: " + tok.DeployedPath})
		return
	}
	perm := fi.Mode().Perm()
	if perm&0o022 != 0 {
		add(doctorResult{Check: check, Status: "warn",
			Message: fmt.Sprintf("permissions %04o on %s — world/group writable", perm, tok.DeployedPath)})
	} else {
		msg := tok.DeployedPath
		// Platform-specific filesystem type probe.
		if fstype, fsErr := doctorFSType(tok.DeployedPath); fsErr == nil {
			msg += fmt.Sprintf(" [fs: %s]", fstype)
			if isFSUnsupported(fstype) {
				add(doctorResult{Check: check, Status: "warn",
					Message: fmt.Sprintf("filesystem %q on %s may not support inotify/fsnotify reliably", fstype, tok.DeployedPath)})
				return
			}
		}
		add(doctorResult{Check: check, Status: "ok", Message: msg})
	}
}

// isFSUnsupported returns true for filesystems where read-event inotify
// is known to be unreliable: Plan 9 (9p), WSL drvfs, overlay, tmpfs.
func isFSUnsupported(fstype string) bool {
	switch fstype {
	case "9p", "drvfs", "overlay", "overlayfs", "tmpfs":
		return true
	}
	return false
}
