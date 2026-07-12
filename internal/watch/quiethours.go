package watch

import "time"

// InQuietHours reports whether t falls within the configured quiet window.
// Quiet hours suppress alert pushes while still recording the event locally.
// Returns false when quiet hours are disabled (start or end is < 0).
//
// Wraps midnight: e.g. start=22, end=6 means quiet from 22:00 to 06:00.
func InQuietHours(cfg WatchConfig, t time.Time) bool {
	s, e := cfg.QuietHoursStart, cfg.QuietHoursEnd
	if s < 0 || e < 0 {
		return false
	}
	h := t.Hour()
	if s < e {
		// Same-day window: e.g. 08:00-22:00.
		return h >= s && h < e
	}
	if s > e {
		// Wraps midnight: e.g. 22:00-06:00.
		return h >= s || h < e
	}
	// s == e: zero-width window, never quiet.
	return false
}
