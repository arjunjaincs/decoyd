// Decoyd — self-hosted canary token generator and monitor.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"text/tabwriter"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/arjunjaincs/decoyd/internal/config"
	"github.com/arjunjaincs/decoyd/internal/store"
	"github.com/arjunjaincs/decoyd/internal/triglog"
	"github.com/arjunjaincs/decoyd/internal/tui"
	"github.com/arjunjaincs/decoyd/internal/watch"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "decoyd: "+err.Error())
		os.Exit(1)
	}
}

func cleanExplicitDataDir(raw string) (string, error) {
	if raw == "" || strings.ContainsRune(raw, 0) {
		return "", fmt.Errorf("--data-dir requires a valid directory path")
	}
	cleaned := filepath.Clean(raw)
	for _, part := range strings.Split(filepath.ToSlash(cleaned), "/") {
		if part == ".." {
			return "", fmt.Errorf("--data-dir must not contain '..' traversal segments: %q", raw)
		}
	}
	abs, err := filepath.Abs(cleaned)
	if err != nil {
		return "", fmt.Errorf("--data-dir %q: %w", raw, err)
	}
	return abs, nil
}

func parseGlobalFlags(args []string) (dataDir string, explicit bool, rest []string, err error) {
	rest = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--data-dir":
			i++
			if i >= len(args) || args[i] == "" {
				return "", false, nil, fmt.Errorf("--data-dir requires a directory argument")
			}
			abs, cerr := cleanExplicitDataDir(args[i])
			if cerr != nil {
				return "", false, nil, cerr
			}
			dataDir = abs
			explicit = true
		case strings.HasPrefix(a, "--data-dir="):
			val := strings.TrimPrefix(a, "--data-dir=")
			abs, cerr := cleanExplicitDataDir(val)
			if cerr != nil {
				return "", false, nil, cerr
			}
			dataDir = abs
			explicit = true
		default:
			rest = append(rest, a)
		}
	}
	return dataDir, explicit, rest, nil
}

// userDataDirPathNoCreate resolves the user's data directory path without
// creating it on disk (used by read-only commands and O4-compliant flows).
func userDataDirPathNoCreate() (string, error) {
	if runtime.GOOS == "windows" {
		base, err := os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("cannot locate AppData directory: %w", err)
		}
		return filepath.Join(base, config.AppName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot locate home directory: %w", err)
	}
	return filepath.Join(home, config.AppNameLower), nil
}

func ensureWritableDataDir(explicitDir string, explicit bool) (string, error) {
	if !explicit {
		return config.DataDir()
	}
	// #nosec G703 -- explicitDir is validated by cleanExplicitDataDir (operator CLI --data-dir flag)
	if err := os.MkdirAll(explicitDir, 0o700); err != nil {
		return "", fmt.Errorf("cannot create data directory %s: %w", explicitDir, err)
	}
	return explicitDir, nil
}

