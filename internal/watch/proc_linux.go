//go:build linux

package watch

import (
	"fmt"
	"os"
	"strings"
)

// processName attempts to resolve the process name from /proc/<pid>/comm.
// Returns an empty string if the lookup fails for any reason.
// This is best-effort: the process may have exited before we read the file.
func processName(pid int) string {
	if pid <= 0 {
		return ""
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid)) // #nosec G304 -- path is /proc/<pid>/comm, not user input
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
