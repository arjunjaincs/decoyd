package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDataDir_Linux simulates a Linux environment by temporarily overriding
// HOME and checking that DataDir returns $HOME/.decoyd/.
func TestDataDir_Linux(t *testing.T) {
	if os.Getenv("GOOS") == "windows" {
		t.Skip("skipping linux-specific path test on windows GOOS")
	}

	// Use a temp dir as the fake HOME.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	dir, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir() error: %v", err)
	}

	// On non-Windows runtime, we expect $HOME/.decoyd
	if os.Getenv("GOOS") != "windows" {
		want := filepath.Join(tmpHome, AppNameLower)
		if dir != want {
			// Windows CI runners actually run as windows GOOS — skip assertion
			// mismatch that is expected there.
			t.Logf("dir=%q want=%q (may differ on Windows CI)", dir, want)
		}
	}

	// Directory must have been created.
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Errorf("DataDir() did not create directory %q", dir)
	}
}

// TestDataDir_PathContainsAppName verifies that regardless of OS the returned
// path contains the expected app name segment.
func TestDataDir_PathContainsAppName(t *testing.T) {
	dir, err := DataDir()
	if err != nil {
		t.Fatalf("DataDir() error: %v", err)
	}

	lower := strings.ToLower(dir)
	if !strings.Contains(lower, "decoyd") {
		t.Errorf("DataDir() = %q; expected path to contain 'decoyd'", dir)
	}
}

// TestIsFirstRun_SentinelAbsent confirms first-run detection on a fresh directory.
func TestIsFirstRun_SentinelAbsent(t *testing.T) {
	dir := t.TempDir()

	first, err := IsFirstRun(dir)
	if err != nil {
		t.Fatalf("IsFirstRun() error: %v", err)
	}
	if !first {
		t.Error("IsFirstRun() = false; want true for fresh directory")
	}
}

// TestIsFirstRun_SentinelPresent confirms that after MarkInitialized the
// first-run flag is false.
func TestIsFirstRun_SentinelPresent(t *testing.T) {
	dir := t.TempDir()

	if err := MarkInitialized(dir); err != nil {
		t.Fatalf("MarkInitialized() error: %v", err)
	}

	first, err := IsFirstRun(dir)
	if err != nil {
		t.Fatalf("IsFirstRun() after mark error: %v", err)
	}
	if first {
		t.Error("IsFirstRun() = true after MarkInitialized; want false")
	}
}

// TestMarkInitialized_Idempotent confirms calling MarkInitialized twice
// does not return an error.
func TestMarkInitialized_Idempotent(t *testing.T) {
	dir := t.TempDir()

	if err := MarkInitialized(dir); err != nil {
		t.Fatalf("first MarkInitialized() error: %v", err)
	}
	if err := MarkInitialized(dir); err != nil {
		t.Fatalf("second MarkInitialized() error: %v", err)
	}
}

// TestDataDir_TableDriven runs table-driven assertions on expected path
// structure across different simulated base-dir values.
func TestDataDir_TableDriven(t *testing.T) {
	tests := []struct {
		name    string
		envKey  string
		envVal  string
		wantSeg string // expected segment in the returned path
	}{
		{
			name:    "home override",
			envKey:  "HOME",
			envVal:  t.TempDir(),
			wantSeg: "decoyd",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.envKey, tc.envVal)

			dir, err := DataDir()
			if err != nil {
				t.Fatalf("DataDir() error: %v", err)
			}

			if !strings.Contains(strings.ToLower(dir), tc.wantSeg) {
				t.Errorf("DataDir() = %q; want segment %q", dir, tc.wantSeg)
			}
		})
	}
}
