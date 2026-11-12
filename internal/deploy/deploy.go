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
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// dirPermForType returns the directory permission for a token type.
// Secret token types (keys, credentials) get 0o700 to restrict directory
// listing to the owner. All other types get 0o750.
func dirPermForType(tokenType string) os.FileMode {
	switch tokenType {
	case tokens.TypeAWSCredentials,
		tokens.TypeSSHKey,
		tokens.TypeGitHubPAT,
		tokens.TypeSlackToken,
		tokens.TypeEnvSecrets,
		tokens.TypeDBDump,
		tokens.TypeKubeconfig:
		return 0o700
	default:
		return 0o750
	}
}

// DeployResult carries the outcome of a single deploy operation.
type DeployResult struct {
	Token       tokens.Token
	DeployedTo  string   // absolute path of the primary written file
	ExtraFiles  []string // additional files written (e.g. id_ed25519.pub)
	DryRun      bool     // true ⇒ nothing was written, result is a preview
	WouldCreate bool     // only meaningful when DryRun == true
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

	// Warn when running as root and deploying outside system-managed directories.
	// Deploying outside /var/lib/decoyd or /etc/decoyd as root risks clobbering
	// system files if the caller passes a user-writable path. This is a warning,
	// not an error — the caller may be intentionally writing to a custom path
	// (e.g., /opt, CI runner home).
	warnIfRootOutsideSystemDirs(targetDir)

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
	if err := os.MkdirAll(absDir, dirPermForType(t.Type)); err != nil {
		return DeployResult{}, fmt.Errorf("deploy: create directory: %w", err)
	}

	perm := PermForType(t.Type)
	if err := writeFile(dest, t.Value, perm); err != nil {
		return DeployResult{}, err
	}
	backdateFiles(dest)

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
		Token:      t,
		DeployedTo: privDest,
		ExtraFiles: []string{pubDest},
	}

	if opts.DryRun {
		res.DryRun = true
		res.WouldCreate = true
		return res, nil
	}

	if err := os.MkdirAll(absDir, dirPermForType(t.Type)); err != nil {
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
	backdateFiles(privDest, pubDest)

	return res, nil
}

// writeFile writes content to dest with the given permissions.
// Uses openExclNoFollow (O_CREAT|O_EXCL|O_NOFOLLOW on Linux) to prevent
// symlink attacks and TOCTOU races. Applies permissions via file descriptor
// (Chmod on the fd) rather than a separate path-based chmod call.
func writeFile(dest, content string, perm os.FileMode) error {
	f, err := openExclNoFollow(dest, perm)
	if err != nil {
		return fmt.Errorf("deploy: open %s: %w", dest, err)
	}
	defer f.Close()

	if _, err := io.WriteString(f, content); err != nil {
		// Best-effort: remove the partially-written file to avoid a corrupt decoy.
		_ = os.Remove(dest)
		return fmt.Errorf("deploy: write %s: %w", dest, err)
	}

	// Apply permissions via fd (avoids a path-based chmod that could race).
	if err := f.Chmod(perm); err != nil {
		// Non-fatal on platforms where Chmod via fd is unsupported;
		// the perm passed to openExclNoFollow already set the umask-adjusted mode.
		_ = err
	}
	return nil
}

// backdateFiles sets the atime and mtime of each path to a random time
// 30-400 days before now.  This ensures deployed decoy files do not have a
// suspicious "just-created" timestamp that might alert a careful attacker.
// Errors are silently ignored; backdating is best-effort.
func backdateFiles(paths ...string) {
	daysBack := 30 + rand.Intn(371) // #nosec G404 -- mtime jitter is not security-sensitive; math/rand is sufficient
	t := time.Now().AddDate(0, 0, -daysBack)
	for _, p := range paths {
		_ = os.Chtimes(p, t, t)
	}
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

var (
	deployGetUID            = os.Getuid
	deployWarnOut io.Writer = os.Stderr
)

// warnIfRootOutsideSystemDirs writes a warning to deployWarnOut when the process
// runs as UID 0 and targetDir is not under a system-managed decoyd path.
// System paths: /var/lib/decoyd, /etc/decoyd (Linux convention).
// This is a warning, not an error. AGENTS.md Rule 12/14.
func warnIfRootOutsideSystemDirs(targetDir string) {
	if deployGetUID() != 0 {
		return
	}
	abs, err := filepath.Abs(targetDir)
	if err != nil {
		return
	}
	systemPaths := []string{"/var/lib/decoyd", "/etc/decoyd"}
	for _, sp := range systemPaths {
		spAbs, serr := filepath.Abs(sp)
		if serr != nil {
			spAbs = sp
		}
		if abs == spAbs || strings.HasPrefix(abs, spAbs+string(filepath.Separator)) {
			return // within a system path — OK
		}
	}
	fmt.Fprintf(deployWarnOut,
		"decoyd deploy: warning: running as root; deploying to %q which is outside system-managed directories"+
			" (/var/lib/decoyd, /etc/decoyd)."+
			" This risks deploying to a user-writable path.\n", abs)
}
