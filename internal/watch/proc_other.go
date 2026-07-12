//go:build !linux

package watch

// processName returns an empty string on non-Linux platforms.
// True process attribution requires /proc or platform-specific APIs;
// fsnotify events do not carry a PID, so this remains a v1.1 item.
func processName(_ int) string { return "" }
