//go:build linux

// deploy_linux.go — Linux-specific deploy helpers.
// Uses O_CREAT|O_EXCL|O_NOFOLLOW to prevent symlink attacks:
//   - O_EXCL: fail if the file already exists (atomic; avoids TOCTOU).
//   - O_NOFOLLOW: fail if the final path component is a symlink.
//     This prevents an attacker who pre-places a symlink from redirecting
//     the write to an arbitrary target.
package deploy

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// openExclNoFollow opens path for exclusive creation with no-follow semantics.
// Returns an *os.File ready for writing. The caller must close it.
//
// Flags: O_WRONLY|O_CREAT|O_EXCL|O_NOFOLLOW|O_CLOEXEC
func openExclNoFollow(path string, perm os.FileMode) (*os.File, error) {
	fd, err := unix.Open(
		path,
		unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		uint32(perm), // #nosec G115 -- perm is always a valid mode (0600 or 0644)
	)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return os.NewFile(uintptr(fd), path), nil // #nosec G115 -- fd is non-negative
}