func run(args []string) error {
	dataDir, explicitDir, rest, err := parseGlobalFlags(args)
	if err != nil {
		return err
	}

	// Commands that MUST NOT open the bbolt store (no exclusive lock) and
	// system/read-only commands that must not touch $HOME/.decoyd (O4).
	if len(rest) > 0 {
		switch rest[0] {
		case "help", "--help", "-h":
			printUsage()
			return nil
		case "install":
			for _, a := range rest[1:] {
				if a == "--system" {
					// O4: system install never creates or writes $HOME/.decoyd or --data-dir.
					return cmdInstallSystem()
				}
			}
			writableDir, dErr := ensureWritableDataDir(dataDir, explicitDir)
			if dErr != nil {
				return dErr
			}
			return cmdInstall(writableDir)
		case "uninstall":
			for _, a := range rest[1:] {
				if a == "--system" {
					// O4: system uninstall never creates or writes $HOME/.decoyd or --data-dir.
					return cmdUninstallSystem()
				}
			}
			writableDir, dErr := ensureWritableDataDir(dataDir, explicitDir)
			if dErr != nil {
				return dErr
			}
			return cmdUninstall(writableDir)
		case "sync":
			srcDir := dataDir
			if !explicitDir {
				srcDir, err = userDataDirPathNoCreate()
				if err != nil {
					return fmt.Errorf("cannot resolve config directory: %w", err)
				}
			}
			return cmdSync(srcDir)
		case "watch":
			tier1 := false
			for _, a := range rest[1:] {
				if a == "--tier1" {
					tier1 = true
				}
			}
			writableDir, dErr := ensureWritableDataDir(dataDir, explicitDir)
			if dErr != nil {
				return dErr
			}
			return cmdWatch(writableDir, tier1)
		case "triggers":
			writableDir, dErr := ensureWritableDataDir(dataDir, explicitDir)
			if dErr != nil {
				return dErr
			}
			return cmdTriggers(writableDir)
		case "events":
			if explicitDir {
				return cmdEvents(dataDir, rest[1:])
			}
			userDir, dErr := userDataDirPathNoCreate()
			if dErr != nil {
				return fmt.Errorf("cannot resolve config directory: %w", dErr)
			}
			return cmdEventsWithOptions(userDir, systemTriggersDir, true, rest[1:])
		case "test":
			writableDir, dErr := ensureWritableDataDir(dataDir, explicitDir)
			if dErr != nil {
				return dErr
			}
			return cmdSelfTest(writableDir)
		case "doctor":
			writableDir, dErr := ensureWritableDataDir(dataDir, explicitDir)
			if dErr != nil {
				return dErr
			}
			return cmdDoctor(writableDir, rest[1:])
		}
	}

	writableDir, err := ensureWritableDataDir(dataDir, explicitDir)
	if err != nil {
		return fmt.Errorf("cannot resolve config directory: %w", err)
	}
	dataDir = writableDir
	args = rest

	// All remaining commands open the bbolt store exclusively.
	dbPath := filepath.Join(dataDir, "decoyd.db")
	st, err := store.Open(dbPath)
	if err != nil {
		// store.Open returns a friendly 'already running' error on timeout.
		return err
	}
	defer func() { _ = st.Close() }()

	// Dispatch store-dependent subcommands.
	if len(args) > 0 {
		switch args[0] {
		case "list":
			return cmdList(st)
		case "remove":
			// Parse optional --purge flag from remaining args.
			purge := false
			remaining := args[1:]
			for i, a := range remaining {
				if a == "--purge" || a == "-purge" {
					purge = true
					remaining = append(remaining[:i], remaining[i+1:]...)
					break
				}
			}
			if len(remaining) < 1 {
				return fmt.Errorf("usage: decoyd remove [--purge] <id>")
			}
			return cmdRemove(st, dataDir, remaining[0], purge)
		default:
			return fmt.Errorf("unknown command %q — run 'decoyd help' for usage", args[0])
		}
	}

	// No subcommand → launch TUI.
	// Splash shows on every launch (not just first run — the typewriter
	// animation also serves as a natural startup indicator).
	firstRun, err := config.IsFirstRun(dataDir)
	if err != nil {
		return fmt.Errorf("cannot check first-run state: %w", err)
	}
	if firstRun {
		if err := config.MarkInitialized(dataDir); err != nil {
			fmt.Fprintln(os.Stderr, "decoyd: warning: cannot write sentinel file:", err)
		}
	}

	// Start with zero dimensions; bubbletea will send a WindowSizeMsg immediately.
	// Always pass isFirstRun=true so the splash screen plays on every launch.
	model := tui.NewRootModel(true, 0, 0, st, dataDir)
	p := tea.NewProgram(
		model,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	// p.Run returns the final model state. Retrieve any embedded watcher and
	// stop it cleanly so the PID file is removed regardless of how the TUI
	// exited (tea.Quit, no-TTY, SIGTERM, etc.).
	finalModel, runErr := p.Run()
	if finalModel != nil {
		if rm, ok := finalModel.(tui.RootModel); ok {
			if w := rm.Watcher(); w != nil {
				w.Stop()
			}
		}
	}
	if runErr != nil {
		return fmt.Errorf("TUI error: %w", runErr)
	}
	return nil
}

// cmdList prints all tokens in a tab-aligned table, suitable for scripting.
func cmdList(st *store.Store) error {
	tokens, err := st.ListTokens()
	if err != nil {
		return fmt.Errorf("list tokens: %w", err)
	}
	if len(tokens) == 0 {
		fmt.Println("No tokens found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tTYPE\tFILE\tLOCATION\tTRIGGERED\tNOTES")
	fmt.Fprintln(w, "──────────────────\t──────────────────\t──────────\t──────────────────────────\t──────────\t──────────")
	for _, t := range tokens {
		triggered := "no"
		if t.Triggered {
			triggered = "YES"
		}
		loc := t.DeployedPath
		if loc == "" {
			loc = "(not deployed)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			t.ID, t.Type, t.Filename, loc, triggered, t.Notes)
	}
	return w.Flush()
}

// cmdRemove deletes a token record from the store by its ID.
// When purge is true it also deletes the deployed file at tok.DeployedPath.
// A missing file on disk is noted but not treated as an error.
func cmdRemove(st *store.Store, dataDir, id string, purge bool) error {
	// Verify the token exists first so we can give a clear error.
	tok, err := st.GetToken(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("no token with ID %q — run 'decoyd list' to see existing tokens", id)
		}
		return fmt.Errorf("look up token %q: %w", id, err)
	}

	if err := st.DeleteToken(id); err != nil {
		return fmt.Errorf("delete token: %w", err)
	}

	// Update deployed_tokens.json so a running headless watcher stops
	// watching this path immediately without needing a restart.
	_ = watch.ReconcileSnapshot(st, dataDir)

	fmt.Printf("Removed token %s (%s)\n", id, tok.Type)

	if tok.DeployedPath == "" {
		// No deployed file — nothing more to do regardless of --purge.
		if purge {
			fmt.Println("Note: no deployed file recorded for this token.")
		}
		return nil
	}

	if !purge {
		fmt.Printf("Note: deployed file at %s was NOT removed.\n", tok.DeployedPath)
		fmt.Println("      Use --purge to also delete it: decoyd remove --purge " + id)
		return nil
	}

	// --purge: delete the file.
	if err := os.Remove(tok.DeployedPath); err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("Note: deployed file at %s was already gone (not on disk).\n", tok.DeployedPath)
		} else {
			fmt.Printf("Warning: could not delete %s: %v\n", tok.DeployedPath, err)
		}
	} else {
		fmt.Printf("Deleted deployed file: %s\n", tok.DeployedPath)
	}
	return nil
}

