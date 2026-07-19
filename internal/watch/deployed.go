// deployed.go — snapshot of tokens that have been deployed to disk.
//
// deployed_tokens.json is the cross-process coordination file between the TUI
// and the headless watcher.  The TUI writes it on every deploy/delete/startup;
// the headless watcher reads it at start and on SIGHUP to discover which files
// to watch.
//
// Design constraints:
//   - Atomic write (tmp-then-rename) so the watcher never reads a partial file.
//   - No bbolt dependency — the watcher must not open decoyd.db.
//   - 0600 permissions — deployed paths are not sensitive, but consistent with
//     the rest of the data directory.
package watch

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const deployedSnapshotFile = "deployed_tokens.json"

// DeployedToken is the minimal token record stored in deployed_tokens.json.
// Only fields the watcher needs are included; full token data lives in bbolt.
type DeployedToken struct {
	ID             string `json:"id"`
	Type           string `json:"type"`
	DeployedPath   string `json:"deployed_path"`
	AlertChannelID string `json:"alert_channel_id,omitempty"`
}

// WriteDeployedSnapshot atomically writes the list of deployed tokens to
// dataDir/deployed_tokens.json (0600, tmp-then-rename).
// Only tokens with a non-empty DeployedPath are included.
func WriteDeployedSnapshot(dataDir string, tokens []DeployedToken) error {
	// Filter to deployed-only.
	deployed := tokens[:0]
	for _, t := range tokens {
		if t.DeployedPath != "" {
			deployed = append(deployed, t)
		}
	}

	data, err := json.MarshalIndent(deployed, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal deployed snapshot: %w", err)
	}

	path := filepath.Join(dataDir, deployedSnapshotFile)
	tmp := path + ".tmp"

	if err := os.WriteFile(tmp, data, 0o600); err != nil { // #nosec G304 -- tmp is always filepath.Join(dataDir, deployedSnapshotFile)+".tmp"
		return fmt.Errorf("write deployed snapshot: %w", err)
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(tmp, 0o600)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("install deployed snapshot: %w", err)
	}
	return nil
}

// ReadDeployedSnapshot reads dataDir/deployed_tokens.json and returns the list
// of deployed tokens.  Returns an empty slice (not an error) when the file does
// not exist yet.
func ReadDeployedSnapshot(dataDir string) ([]DeployedToken, error) {
	path := filepath.Join(dataDir, deployedSnapshotFile)
	data, err := os.ReadFile(path) // #nosec G304 -- path is always filepath.Join(dataDir, deployedSnapshotFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read deployed snapshot: %w", err)
	}
	var tokens []DeployedToken
	if err := json.Unmarshal(data, &tokens); err != nil {
		return nil, fmt.Errorf("parse deployed snapshot: %w", err)
	}
	return tokens, nil
}
