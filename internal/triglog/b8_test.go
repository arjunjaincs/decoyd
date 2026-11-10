package triglog_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/arjunjaincs/decoyd/internal/triglog"
)

// TestTriglog_ConcurrentAppendDeleteOneNeverLosesEvent proves that concurrent
// Append and DeleteOne calls never lose an appended event that was not explicitly
// deleted.  This is the B8 (A12) requirement.
//
// Strategy:
//   - N goroutines each Append a unique event.
//   - Concurrently, a DeleteOne goroutine deletes only the first event (ID "del-target").
//   - After all goroutines finish, Load must return all N events (IDs "ev-0".."ev-N-1"),
//     and "del-target" must NOT appear.
func TestTriglog_ConcurrentAppendDeleteOneNeverLosesEvent(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC()

	// Pre-insert the deletion target first so DeleteOne has something to work with.
	delTarget := triglog.TriggerEvent{
		ID:          "del-target",
		TokenID:     "tok-del",
		EventType:   triglog.TriggerSent,
		TriggeredAt: now,
	}
	if err := triglog.Append(dir, delTarget); err != nil {
		t.Fatalf("pre-insert del-target: %v", err)
	}

	const n = 30
	var wg sync.WaitGroup

	// Writer goroutines: each appends one unique event.
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			ev := triglog.TriggerEvent{
				ID:          fmt.Sprintf("ev-%d", i),
				TokenID:     "tok1",
				EventType:   triglog.TriggerSent,
				TriggeredAt: now.Add(time.Duration(i) * time.Millisecond),
			}
			if err := triglog.Append(dir, ev); err != nil {
				t.Errorf("Append ev-%d: %v", i, err)
			}
		}()
	}

	// DeleteOne goroutine: races against the writers.
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := triglog.DeleteOne(dir, "del-target"); err != nil {
			t.Errorf("DeleteOne: %v", err)
		}
	}()

	wg.Wait()

	// Verify: all n appended events are present, del-target is gone.
	events, err := triglog.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	idSet := make(map[string]bool, len(events))
	for _, e := range events {
		idSet[e.ID] = true
	}

	// del-target must be gone.
	if idSet["del-target"] {
		t.Error("del-target was not deleted despite DeleteOne call")
	}

	// All n appended events must be present.
	missing := 0
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("ev-%d", i)
		if !idSet[id] {
			t.Errorf("event %q was lost during concurrent Append+DeleteOne", id)
			missing++
		}
	}
	t.Logf("concurrent test: %d events appended, %d present in log, %d missing, del-target present=%v",
		n, len(events), missing, idSet["del-target"])
}
