//go:build !linux

// deploy_notlinux.go — non-Linux deploy helpers.
// On non-Linux platforms, O_NOFOLLOW is not available via unix.Open,
// so we use os.OpenFile with O_CREATE|O_EXCL which at least prevents
// overwriting an existing file.
package deploy

import (
	"fmt"
	"os"
)

// openExclNoFollow opens path for exclusive creation.
// On non-Linux platforms, symlink-following is not prevented at the OS level;
// rely on the target directory permissions to limit attack surface.
func openExclNoFollow(path string, perm os.FileMode) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return f, nil
}
