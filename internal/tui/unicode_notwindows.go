//go:build !windows

package tui

// hasVTSupport returns true on non-Windows platforms.
// Linux/macOS terminals universally support Unicode and VT sequences.
func hasVTSupport() bool {
	return true
}
