// watcherstatus.go — exported helper for determining headless watcher state.
// Used by the dashboard (internal/tui/statusscreen.go) to distinguish the
// three states without importing platform-specific liveness code directly.
package watch

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// HeadlessState describes the state of a headless watcher process.
type HeadlessState int

const (
	// HeadlessNotRunning means no watcher.pid file exists.
	HeadlessNotRunning HeadlessState = iota
	// HeadlessRunning means watcher.pid exists and the holder is alive.
	HeadlessRunning
	// HeadlessStale means watcher.pid exists but the holder is dead.
	HeadlessStale
)

// HeadlessWatcherState reads <dataDir>/watcher.pid and returns the current
// state of any headless watcher process. It does NOT acquire the lock.
//
// This is intended for read-only status display (e.g. the TUI dashboard)
// and must not be used as a synchronisation mechanism.
func HeadlessWatcherState(dataDir string) (state HeadlessState, pid int) {
	path := filepath.Join(dataDir, pidFileName)
	data, err := os.ReadFile(path) // #nosec G304 -- path is always filepath.Join(dataDir, pidFileName)
	if err != nil {
		return HeadlessNotRunning, 0
	}
	s := strings.TrimSpace(string(data))
	p, err := strconv.Atoi(s)
	if err != nil || p <= 0 {
		return HeadlessStale, 0
	}
	if isProcessAlive(p) {
		return HeadlessRunning, p
	}
	return HeadlessStale, p
}
