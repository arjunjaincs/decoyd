package watch_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/arjunjaincs/decoyd/internal/store"
	"github.com/arjunjaincs/decoyd/internal/triglog"
	"github.com/arjunjaincs/decoyd/internal/watch"
)

// ----------------------------------------------------------------------------
// Debouncer
// ----------------------------------------------------------------------------

func TestDebouncer_FiresOnceAfterSilence(t *testing.T) {
	d := watch.NewDebouncer(50 * time.Millisecond)
	defer d.Stop()

	count := 0
	inc := func() { count++ }
	for i := 0; i < 5; i++ {
		d.Trigger("key", inc)
	}
	time.Sleep(150 * time.Millisecond)
	if count != 1 {
		t.Errorf("expected 1 fire, got %d", count)
	}
}

func TestDebouncer_SeparateKeysAreIndependent(t *testing.T) {
	d := watch.NewDebouncer(30 * time.Millisecond)
	defer d.Stop()
	var aCount, bCount int
	d.Trigger("a", func() { aCount++ })
	d.Trigger("b", func() { bCount++ })
	time.Sleep(120 * time.Millisecond)
	if aCount != 1 || bCount != 1 {
		t.Errorf("expected 1 each, got a=%d b=%d", aCount, bCount)
	}
}

func TestDebouncer_StopCancelsTimers(t *testing.T) {
	d := watch.NewDebouncer(200 * time.Millisecond)
	called := false
	d.Trigger("key", func() { called = true })
	d.Stop()
	time.Sleep(300 * time.Millisecond)
	if called {
		t.Error("Stop should have prevented the timer from firing")
	}
}

// ----------------------------------------------------------------------------
// RateLimiter
// ----------------------------------------------------------------------------

func TestRateLimiter_AllowsUpToLimit(t *testing.T) {
	rl := watch.NewRateLimiter(3)
	for i := 0; i < 3; i++ {
		if !rl.Allow("tok") {
			t.Fatalf("expected Allow=true on call %d", i+1)
		}
	}
	if rl.Allow("tok") {
		t.Error("expected Allow=false after limit exceeded")
	}
}

func TestRateLimiter_ZeroMeansUnlimited(t *testing.T) {
	rl := watch.NewRateLimiter(0)
	for i := 0; i < 100; i++ {
		if !rl.Allow("tok") {
			t.Fatalf("unlimited limiter denied on call %d", i+1)
		}
	}
}

func TestRateLimiter_SeparateTokensAreIndependent(t *testing.T) {
	rl := watch.NewRateLimiter(1)
	if !rl.Allow("a") {
		t.Error("tok-a first call should be allowed")
	}
	if rl.Allow("a") {
		t.Error("tok-a second call should be denied")
	}
	if !rl.Allow("b") {
		t.Error("tok-b first call should be allowed (independent counter)")
	}
}

func TestRateLimiter_ResetRestoresQuota(t *testing.T) {
	rl := watch.NewRateLimiter(1)
	rl.Allow("tok")
	rl.Reset()
	if !rl.Allow("tok") {
		t.Error("expected Allow=true after Reset")
	}
}

// ----------------------------------------------------------------------------
// Quiet hours
// ----------------------------------------------------------------------------

func TestInQuietHours_Disabled(t *testing.T) {
	cfg := watch.WatchConfig{QuietHoursStart: -1, QuietHoursEnd: -1}
	if watch.InQuietHours(cfg, time.Date(2024, 1, 1, 23, 0, 0, 0, time.UTC)) {
		t.Error("expected false when disabled")
	}
}

func TestInQuietHours_WrapMidnightInside(t *testing.T) {
	cfg := watch.WatchConfig{QuietHoursStart: 22, QuietHoursEnd: 6}
	for _, h := range []int{22, 23, 0, 1, 5} {
		if !watch.InQuietHours(cfg, time.Date(2024, 1, 1, h, 0, 0, 0, time.UTC)) {
			t.Errorf("hour %d should be quiet", h)
		}
	}
}

func TestInQuietHours_WrapMidnightOutside(t *testing.T) {
	cfg := watch.WatchConfig{QuietHoursStart: 22, QuietHoursEnd: 6}
	for _, h := range []int{6, 12, 18, 21} {
		if watch.InQuietHours(cfg, time.Date(2024, 1, 1, h, 0, 0, 0, time.UTC)) {
			t.Errorf("hour %d should NOT be quiet", h)
		}
	}
}

func TestInQuietHours_ZeroWidthWindow(t *testing.T) {
	cfg := watch.WatchConfig{QuietHoursStart: 10, QuietHoursEnd: 10}
	if watch.InQuietHours(cfg, time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)) {
		t.Error("zero-width window should never be quiet")
	}
}

