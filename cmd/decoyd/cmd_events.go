// cmd_events.go — "decoyd events" subcommand.
//
// Reads triggers.jsonl and formats events. Supports filtering by token,
// time range, and exit code 3 when --fail-if-any and at least one event exists.
//
// Schema version 1 adds watcher_tier and attribution fields (T5).
// Old events (without these fields) load correctly due to json:",omitempty".
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/arjunjaincs/decoyd/internal/triglog"
)

// exitCodeEventsFound is the exit code when --fail-if-any is set and events exist.
const exitCodeEventsFound = 3

// cmdEvents implements "decoyd events [--json] [--since DURATION] [--fail-if-any]".
func cmdEvents(dataDir string, args []string) error {
	return cmdEventsWithOptions(dataDir, "", false, args)
}

func cmdEventsWithOptions(dataDir, systemDir string, includeSystem bool, args []string) error {
	var (
		jsonOut   bool
		failIfAny bool
		since     time.Duration
		tokenID   string
		limit     int
	)

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOut = true
		case "--fail-if-any":
			failIfAny = true
		case "--since":
			i++
			if i >= len(args) {
				return fmt.Errorf("--since requires a duration argument (e.g. 1h, 30m, 24h)")
			}
			d, err := time.ParseDuration(args[i])
			if err != nil {
				return fmt.Errorf("--since: invalid duration %q: %w", args[i], err)
			}
			since = d
		case "--token":
			i++
			if i >= len(args) {
				return fmt.Errorf("--token requires a token ID argument")
			}
			tokenID = args[i]
		case "--limit":
			i++
			if i >= len(args) {
				return fmt.Errorf("--limit requires a number argument")
			}
			n := 0
			if _, err := fmt.Sscanf(args[i], "%d", &n); err != nil || n < 0 {
				return fmt.Errorf("--limit: invalid value %q (must be a non-negative integer)", args[i])
			}
			limit = n
		}
	}

	events, err := loadMergedEvents(dataDir, systemDir, includeSystem)
	if err != nil {
		return fmt.Errorf("load events: %w", err)
	}

	// Filter by token ID.
	if tokenID != "" {
		filtered := events[:0]
		for _, e := range events {
			if e.TokenID == tokenID {
				filtered = append(filtered, e)
			}
		}
		events = filtered
	}

	// Filter by --since.
	if since > 0 {
		cutoff := time.Now().Add(-since)
		filtered := events[:0]
		for _, e := range events {
			if e.TriggeredAt.After(cutoff) {
				filtered = append(filtered, e)
			}
		}
		events = filtered
	}

	// Apply --limit (take last N).
	if limit > 0 && len(events) > limit {
		events = events[len(events)-limit:]
	}

	if len(events) == 0 {
		if jsonOut {
			fmt.Println("[]")
		} else {
			fmt.Println("No trigger events recorded yet.")
		}
		return nil
	}

	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(events); err != nil {
			return fmt.Errorf("json encode: %w", err)
		}
	} else {
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "TIME\tTOKEN\tTYPE\tEVENT\tSTATUS\tTIER\tPID\tEXE")
		fmt.Fprintln(w, "───────────────────────\t──────────────────\t──────\t──────\t──────────────\t──────\t────\t────────────")
		for _, e := range events {
			tier := e.WatcherTier
			if tier == "" {
				tier = "tier2"
			}
			pid := ""
			exe := ""
			if e.Attribution != nil {
				pid = fmt.Sprintf("%d", e.Attribution.Accessor.PID)
				exe = e.Attribution.Accessor.Exe
				if exe == "" {
					exe = e.Attribution.Accessor.Comm
				}
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				e.TriggeredAt.Local().Format("2006-01-02 15:04:05"),
				e.TokenID, e.TokenType, e.EventType, e.Status,
				tier, pid, exe)
		}
		if err := w.Flush(); err != nil {
			return err
		}
	}

	if failIfAny && len(events) > 0 {
		os.Exit(exitCodeEventsFound)
	}
	return nil
}

func loadMergedEvents(dataDir, systemDir string, includeSystem bool) ([]triglog.TriggerEvent, error) {
	var all []triglog.TriggerEvent
	if dataDir != "" {
		evs, lErr := triglog.Load(dataDir)
		if lErr != nil {
			return nil, lErr
		}
		all = append(all, evs...)
	}
	if includeSystem && systemDir != "" && filepath.Clean(systemDir) != filepath.Clean(dataDir) {
		evs, lErr := triglog.Load(systemDir)
		if lErr == nil {
			all = append(all, evs...)
		}
	}
	if len(all) <= 1 {
		return all, nil
	}
	seen := make(map[string]bool, len(all))
	dedup := make([]triglog.TriggerEvent, 0, len(all))
	for _, e := range all {
		if e.ID != "" {
			if seen[e.ID] {
				continue
			}
			seen[e.ID] = true
		}
		dedup = append(dedup, e)
	}
	sort.SliceStable(dedup, func(i, j int) bool {
		return dedup[i].TriggeredAt.Before(dedup[j].TriggeredAt)
	})
	return dedup, nil
}
