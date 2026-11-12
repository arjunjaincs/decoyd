//go:build windows

// deploy_windows.go — Windows-specific deploy helpers.
//
// Reparse-point (symlink/junction) refusal:
// On Windows, O_NOFOLLOW is not available through the standard library.
// Instead we query Win32 file attributes via GetFileAttributes (checking
// FILE_ATTRIBUTE_REPARSE_POINT) as well as os.Lstat ModeSymlink before calling
// os.OpenFile(O_CREATE|O_EXCL|O_WRONLY). This detects file symlinks, directory
// symlinks, and NTFS junctions (IO_REPARSE_TAG_MOUNT_POINT / mklink /J).
// Ref: https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-getfileattributesw
package deploy

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// ErrReparsePoint is returned when the target path is a symlink or reparse point.
var ErrReparsePoint = errors.New("deploy: target is a symlink or reparse point")

// openExclNoFollow opens path for exclusive creation on Windows.
// If path is an existing symlink or NTFS reparse point (including junctions),
// returns ErrReparsePoint without opening or traversing it.
func openExclNoFollow(path string, perm os.FileMode) (*os.File, error) {
	if p16, uerr := windows.UTF16PtrFromString(path); uerr == nil {
		attrs, gerr := windows.GetFileAttributes(p16)
		if gerr == nil && attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return nil, fmt.Errorf("%w: %s", ErrReparsePoint, path)
		}
	}
	if fi, err := os.Lstat(path); err == nil {
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
