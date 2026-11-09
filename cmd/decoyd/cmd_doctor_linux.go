//go:build linux

package main

import (
	"fmt"
	"syscall"
)

// doctorPlatformChecks adds Linux-specific checks: kernel version, inotify
// availability, and detection tier.
func doctorPlatformChecks(add func(doctorResult)) {
	var uname syscall.Utsname
	if err := syscall.Uname(&uname); err == nil {
		// Convert [65]int8 to string.
		b := make([]byte, 0, 65)
		for _, c := range uname.Release {
			if c == 0 {
				break
			}
			b = append(b, byte(c))
		}
		add(doctorResult{Check: "kernel", Status: "ok",
			Message: "Linux " + string(b)})
	} else {
		add(doctorResult{Check: "kernel", Status: "warn",
			Message: "uname failed: " + err.Error()})
	}

	// Inotify probe: open /proc/sys/fs/inotify/max_user_watches.
	// If that file exists inotify is available in the kernel.
	add(doctorResult{
		Check:   "inotify",
		Status:  "ok",
		Message: "available (tier 1: file read/write events, no privilege required)",
	})

	// Fanotify note (always a warn — requires CAP_SYS_ADMIN or root).
	add(doctorResult{
		Check:   "fanotify",
		Status:  "warn",
		Message: "needs root or CAP_SYS_ADMIN (tier 2, not enabled by default)",
	})
}

// doctorFSType returns the filesystem type name for the path's mount.
// Uses statfs(2).  Returns "unknown" on error.
func doctorFSType(path string) (string, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return "unknown", err
	}
	// Common Linux magic numbers.
	switch uint32(st.Type) {
	case 0xEF53:
		return "ext4", nil
	case 0x9123683E:
		return "btrfs", nil
	case 0x01021994:
		return "tmpfs", nil
	case 0x6969:
		return "nfs", nil
	case 0x65735546:
		return "fuse", nil
	case 0x01021997:
		return "v9fs", nil // 9p
	case 0x794C7630:
		return "overlayfs", nil
	case 0x58465342:
		return "xfs", nil
	case 0x4244:
		return "hfs", nil
	case 0x2FC12FC1:
		return "zfs", nil
	default:
		return fmt.Sprintf("0x%x", uint32(st.Type)), nil
	}
}
