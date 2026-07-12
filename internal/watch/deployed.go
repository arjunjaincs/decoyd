// Package watch — deployed token snapshot.
//
// The headless watcher ("decoyd watch") must not open decoyd.db (bbolt holds
// an exclusive file lock per opener; opening it from two processes causes a
// timeout/deadlock).  Instead, the TUI writes a lightweight JSON snapshot of
// currently-deployed tokens whenever any deploy, update, or delete changes a
// DeployedPath.  The headless watcher reads this snapshot to know which paths
// to monitor.
//
// File: <dataDir>/deployed_tokens.json (mode 0600, atomic rename write).

package watch

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const deployedFile = "deployed_tokens.json"

// DeployedToken is a minimal record for the headless watcher.
// It contains only the fields the watcher needs; the full token is in bbolt.
type DeployedToken struct {
	ID             string `json:"id"`
	Type           string `json:"type"`
	DeployedPath   string `json:"deployed_path"`
	AlertChannelID string `json:"alert_channel_id,omitempty"`
}

// WriteDeployedSnapshot writes the current deployed-token list to
// <dataDir>/deployed_tokens.json using an atomic rename so readers never see
// a partial write.  Called by the TUI after every deploy, update, or delete
// that changes a token's DeployedPath.
func WriteDeployedSnapshot(dataDir string, toks []DeployedToken) error {
	data, err := json.MarshalIndent(toks, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dataDir, deployedFile+".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil { // #nosec G304
		return err
	}
	return os.Rename(tmp, filepath.Join(dataDir, deployedFile))
}

// ReadDeployedSnapshot reads <dataDir>/deployed_tokens.json.
// Returns an empty slice (not an error) if the file does not exist.
func ReadDeployedSnapshot(dataDir string) ([]DeployedToken, error) {
	path := filepath.Join(dataDir, deployedFile)
	data, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var toks []DeployedToken
	if err := json.Unmarshal(data, &toks); err != nil {
		return nil, err
	}
	return toks, nil
}
