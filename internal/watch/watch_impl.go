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
	// reload re-reads the token list and synchronises the set of watched files,
	// adding new watches and removing stale ones. No-op if not running. Thread-safe.
	reload() error
}

// newImpl constructs the platform-specific implementation.
// Implemented in watch_linux.go and watch_windows.go.
func newImpl(st *store.Store, dataDir string) (watcherImpl, error) {
	return newPlatformImpl(st, dataDir)
}

// newImplWithOptions constructs the platform-specific implementation with options.
// Implemented in hybrid_linux.go (Linux) and watch_windows.go (Windows, options ignored).
func newImplWithOptions(st *store.Store, dataDir string, opts WatchOptions) (watcherImpl, error) {
	return newPlatformImplWithOptions(st, dataDir, opts)
}