// cmdTriggers prints recent trigger events from triggers.jsonl.
// Uses triglog (no bbolt access) so it can run alongside 'decoyd watch'.
func cmdTriggers(dataDir string) error {
	events, err := triglog.Load(dataDir)
	if err != nil {
		return fmt.Errorf("load triggers: %w", err)
	}
	if len(events) == 0 {
		fmt.Println("No trigger events recorded yet.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TIME\tTOKEN\tTYPE\tEVENT\tSTATUS")
	fmt.Fprintln(w, "───────────────────────\t──────────────────\t──────\t──────\t──────────────")
	for _, e := range events {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			e.TriggeredAt.Local().Format("2006-01-02 15:04:05"),
			e.TokenID, e.TokenType, e.EventType, e.Status)
	}
	return w.Flush()
}

func printUsage() {
	fmt.Print(`Usage: decoyd [--data-dir DIR] [command]

Commands:
  (none)             Launch the interactive TUI
  list               Print all token records in a tab-aligned table
  remove <id>        Delete a token record (deployed file is NOT removed)
  remove --purge <id> Delete a token record AND its deployed file from disk
  watch [--tier1]    Run the headless file watcher (alert on token access)
                     --tier1: require fanotify-based Tier 1 detection (Linux only)
  install [--system] Install decoyd watch as a service (user mode or root Tier 1 systemd on Linux)
  uninstall [--system] Remove the installed service (plus /var/lib/decoyd and /etc/decoyd with --system)
  sync               Copy alert config one-way into /etc/decoyd/alert_config.json (Linux root)
  triggers           Print recent trigger events (alias; see events)
  events             Print trigger events with schema v1 fields
                     Flags: --json --since DURATION --token ID --limit N --fail-if-any
                     Exit code 3 when --fail-if-any and events exist
  test               Self-test: deploy temp decoy, start watcher, assert alert arrives
  doctor [--json]    Run health checks (platform, data dir, DB lock, alert config)
  help               Show this help

Data directory:
  Linux:    ~/.decoyd/
  Windows:  %%APPDATA%%\Decoyd\
`)
}
