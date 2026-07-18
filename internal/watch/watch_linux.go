//go:build linux

package watch

import (
	"github.com/arjunjaincs/decoyd/internal/store"
)

// linuxWatcher is the Linux stub. Full inotify implementation added in Phase 4.
type linuxWatcher struct {
	st      *store.Store
	dataDir string
}

func newPlatformImpl(st *store.Store, dataDir string) (watcherImpl, error) {
	return &linuxWatcher{st: st, dataDir: dataDir}, nil
}

func (w *linuxWatcher) start() error         { return nil }
func (w *linuxWatcher) stop()                {}
func (w *linuxWatcher) status() WatcherStatus { return WatcherStatus{} }
