// watcher_config.go — shared watcher configuration types used by both
// the Linux (inotify) and Windows (fsnotify) implementations.
package watch

import "time"

// WatcherConfig controls debounce, rate limiting, and quiet hours.
// Zero values give sensible defaults (see DefaultWatcherConfig).
type WatcherConfig struct {
	// DebounceDuration is the minimum time that must elapse after the last
	// event on a given path before the event is forwarded to alert dispatch.
	// Default: 2 seconds.
	DebounceDuration time.Duration

	// RateLimit is the maximum number of alerts per token per hour.
	// Default: 5.
	RateLimit int

	// QuietHoursStart is the hour (0–23, local time) at which quiet hours begin.
	// Events during quiet hours are recorded in triglog as TriggerQuietHours but
	// no alert is sent.  Zero value disables quiet hours.
	QuietHoursStart int

	// QuietHoursEnd is the hour (0–23, local time) at which quiet hours end.
	// If QuietHoursEnd < QuietHoursStart the range wraps midnight.
	// QuietHoursEnd == QuietHoursStart (and QuietHoursStart != 0) means the
	// whole day is quiet — not a useful config, but handled safely.
	QuietHoursEnd int

	// QuietHoursEnabled enables quiet hours suppression.
	QuietHoursEnabled bool
}

// DefaultWatcherConfig returns the recommended production defaults.
func DefaultWatcherConfig() WatcherConfig {
	return WatcherConfig{
		DebounceDuration: 2 * time.Second,
		RateLimit:        5,
	}
}

// rateEntry tracks per-token rate-limit state within a single watcher session.
// Stored in the event loop's rateMap; not persisted to disk.
type rateEntry struct {
	count     int
	windowEnd time.Time
}

// debounceEntry tracks per-path debounce state within a single watcher session.
// Stored in the event loop's debounce map; not persisted to disk.
type debounceEntry struct {
	token  DeployedToken
	event  string    // "access", "write", "rename", "delete"
	expiry time.Time // dispatch alert at or after this time
}



// inQuietHours reports whether the watcher should suppress alerts at now.
func (c WatcherConfig) inQuietHours(now time.Time) bool {
	if !c.QuietHoursEnabled {
		return false
	}
	h := now.Local().Hour()
	s, e := c.QuietHoursStart, c.QuietHoursEnd
	if s == e {
		// Same start and end with QuietHoursEnabled: treat as all-day suppression.
		return true
	}
	if s < e {
		// Normal range, e.g. 22–06 without wrap: actually wait — s < e means no wrap.
		// e.g. s=09, e=17: quiet from 09:00 to 16:59.
		return h >= s && h < e
	}
	// Wrap-midnight: e.g. s=22, e=06: quiet from 22:00 to 05:59.
	return h >= s || h < e
}
