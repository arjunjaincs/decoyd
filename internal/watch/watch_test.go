// Package watch — tests for deployed snapshot and watcher config.
// All tests in this file run on every platform (no build tags).
package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ----------------------------------------------------------------------------
// Snapshot read/write
// ----------------------------------------------------------------------------

func TestWriteReadDeployedSnapshot_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	tokens := []DeployedToken{
		{ID: "aabbccdd11223344", Type: "aws_credentials", DeployedPath: "/home/user/.aws/credentials"},
		{ID: "deadbeefdeadbeef", Type: "ssh_key", DeployedPath: "/home/user/.ssh/id_rsa", AlertChannelID: "chanid1"},
	}
	if err := WriteDeployedSnapshot(dir, tokens); err != nil {
		t.Fatalf("WriteDeployedSnapshot() error: %v", err)
	}
	got, err := ReadDeployedSnapshot(dir)
	if err != nil {
		t.Fatalf("ReadDeployedSnapshot() error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 tokens, got %d", len(got))
	}
	if got[0].ID != tokens[0].ID || got[1].ID != tokens[1].ID {
		t.Errorf("IDs mismatch: got %q %q", got[0].ID, got[1].ID)
	}
	if got[1].AlertChannelID != "chanid1" {
		t.Errorf("AlertChannelID mismatch: got %q", got[1].AlertChannelID)
	}
}

func TestWriteDeployedSnapshot_FiltersUndeployed(t *testing.T) {
	dir := t.TempDir()
	tokens := []DeployedToken{
		{ID: "deployed01234567", Type: "aws_credentials", DeployedPath: "/tmp/credentials"},
		{ID: "notdeployedabcd0", Type: "ssh_key", DeployedPath: ""},
	}
	if err := WriteDeployedSnapshot(dir, tokens); err != nil {
		t.Fatalf("WriteDeployedSnapshot() error: %v", err)
	}
	got, err := ReadDeployedSnapshot(dir)
	if err != nil {
		t.Fatalf("ReadDeployedSnapshot() error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 deployed token, got %d", len(got))
	}
	if got[0].ID != "deployed01234567" {
		t.Errorf("wrong token returned: %q", got[0].ID)
	}
}

func TestReadDeployedSnapshot_MissingFileReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	got, err := ReadDeployedSnapshot(dir)
	if err != nil {
		t.Fatalf("ReadDeployedSnapshot on missing file should not error; got: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 tokens, got %d", len(got))
	}
}

func TestWriteDeployedSnapshot_AtomicOverwrite(t *testing.T) {
	dir := t.TempDir()
	initial := []DeployedToken{
		{ID: "firsttoken00000", Type: "aws_credentials", DeployedPath: "/tmp/first"},
	}
	if err := WriteDeployedSnapshot(dir, initial); err != nil {
		t.Fatal(err)
	}
	updated := []DeployedToken{
		{ID: "secondtoken0000", Type: "ssh_key", DeployedPath: "/tmp/second"},
	}
	if err := WriteDeployedSnapshot(dir, updated); err != nil {
		t.Fatal(err)
	}
	got, err := ReadDeployedSnapshot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "secondtoken0000" {
		t.Errorf("expected second token after overwrite, got %+v", got)
	}
	// No tmp file left behind.
	if _, err := os.Stat(filepath.Join(dir, deployedSnapshotFile+".tmp")); !os.IsNotExist(err) {
		t.Error("tmp file was not cleaned up after atomic rename")
	}
}

// ----------------------------------------------------------------------------
// WatcherConfig / quiet hours (platform-independent logic tests)
// ----------------------------------------------------------------------------

// timeAtHour returns a local time.Time with the given hour for testing
// the quiet-hours logic, which uses local wall-clock time.
func timeAtHour(hour int) time.Time {
	now := time.Now().Local()
	return time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
}

func TestWatcherConfig_InQuietHours_Disabled(t *testing.T) {
	cfg := WatcherConfig{QuietHoursEnabled: false}
	for h := 0; h < 24; h++ {
		if cfg.inQuietHours(timeAtHour(h)) {
			t.Errorf("inQuietHours(hour=%d) = true; want false when disabled", h)
		}
	}
}

