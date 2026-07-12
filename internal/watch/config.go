package watch

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const watchConfigFile = "watch_config.json"

// WatchConfig controls alert quality features. It is persisted as
// watch_config.json in the Decoyd data directory.
type WatchConfig struct {
	// DebounceSeconds collapses rapid repeated events on the same file into
	// one trigger. Default: 2.
	DebounceSeconds int `json:"debounce_seconds"`
	// RateLimitPerHour caps the number of alerts dispatched per token per
	// hour. 0 means unlimited. Default: 5.
	RateLimitPerHour int `json:"rate_limit_per_hour"`
	// QuietHoursStart and QuietHoursEnd define a window (0-23) during which
	// alerts are logged locally but not pushed. -1 disables quiet hours.
	QuietHoursStart int `json:"quiet_hours_start"`
	QuietHoursEnd   int `json:"quiet_hours_end"`
	// IgnoreProcesses is a list of process names whose file accesses are
	// silently discarded (Linux only; requires process attribution support).
	IgnoreProcesses []string `json:"ignore_processes"`
}

// DefaultWatchConfig returns sensible defaults.
func DefaultWatchConfig() WatchConfig {
	return WatchConfig{
		DebounceSeconds:  2,
		RateLimitPerHour: 5,
		QuietHoursStart:  -1,
		QuietHoursEnd:    -1,
	}
}

// LoadWatchConfig reads watch_config.json from dataDir. Returns defaults if
// the file does not exist or cannot be decoded.
func LoadWatchConfig(dataDir string) (WatchConfig, error) {
	path := filepath.Join(dataDir, watchConfigFile)
	data, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultWatchConfig(), nil
		}
		return DefaultWatchConfig(), err
	}
	cfg := DefaultWatchConfig()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return DefaultWatchConfig(), err
	}
	if cfg.DebounceSeconds <= 0 {
		cfg.DebounceSeconds = 2
	}
	return cfg, nil
}

// SaveWatchConfig writes cfg to watch_config.json in dataDir (mode 0600).
func SaveWatchConfig(dataDir string, cfg WatchConfig) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dataDir, watchConfigFile)
	return os.WriteFile(path, data, 0o600) // #nosec G304
}
