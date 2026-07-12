// export_test.go exposes internal helpers for use in external _test packages.
// This file is compiled ONLY when running tests (build tag: test).

package watch

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// WriteTestPIDFile writes a synthetic PID to watcher.pid under dir.
// Used by watchlock_test.go to simulate a stale lock.
func WriteTestPIDFile(t *testing.T, dir string, pid int) {
	t.Helper()
	p := filepath.Join(dir, watchLockFile)
	if err := os.WriteFile(p, []byte(fmt.Sprintf("%d\n", pid)), 0o600); err != nil {
		t.Fatalf("WriteTestPIDFile: %v", err)
	}
}
