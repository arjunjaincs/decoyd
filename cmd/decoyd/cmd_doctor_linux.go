//go:build linux

package main

import (
	"fmt"
	"syscall"

	"golang.org/x/sys/unix"
)

// releaseFromUtsname converts [65]int8Release to string with explicit byte bounds checks.
func releaseFromUtsname(rel [65]int8) string {
	b := make([]byte, 0, len(rel))
	for _, c := range rel {
		if c == 0 {
			break
		}
		if c < 0 {
			// Non-ASCII byte; ignore to avoid signed-to-unsigned truncation issues.
			continue
		}
		b = append(b, byte(c))
	}
	return string(b)
}

// fsTypeFromMagic maps Linux statfs magic numbers without integer narrowing.
func fsTypeFromMagic(magic int64) string {
	switch magic {
	case 0xEF53:
		return "ext4"
	case 0x9123683E:
		return "btrfs"
	case 0x01021994:
		return "tmpfs"
	case 0x6969:
		return "nfs"
	case 0x65735546:
		return "fuse"
	case 0x01021997:
		return "v9fs" // 9p
	case 0x794C7630:
		return "overlayfs"
	case 0x58465342:
		return "xfs"
	case 0x4244:
		return "hfs"
	case 0x2FC12FC1:
		return "zfs"
	default:
		return fmt.Sprintf("0x%x", magic)
	}
}

// doctorPlatformChecks adds Linux-specific checks: kernel version, inotify
// availability, and detection tier.
func doctorPlatformChecks(add func(doctorResult)) {
	var uname syscall.Utsname
	if err := syscall.Uname(&uname); err == nil {
		add(doctorResult{Check: "kernel", Status: "ok",
			Message: "Linux " + releaseFromUtsname(uname.Release)})
	} else {
		add(doctorResult{Check: "kernel", Status: "warn",
			Message: "uname failed: " + err.Error()})
	}

	// Inotify probe (Tier 2 fallback).
	if fd, err := unix.InotifyInit1(unix.IN_CLOEXEC); err == nil {
		_ = unix.Close(fd)
		add(doctorResult{
			Check:   "inotify",
			Status:  "ok",
			Message: "available (Tier 2: file read/write events, no privilege required)",
		})
	} else {
		add(doctorResult{
			Check:   "inotify",
			Status:  "fail",
			Message: "unavailable: " + err.Error(),
		})
	}

	// Fanotify probe (Tier 1 primary).
	if fd, err := unix.FanotifyInit(
		unix.FAN_CLASS_NOTIF|unix.FAN_CLOEXEC|unix.FAN_NONBLOCK,
		uint(unix.O_RDONLY|unix.O_LARGEFILE|unix.O_CLOEXEC),
	); err == nil {
		_ = unix.Close(fd)
		add(doctorResult{
			Check:   "fanotify",
			Status:  "ok",
			Message: "available (Tier 1: fanotify FAN_CLASS_NOTIF with PID attribution)",
		})
	} else {
		add(doctorResult{
			Check:   "fanotify",
			Status:  "warn",
			Message: fmt.Sprintf("unavailable (%v) — needs root or CAP_SYS_ADMIN for Tier 1; Tier 2 inotify active", err),
		})
	}
}

// doctorFSType returns the filesystem type name for the path's mount.
// Uses statfs(2). Returns "unknown" on error.
func doctorFSType(path string) (string, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return "unknown", err
	}
	return fsTypeFromMagic(int64(st.Type)), nil
}
