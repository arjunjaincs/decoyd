package filelock_test

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/arjunjaincs/decoyd/internal/filelock"
)

func TestLock_BasicAcquireRelease(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test")

	lf, err := filelock.Lock(path)
	if err != nil {
		t.Fatalf("Lock() error: %v", err)
	}
	// Lock file must exist.
	if _, err := os.Stat(path + ".lock"); err != nil {
		t.Errorf("lock file does not exist: %v", err)
	}
	lf.Unlock()
	// Double Unlock must not panic.
	lf.Unlock()
}

func TestLock_MutualExclusion(t *testing.T) {
	// Verify that two goroutines holding the lock never overlap.
	dir := t.TempDir()
	path := filepath.Join(dir, "counter")

	const goroutines = 10
	const iters = 50

	var (
		wg      sync.WaitGroup
		counter int64
		maxSeen int64
	)

	worker := func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			lf, err := filelock.Lock(path)
			if err != nil {
				t.Errorf("Lock() error: %v", err)
				return
			}
			// Increment, check, decrement — no other goroutine should be
			// inside this critical section at the same time.
			v := atomic.AddInt64(&counter, 1)
			if v > atomic.LoadInt64(&maxSeen) {
				atomic.StoreInt64(&maxSeen, v)
			}
			// Yield to make races more likely.
			// A race detector will catch data-race; this checks logical exclusion.
			atomic.AddInt64(&counter, -1)
			lf.Unlock()
		}
	}

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go worker()
	}
	wg.Wait()

	// If mutual exclusion worked, counter was never > 1 inside the critical section.
	if maxSeen > 1 {
		t.Errorf("mutual exclusion violated: max concurrent holders = %d", maxSeen)
	}
}