// ----------------------------------------------------------------------------
// Deployed token snapshot
// ----------------------------------------------------------------------------

func TestDeployedSnapshot_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := []watch.DeployedToken{
		{ID: "tok-1", Type: "aws", DeployedPath: "/tmp/creds.csv"},
		{ID: "tok-2", Type: "azure", DeployedPath: "/tmp/azure.env", AlertChannelID: "aabb1122"},
	}
	if err := watch.WriteDeployedSnapshot(dir, in); err != nil {
		t.Fatalf("WriteDeployedSnapshot: %v", err)
	}
	out, err := watch.ReadDeployedSnapshot(dir)
	if err != nil {
		t.Fatalf("ReadDeployedSnapshot: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 tokens, got %d", len(out))
	}
	if out[0].ID != "tok-1" || out[1].AlertChannelID != "aabb1122" {
		t.Errorf("round-trip mismatch: %+v", out)
	}
}

func TestDeployedSnapshot_MissingFileReturnsEmpty(t *testing.T) {
	toks, err := watch.ReadDeployedSnapshot(t.TempDir())
	if err != nil {
		t.Errorf("expected nil error for missing file, got %v", err)
	}
	if len(toks) != 0 {
		t.Errorf("expected empty slice, got %d", len(toks))
	}
}

func TestDeployedSnapshot_EmptySlice(t *testing.T) {
	dir := t.TempDir()
	if err := watch.WriteDeployedSnapshot(dir, nil); err != nil {
		t.Fatalf("WriteDeployedSnapshot(nil): %v", err)
	}
	out, err := watch.ReadDeployedSnapshot(dir)
	if err != nil {
		t.Fatalf("ReadDeployedSnapshot: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("expected empty slice, got %d", len(out))
	}
}

// ----------------------------------------------------------------------------
// triglog: Append / Load / deduplication / LoadByToken
// ----------------------------------------------------------------------------

func TestTriglog_AppendAndLoad(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Millisecond)
	te := triglog.TriggerEvent{
		ID:          "aabbccdd00112233",
		TokenID:     "tok-abc",
		TokenType:   "aws",
		Path:        "/etc/decoy.csv",
		TriggeredAt: now,
		EventType:   "access",
		Status:      triglog.TriggerPending,
	}
	if err := triglog.Append(dir, te); err != nil {
		t.Fatalf("Append: %v", err)
	}
	list, err := triglog.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 event, got %d", len(list))
	}
	got := list[0]
	if got.ID != te.ID || got.Status != triglog.TriggerPending {
		t.Errorf("mismatch: %+v", got)
	}
}

func TestTriglog_DeduplicatesByIDLatestWins(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()

	pending := triglog.TriggerEvent{
		ID: "dedup0001", TokenID: "tok-x", TriggeredAt: now,
		EventType: "write", Status: triglog.TriggerPending,
	}
	final := triglog.TriggerEvent{
		ID: "dedup0001", TokenID: "tok-x", TriggeredAt: now,
		EventType: "write", Status: triglog.TriggerSent, AlertError: "",
	}

	_ = triglog.Append(dir, pending)
	_ = triglog.Append(dir, final) // supersedes pending

	list, err := triglog.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 deduplicated event, got %d", len(list))
	}
	if list[0].Status != triglog.TriggerSent {
		t.Errorf("expected TriggerSent (latest wins), got %q", list[0].Status)
	}
}

func TestTriglog_LoadMissingFileReturnsEmpty(t *testing.T) {
	list, err := triglog.Load(t.TempDir())
	if err != nil {
		t.Errorf("expected nil error for missing file, got %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected empty, got %d", len(list))
	}
}

func TestTriglog_LoadByToken(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().UTC()
	events := []triglog.TriggerEvent{
		{ID: "ev001", TokenID: "tok-A", TriggeredAt: base, Status: triglog.TriggerSent},
		{ID: "ev002", TokenID: "tok-B", TriggeredAt: base.Add(time.Second), Status: triglog.TriggerSent},
		{ID: "ev003", TokenID: "tok-A", TriggeredAt: base.Add(2 * time.Second), Status: triglog.TriggerFailed},
	}
	for _, e := range events {
		_ = triglog.Append(dir, e)
	}
	listA, err := triglog.LoadByToken(dir, "tok-A")
	if err != nil {
		t.Fatalf("LoadByToken: %v", err)
	}
	if len(listA) != 2 {
		t.Fatalf("expected 2 for tok-A, got %d", len(listA))
	}
	// newest-first: ev003 (TriggerFailed, +2s) should be first
	if listA[0].Status != triglog.TriggerFailed {
		t.Error("expected newest (TriggerFailed) first")
	}
}

