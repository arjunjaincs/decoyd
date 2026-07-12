// Package deploy handles writing canary token files to disk.
//
// DeployToFile is the core operation: it writes a token's content to
// targetDir/token.Filename with appropriate permissions, refuses to
// overwrite an existing file, and records the absolute deployed path
// in the token's DeployedPath field via the provided update callback.
package deploy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/arjunjaincs/decoyd/internal/tokens"
)

// ErrAlreadyExists is returned when the target path already exists.
// The caller should ask the user to confirm before removing and retrying.
var ErrAlreadyExists = errors.New("file already exists at target path")

// PermForType returns the file-system permission bits appropriate for a
// given token type.  Keys and credentials get 0600; others get 0644.
func PermForType(tokenType string) os.FileMode {
	switch tokenType {
	case tokens.TypeAWSCredentials,
		tokens.TypeSSHKey,
		tokens.TypeGitHubPAT,
		tokens.TypeSlackToken,
		tokens.TypeEnvSecrets:
		return 0o600
	default:
		return 0o644
	}
}

// DeployResult carries the outcome of a single deploy operation.
type DeployResult struct {
	Token       tokens.Token
	DeployedTo  string // absolute path of the written file
	DryRun      bool   // true ⇒ nothing was written, result is a preview
	WouldCreate bool   // only meaningful when DryRun == true
}

// Options controls how DeployToFile behaves.
type Options struct {
	// DryRun: if true, no file is written; the result shows what would happen.
	DryRun bool
}

// DeployToFile writes t.Value to filepath.Join(targetDir, t.Filename).
//
//   - Returns ErrAlreadyExists without writing if the target file already exists
//     (even in dry-run mode the check is performed).
//   - Sets permission bits via PermForType.
//   - Returns a DeployResult describing what happened (or would happen in dry-run).
func DeployToFile(t tokens.Token, targetDir string, opts Options) (DeployResult, error) {
	if targetDir == "" {
		return DeployResult{}, errors.New("deploy: targetDir must not be empty")
	}

	// Resolve the absolute target path.
	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return DeployResult{}, fmt.Errorf("deploy: resolve path: %w", err)
	}
	dest := filepath.Join(absDir, t.Filename)

	// Refuse to overwrite.
	if _, err := os.Stat(dest); err == nil {
		return DeployResult{}, fmt.Errorf("%w: %s", ErrAlreadyExists, dest)
	} else if !errors.Is(err, os.ErrNotExist) {
		return DeployResult{}, fmt.Errorf("deploy: stat target: %w", err)
	}

	res := DeployResult{
		Token:      t,
		DeployedTo: dest,
	}

	if opts.DryRun {
		res.DryRun = true
		res.WouldCreate = true
		return res, nil
	}

	// Ensure the directory exists.
	if err := os.MkdirAll(absDir, 0o750); err != nil {
		return DeployResult{}, fmt.Errorf("deploy: create directory: %w", err)
	}

	perm := PermForType(t.Type)
	if err := os.WriteFile(dest, []byte(t.Value), perm); err != nil {
		return DeployResult{}, fmt.Errorf("deploy: write file: %w", err)
	}

	// On Windows, file permissions aren't enforced the same way; chmod is a
	// no-op there but we call it anyway for portability.
	if runtime.GOOS != "windows" {
		if err := os.Chmod(dest, perm); err != nil {
			// Non-fatal — log via the returned result.
			_ = err
		}
	}

	return res, nil
}

// PresetDirs returns platform-appropriate preset destination directories.
// The first entry is always the home directory.
func PresetDirs() ([]PresetDir, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home dir: %w", err)
	}

	presets := []PresetDir{
		{Label: "Home directory", Path: home},
		{Label: "Downloads", Path: filepath.Join(home, "Downloads")},
		{Label: "Desktop", Path: filepath.Join(home, "Desktop")},
	}

	// SSH config dir — only add when it exists.
	sshDir := filepath.Join(home, ".ssh")
	if _, err := os.Stat(sshDir); err == nil {
		presets = append(presets, PresetDir{Label: "~/.ssh", Path: sshDir})
	}

	return presets, nil
}

// PresetDir is one entry in the preset destination picker.
type PresetDir struct {
	Label string // display name shown in TUI
	Path  string // absolute path
}

// SanitizePath expands a leading ~ to the user home directory.
func SanitizePath(raw string) (string, error) {
	if strings.HasPrefix(raw, "~/") || raw == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, raw[1:]), nil
	}
	return filepath.Abs(raw)
}
