// Package config resolves the per-OS data directory for Decoyd and provides
// helpers for first-run detection.
package config

import (
	"os"
	"path/filepath"
	"runtime"
)

const (
	// sentinelFile is written on first run to mark the config directory as initialized.
	sentinelFile = ".initialized"
	// AppName is used as the directory name inside the OS config root.
	AppName = "Decoyd"
	// AppNameLower is used on Linux (hidden dot-dir in home).
	AppNameLower = ".decoyd"
)

// DataDir returns the platform-appropriate writable data directory for Decoyd.
//
//   - Linux / other Unix:  $HOME/.decoyd/
//   - Windows:             %APPDATA%\Decoyd\   (os.UserConfigDir)
//
// The directory is created if it does not already exist.
func DataDir() (string, error) {
	var dir string

	if runtime.GOOS == "windows" {
		base, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(base, AppName)
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, AppNameLower)
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}

	return dir, nil
}

// IsFirstRun reports whether the sentinel file is absent in dir, meaning this
// is the first time Decoyd has run with this config directory.
func IsFirstRun(dir string) (bool, error) {
	sentinel := filepath.Join(dir, sentinelFile)
	_, err := os.Stat(sentinel)
	if os.IsNotExist(err) {
		return true, nil
	}
	return false, err
}

// MarkInitialized writes the sentinel file to dir, recording that Decoyd has
// run at least once. Subsequent calls to IsFirstRun will return false.
func MarkInitialized(dir string) error {
	sentinel := filepath.Join(dir, sentinelFile)
	f, err := os.OpenFile(sentinel, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) // #nosec G304 -- path is always filepath.Join(dataDir, sentinelFile), never user input
	if err != nil {
		return err
	}
	return f.Close()
}
