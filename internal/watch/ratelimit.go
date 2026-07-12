package watch

import (
	"sync"
	"time"
)

// RateLimiter caps alert dispatches per token per hour.
// A limit of 0 means unlimited.
type RateLimiter struct {
	mu     sync.Mutex
	counts map[string]int
	epoch  time.Time
	limit  int
}

// NewRateLimiter returns a RateLimiter that allows at most limit dispatches
// per token per hour. limit=0 means unlimited.
func NewRateLimiter(limit int) *RateLimiter {
	return &RateLimiter{
		counts: make(map[string]int),
		epoch:  time.Now(),
		limit:  limit,
	}
}

// Allow reports whether an alert may be dispatched for tokenID.
// It increments the counter when returning true. The window resets every hour.
func (r *RateLimiter) Allow(tokenID string) bool {
	if r.limit == 0 {
		return true // unlimited
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if time.Since(r.epoch) >= time.Hour {
		r.counts = make(map[string]int)
		r.epoch = time.Now()
	}
	if r.counts[tokenID] >= r.limit {
		return false
	}
	r.counts[tokenID]++
	return true
}

// Reset clears all counters and resets the epoch. Useful for testing.
func (r *RateLimiter) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.counts = make(map[string]int)
	r.epoch = time.Now()
}
