// Package limit provides in-memory fixed-window counters for abuse limits.
package limit

import (
	"sync"
	"time"
)

// Limiter counts events per key in fixed windows. State is per process.
type Limiter struct {
	mu        sync.Mutex
	windows   map[string]*window
	lastSweep time.Time
}

type window struct {
	start  time.Time
	count  int
	period time.Duration
}

// New returns an empty limiter.
func New() *Limiter {
	return &Limiter{windows: map[string]*window{}}
}

// Allow counts one event for key. When the window already holds limit events
// it reports false and how long until the window resets.
func (l *Limiter) Allow(key string, limit int, period time.Duration, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(now)
	w := l.windows[key]
	if w == nil || !now.Before(w.start.Add(w.period)) {
		w = &window{start: now, period: period}
		l.windows[key] = w
	}
	if w.count >= limit {
		return false, w.start.Add(w.period).Sub(now)
	}
	w.count++
	return true, 0
}

// Exceeded reports whether key's current window already holds limit events,
// without counting one. It lets a check refuse before doing the work that
// Allow would count afterwards.
func (l *Limiter) Exceeded(key string, limit int, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.windows[key]
	if w == nil || !now.Before(w.start.Add(w.period)) || w.count < limit {
		return false, 0
	}
	return true, w.start.Add(w.period).Sub(now)
}

// sweep drops expired windows at most once a minute to bound memory.
func (l *Limiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < time.Minute {
		return
	}
	l.lastSweep = now
	for key, w := range l.windows {
		if !now.Before(w.start.Add(w.period)) {
			delete(l.windows, key)
		}
	}
}
