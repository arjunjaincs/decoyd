// watch_impl.go — internal interface satisfied by each platform implementation.
package watch

import (
	"github.com/arjunjaincs/decoyd/internal/store"
)

// watcherImpl is the internal interface every platform-specific watcher must satisfy.
// It is not exported; callers use the *Watcher wrapper.
type watcherImpl interface {
	start() error
	stop()
	status() WatcherStatus
}

// newImpl constructs the platform-specific implementation.
// Implemented in watch_linux.go and watch_windows.go.
func newImpl(st *store.Store, dataDir string) (watcherImpl, error) {
	return newPlatformImpl(st, dataDir)
}
