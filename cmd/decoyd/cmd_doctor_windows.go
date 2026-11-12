//go:build windows

package main

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// doctorPlatformChecks adds Windows-specific checks.
func doctorPlatformChecks(add func(doctorResult)) {
	maj, min, build := windows.RtlGetNtVersionNumbers()
	add(doctorResult{
		Check:   "kernel",
		Status:  "ok",
		Message: fmt.Sprintf("Windows NT %d.%d (build %d)", maj, min, build),
	})
	add(doctorResult{
		Check:   "fsnotify",
		Status:  "ok",
		Message: "ReadDirectoryChangesW available (tier 1)",
	})
}

// doctorFSType returns the filesystem type for the volume containing path.
func doctorFSType(path string) (string, error) {
	root, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "unknown", err
	}
	var fsNameBuf [256]uint16
	err = windows.GetVolumeInformation(root, nil, 0, nil, nil, nil,
		&fsNameBuf[0], uint32(len(fsNameBuf)))
	if err != nil {
		return "unknown", err
	}
	return windows.UTF16ToString(fsNameBuf[:]), nil
}
