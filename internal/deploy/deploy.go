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

// sshKeySentinel separates the private-key PEM from the public-key line in
// an SSH token's Value field — matches what GenerateSSHKey() produces.
const sshKeySentinel = "---PUBLIC KEY---\n"

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
	Token        tokens.Token
	DeployedTo   string // absolute path of the primary written file
	ExtraFiles   []string // additional files written (e.g. id_ed25519.pub)
	DryRun       bool   // true ⇒ nothing was written, result is a preview
	WouldCreate  bool   // only meaningful when DryRun == true
}

// Options controls how DeployToFile behaves.
type Options struct {
	// DryRun: if true, no file is written; the result shows what would happen.
	DryRun bool
}

// DeployToFile writes t.Value to filepath.Join(targetDir, t.Filename).
//
// Special case: TypeSSHKey tokens contain both a private key and a public key
// separated by sshKeySentinel. DeployToFile automatically writes:
//   - id_ed25519      (private key, 0600)
//   - id_ed25519.pub  (public key,  0644)
//
// Returns ErrAlreadyExists without writing if either target file already exists
// (even in dry-run mode the check is performed).
func DeployToFile(t tokens.Token, targetDir string, opts Options) (DeployResult, error) {
	if targetDir == "" {
		return DeployResult{}, errors.New("deploy: targetDir must not be empty")
	}

	// SSH keys get special two-file handling.
	if t.Type == tokens.TypeSSHKey {
		return deploySSHKeyPair(t, targetDir, opts)
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
	if err := writeFile(dest, t.Value, perm); err != nil {
		return DeployResult{}, err
	}

	return res, nil
}

// deploySSHKeyPair handles the special case of an SSH token: it splits the
// Value on sshKeySentinel and writes id_ed25519 (private, 0600) and
// id_ed25519.pub (public, 0644) as a pair.
func deploySSHKeyPair(t tokens.Token, targetDir string, opts Options) (DeployResult, error) {
	parts := strings.SplitN(t.Value, sshKeySentinel, 2)
	if len(parts) != 2 {
		return DeployResult{}, errors.New("deploy: malformed SSH token value (missing sentinel)")
	}
	privPEM := parts[0]
	pubLine := parts[1]

	absDir, err := filepath.Abs(targetDir)
	if err != nil {
		return DeployResult{}, fmt.Errorf("deploy: resolve path: %w", err)
	}
	privDest := filepath.Join(absDir, "id_ed25519")
	pubDest := filepath.Join(absDir, "id_ed25519.pub")

	// Check both targets before writing anything.
	for _, dest := range []string{privDest, pubDest} {
		if _, err := os.Stat(dest); err == nil {
			return DeployResult{}, fmt.Errorf("%w: %s", ErrAlreadyExists, dest)
		} else if !errors.Is(err, os.ErrNotExist) {
			return DeployResult{}, fmt.Errorf("deploy: stat target: %w", err)
		}
	}

	res := DeployResult{
		Token:       t,
		DeployedTo:  privDest,
		ExtraFiles:  []string{pubDest},
	}

	if opts.DryRun {
		res.DryRun = true
		res.WouldCreate = true
		return res, nil
	}

	if err := os.MkdirAll(absDir, 0o750); err != nil {
		return DeployResult{}, fmt.Errorf("deploy: create directory: %w", err)
	}

	// Write private key (0600).
	if err := writeFile(privDest, privPEM, 0o600); err != nil {
		return DeployResult{}, err
	}
	// Write public key (0644).
	if err := writeFile(pubDest, pubLine, 0o644); err != nil {
		// Roll back the private key so we don't leave a partial deploy.
		_ = os.Remove(privDest)
		return DeployResult{}, err
	}

	return res, nil
}

// writeFile writes content to dest with the given permissions.
func writeFile(dest, content string, perm os.FileMode) error {
	if err := os.WriteFile(dest, []byte(content), perm); err != nil {
		return fmt.Errorf("deploy: write %s: %w", dest, err)
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(dest, perm)
	}
	return nil
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