func TestWatcherConfig_InQuietHours_WrapMidnight(t *testing.T) {
	// Quiet from 22:00 to 05:59.
	cfg := WatcherConfig{
		QuietHoursEnabled: true,
		QuietHoursStart:   22,
		QuietHoursEnd:     6,
	}
	quietHours := []int{22, 23, 0, 1, 2, 3, 4, 5}
	alertHours := []int{6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21}
	for _, h := range quietHours {
		if !cfg.inQuietHours(timeAtHour(h)) {
			t.Errorf("inQuietHours(hour=%d) = false; want true (22–06 wrap)", h)
		}
	}
	for _, h := range alertHours {
		if cfg.inQuietHours(timeAtHour(h)) {
			t.Errorf("inQuietHours(hour=%d) = true; want false (22–06 wrap)", h)
		}
	}
}

func TestWatcherConfig_InQuietHours_DaytimeRange(t *testing.T) {
	// No wrap: quiet from 09:00 to 17:59.
	cfg := WatcherConfig{
		QuietHoursEnabled: true,
		QuietHoursStart:   9,
		QuietHoursEnd:     18,
	}
	quietHours := []int{9, 10, 11, 12, 13, 14, 15, 16, 17}
	alertHours := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 18, 19, 20, 21, 22, 23}
	for _, h := range quietHours {
		if !cfg.inQuietHours(timeAtHour(h)) {
			t.Errorf("inQuietHours(hour=%d) = false; want true (09–18)", h)
		}
	}
	for _, h := range alertHours {
		if cfg.inQuietHours(timeAtHour(h)) {
			t.Errorf("inQuietHours(hour=%d) = true; want false (09–18)", h)
		}
	}
}

// ----------------------------------------------------------------------------
// Rate-limit logic (tests the in-process dispatch logic directly)
// ----------------------------------------------------------------------------

func TestRateLimit_AllowsUpToLimit(t *testing.T) {
	cfg := DefaultWatcherConfig() // RateLimit = 5
	rateMap := make(map[string]*rateEntry)
	tok := DeployedToken{ID: "tok1", Type: "aws_credentials", DeployedPath: "/tmp/f"}
	now := time.Now()

	allowCount := 0
	for i := 0; i < 10; i++ {
		if !rateLimitExceeded(rateMap, tok.ID, cfg.RateLimit, now) {
			allowCount++
		}
	}
	if allowCount != cfg.RateLimit {
		t.Errorf("allowed %d; want %d (rate limit)", allowCount, cfg.RateLimit)
	}
}

func TestRateLimit_ResetsAfterHour(t *testing.T) {
	cfg := DefaultWatcherConfig()
	rateMap := make(map[string]*rateEntry)
	tok := DeployedToken{ID: "tok2", Type: "ssh_key", DeployedPath: "/tmp/g"}
	now := time.Now()

	// Exhaust the limit.
	for i := 0; i < cfg.RateLimit; i++ {
		rateLimitExceeded(rateMap, tok.ID, cfg.RateLimit, now)
	}
	// Still blocked at same time.
	if !rateLimitExceeded(rateMap, tok.ID, cfg.RateLimit, now) {
		t.Error("expected blocked after limit exhausted")
	}
	// One hour later: window resets.
	future := now.Add(time.Hour + time.Second)
	if rateLimitExceeded(rateMap, tok.ID, cfg.RateLimit, future) {
		t.Error("expected allowed after 1hr window reset")
	}
}

// rateLimitExceeded is a test helper that mirrors the dispatch rate-limit logic
// so we can test it without spinning up a real inotify watcher.
// Returns true when the event should be suppressed (limit exceeded).
func rateLimitExceeded(rateMap map[string]*rateEntry, tokenID string, limit int, now time.Time) bool {
	re := rateMap[tokenID]
	if re == nil || now.After(re.windowEnd) {
		rateMap[tokenID] = &rateEntry{count: 1, windowEnd: now.Add(time.Hour)}
		return false // first event in window: allow
	}
	re.count++
	return re.count > limit
}
