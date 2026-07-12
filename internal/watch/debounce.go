package watch

import (
	"sync"
	"time"
)

// Debouncer collapses rapid repeated events on the same key into a single
// callback that fires after the configured window of silence.
//
// Design: one time.Timer per key, reset on each Trigger call. The callback
// fires only when no new Trigger arrives within the window.
type Debouncer struct {
	mu     sync.Mutex
	timers map[string]*time.Timer
	window time.Duration
}

// NewDebouncer returns a Debouncer with the given silence window.
func NewDebouncer(window time.Duration) *Debouncer {
	return &Debouncer{
		timers: make(map[string]*time.Timer),
		window: window,
	}
}

// Trigger resets (or creates) the debounce timer for key. fn is called once
// after the debounce window elapses without another Trigger for the same key.
// fn is called in its own goroutine.
func (d *Debouncer) Trigger(key string, fn func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if t, ok := d.timers[key]; ok {
		t.Stop()
	}
	d.timers[key] = time.AfterFunc(d.window, func() {
		d.mu.Lock()
		delete(d.timers, key)
		d.mu.Unlock()
		fn()
	})
}

// Stop cancels all pending timers without firing their callbacks.
func (d *Debouncer) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, t := range d.timers {
		t.Stop()
	}
	d.timers = make(map[string]*time.Timer)
}