func TestTriglog_NewestFirst(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		te := triglog.TriggerEvent{
			ID:          "ev00" + string(rune('a'+i)),
			TokenID:     "tok",
			TriggeredAt: base.Add(time.Duration(i) * time.Minute),
			Status:      triglog.TriggerSent,
		}
		_ = triglog.Append(dir, te)
	}
	list, err := triglog.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for i := 1; i < len(list); i++ {
		if !list[i-1].TriggeredAt.After(list[i].TriggeredAt) {
			t.Errorf("not newest-first at index %d/%d", i-1, i)
		}
	}
}

func TestTriglog_AppendEmptyIDReturnsError(t *testing.T) {
	dir := t.TempDir()
	if err := triglog.Append(dir, triglog.TriggerEvent{ID: ""}); err == nil {
		t.Error("expected error for empty ID")
	}
}

// ----------------------------------------------------------------------------
// Store timeout: second opener should fail fast, not hang
// ----------------------------------------------------------------------------

func TestStore_SecondOpenerFailsFast(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "decoyd.db")

	st1, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	defer st1.Close()

	start := time.Now()
	_, err = store.Open(dbPath)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected second Open to fail (bbolt exclusive lock)")
	}
	// Should fail within ~700ms (500ms timeout + OS overhead).
	if elapsed > 700*time.Millisecond {
		t.Errorf("second Open took %v, expected ≤700ms fail-fast", elapsed)
	}
	t.Logf("second Open failed in %v with: %v", elapsed, err)
}

// compile-time checks that exported types are present
var _ = watch.InQuietHours
var _ watch.WatchConfig
var _ triglog.TriggerStatus
var _ = watch.ErrWatcherRunning

// ----------------------------------------------------------------------------
// Snapshot integration — deploy and delete flows
// ----------------------------------------------------------------------------

// TestSnapshot_DeployAddsToken checks that WriteDeployedSnapshot followed by
// ReadDeployedSnapshot round-trips a newly deployed token.
// This mirrors what deployscreen.go does after a successful deploy.
func TestSnapshot_DeployAddsToken(t *testing.T) {
	dir := t.TempDir()

	tok := watch.DeployedToken{
		ID:             "aabb1122ccdd3344",
		Type:           "aws",
		DeployedPath:   "/home/user/.aws/credentials",
		AlertChannelID: "ch-001",
	}

	// Simulate a deploy: write the snapshot with one token.
	if err := watch.WriteDeployedSnapshot(dir, []watch.DeployedToken{tok}); err != nil {
		t.Fatalf("WriteDeployedSnapshot: %v", err)
	}

	// Read it back.
	snaps, err := watch.ReadDeployedSnapshot(dir)
	if err != nil {
		t.Fatalf("ReadDeployedSnapshot: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snaps))
	}
	got := snaps[0]
	if got.ID != tok.ID || got.DeployedPath != tok.DeployedPath || got.AlertChannelID != tok.AlertChannelID {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, tok)
	}
}

// TestSnapshot_DeleteRemovesToken checks that after a delete the snapshot no
// longer contains the deleted token.
// This mirrors what tokenlist.go does in updateConfirmDelete.
func TestSnapshot_DeleteRemovesToken(t *testing.T) {
	dir := t.TempDir()

	toks := []watch.DeployedToken{
		{ID: "tok-A", Type: "aws", DeployedPath: "/tmp/a"},
		{ID: "tok-B", Type: "github", DeployedPath: "/tmp/b"},
	}
	if err := watch.WriteDeployedSnapshot(dir, toks); err != nil {
		t.Fatalf("initial WriteDeployedSnapshot: %v", err)
	}

	// Simulate delete of tok-A: rebuild list without it.
	var after []watch.DeployedToken
	for _, tok := range toks {
		if tok.ID != "tok-A" {
			after = append(after, tok)
		}
	}
	if err := watch.WriteDeployedSnapshot(dir, after); err != nil {
		t.Fatalf("post-delete WriteDeployedSnapshot: %v", err)
	}

	snaps, err := watch.ReadDeployedSnapshot(dir)
	if err != nil {
		t.Fatalf("ReadDeployedSnapshot: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("expected 1 remaining token, got %d", len(snaps))
	}
	if snaps[0].ID != "tok-B" {
		t.Errorf("expected tok-B to remain, got %q", snaps[0].ID)
	}
}
