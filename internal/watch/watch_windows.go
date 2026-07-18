//go:build windows

package watch

import (
	"github.com/arjunjaincs/decoyd/internal/store"
)

// windowsWatcher is the Windows stub. Full fsnotify implementation added in Phase 4.
type windowsWatcher struct {
	st      *store.Store
	dataDir string
}

func newPlatformImpl(st *store.Store, dataDir string) (watcherImpl, error) {
	return &windowsWatcher{st: st, dataDir: dataDir}, nil
}

func (w *windowsWatcher) start() error         { return nil }
func (w *windowsWatcher) stop()                {}
func (w *windowsWatcher) status() WatcherStatus { return WatcherStatus{} }
