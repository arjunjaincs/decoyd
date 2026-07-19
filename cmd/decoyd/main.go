// Decoyd — self-hosted canary token generator and monitor.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/arjunjaincs/decoyd/internal/config"
	"github.com/arjunjaincs/decoyd/internal/store"
	"github.com/arjunjaincs/decoyd/internal/triglog"
	"github.com/arjunjaincs/decoyd/internal/tui"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "decoyd: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	// Resolve data directory first — needed by all subcommands.
	dataDir, err := config.DataDir()
	if err != nil {
		return fmt.Errorf("cannot resolve config directory: %w", err)
	}

	// Commands that MUST NOT open the bbolt store (no exclusive lock).
	// 'decoyd watch' runs as a persistent background service alongside the
	// TUI; if it opened decoyd.db it would deadlock the next TUI launch.
	if len(args) > 0 {
		switch args[0] {
		case "watch":
			return cmdWatch(dataDir)
		case "install":
			return cmdInstall(dataDir)
		case "triggers":
			return cmdTriggers(dataDir)
		case "help", "--help", "-h":
			printUsage()
			return nil
		}
	}

	// All remaining commands open the bbolt store exclusively.
	dbPath := filepath.Join(dataDir, "decoyd.db")
	st, err := store.Open(dbPath)
	if err != nil {
		// store.Open returns a friendly 'already running' error on timeout.
		return err
	}
	defer st.Close()

	// Dispatch store-dependent subcommands.
	if len(args) > 0 {
		switch args[0] {
		case "list":
			return cmdList(st)
		case "remove":
			if len(args) < 2 {
				return fmt.Errorf("usage: decoyd remove <id>")
			}
			return cmdRemove(st, args[1])
		default:
			return fmt.Errorf("unknown command %q — run 'decoyd help' for usage", args[0])
		}
	}

	// No subcommand → launch TUI.
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
	model := tui.NewRootModel(firstRun, 0, 0, st, dataDir)
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
// It does NOT remove the deployed file from disk.
func cmdRemove(st *store.Store, id string) error {
	// Verify the token exists first so we can give a clear error.
	tok, err := st.GetToken(id)
	if err != nil {
		return fmt.Errorf("token %q not found in store", id)
	}

	if err := st.DeleteToken(id); err != nil {
		return fmt.Errorf("delete token: %w", err)
	}

	fmt.Printf("Removed token %s (%s)\n", id, tok.Type)
	if tok.DeployedPath != "" {
		fmt.Printf("Note: deployed file at %s was NOT removed from disk.\n", tok.DeployedPath)
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
	fmt.Print(`Usage: decoyd [command]

Commands:
  (none)          Launch the interactive TUI
  list            Print all token records in a tab-aligned table
  remove <id>     Delete a token record from the store (deployed file is NOT removed)
  watch           Run the headless file watcher (alert on token access)
  install         Install decoyd watch as a system service (systemd on Linux,
                  Task Scheduler on Windows)
  triggers        Print recent trigger events
  help            Show this help

Data directory:
  Linux:    ~/.decoyd/
  Windows:  %%APPDATA%%\Decoyd\
`)
}
