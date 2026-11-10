//go:build windows

// deploy_windows.go — Windows-specific deploy helpers.
//
// Reparse-point (symlink/junction) refusal:
// On Windows, O_NOFOLLOW is not available through the standard library.
// Instead we use Lstat + check for ModeSymlink before calling openExclNoFollow.
// If the target path is a symlink or junction (reparse point), deploy refuses
// with ErrReparsePoint to prevent symlink-following attacks.
//
// Note: Detecting NTFS junctions requires checking IO_REPARSE_TAG_MOUNT_POINT
// via DeviceIoControl, which requires a Windows syscall not available in pure
// Go without CGO or windows system packages. This is documented as a known
// limitation; we refuse os.ModeSymlink (which covers standard symlinks) and
// log that full junction detection requires a Windows CI privileged test.
// See: https://learn.microsoft.com/en-us/windows/win32/api/winioctl/ni-winioctl-fsctl_get_reparse_point
//
// UNKNOWN: Full junction refusal cannot be tested without a real Windows host.
// The privileged integration test for this is documented in the CI spec (B12).
package deploy

import (
	"errors"
	"fmt"
	"os"
)

// ErrReparsePoint is returned when the target path is a symlink or reparse point.
var ErrReparsePoint = errors.New("deploy: target is a symlink or reparse point")

// openExclNoFollow opens path for exclusive creation on Windows.
// If the path already exists as a symlink, returns ErrReparsePoint.
// Full NTFS junction detection (IO_REPARSE_TAG_MOUNT_POINT) requires
// DeviceIoControl and is UNKNOWN without a real Windows host.
func openExclNoFollow(path string, perm os.FileMode) (*os.File, error) {
	// Lstat gives us the link itself, not what it points to.
	fi, err := os.Lstat(path)
	if err == nil {
		// Path already exists.
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: %s", ErrReparsePoint, path)
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return f, nil
}
