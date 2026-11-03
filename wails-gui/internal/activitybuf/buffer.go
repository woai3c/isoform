package activitybuf

import (
	"sync"
	"time"

	"wails-gui/internal/platform/activitymon"
)

const (
	defaultCapacity = 1000
	defaultWindow   = 30 * time.Second
)

// RingBuffer is a thread-safe circular buffer for ActivityEvents.
// It maintains a rolling window of events by both capacity and time.
type RingBuffer struct {
	mu       sync.Mutex
	events   []activitymon.ActivityEvent
	capacity int
	window   time.Duration
	head     int
	count    int
}

// NewRingBuffer creates a new RingBuffer with the given capacity and time window.
func NewRingBuffer(capacity int, window time.Duration) *RingBuffer {
	if capacity <= 0 {
		capacity = defaultCapacity
	}
	if window <= 0 {
		window = defaultWindow
	}
	return &RingBuffer{
		events:   make([]activitymon.ActivityEvent, capacity),
		capacity: capacity,
		window:   window,
	}
}

const (
	// mergeKeyboardThreshold is the maximum gap between consecutive keyboard
	// events (from the same app) before they are considered separate bursts.
	mergeKeyboardThreshold = 2000 // 2 seconds in ms

	// maxKeyCharsPerEvent caps the merged KeyChars slice to prevent
	// unbounded growth during long continuous typing sessions.
	maxKeyCharsPerEvent = 5000
)

// Push adds an event to the buffer. Consecutive keyboard events from the
// same application are merged into a single event by appending characters.
// Older events beyond the capacity or time window are automatically evicted.
func (b *RingBuffer) Push(evt activitymon.ActivityEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Try to merge consecutive keyboard events from the same app
	if evt.Kind == activitymon.EventKeyboard && len(evt.GetKeyChars()) > 0 && b.count > 0 {
		lastIdx := (b.head + b.count - 1) % b.capacity
		last := &b.events[lastIdx]
		if last.Kind == activitymon.EventKeyboard &&
			last.AppBundleID == evt.AppBundleID &&
			evt.Timestamp-last.Timestamp < mergeKeyboardThreshold &&
			len(last.KeyChars) < maxKeyCharsPerEvent {
			// Merge: append the new key tokens and update timestamp
			last.KeyChars = append(last.KeyChars, evt.KeyChars...)
			last.Timestamp = evt.Timestamp
			// Update window title in case the user switched fields
			last.WindowTitle = evt.WindowTitle
			// Any IME-composed keystroke marks the whole burst: its raw
			// KeyChars are then pre-composition noise, not literal text.
			last.IMEComposing = last.IMEComposing || evt.IMEComposing
			b.evictExpired(evt.Timestamp)
			return
		}
	}

	// Write at the next position
	idx := (b.head + b.count) % b.capacity
	b.events[idx] = evt

	if b.count < b.capacity {
		b.count++
	} else {
		// Buffer is full — advance head
		b.head = (b.head + 1) % b.capacity
	}

	// Evict events outside the time window
	b.evictExpired(evt.Timestamp)
}

// AmendLastKeyboard locates the most recent keyboard event from bundleID and
// applies fn to it in place, returning whether a target was found. The
// typed-text tracker uses this to attach diffed TypedText to a burst's
// merged keyboard event after the burst closes (commit key, typing pause,
// click). Interleaved non-keyboard events (speech, clicks) are skipped.
func (b *RingBuffer) AmendLastKeyboard(bundleID string, fn func(*activitymon.ActivityEvent)) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := b.count - 1; i >= 0; i-- {
		evt := &b.events[(b.head+i)%b.capacity]
		if evt.Kind != activitymon.EventKeyboard || evt.AppBundleID != bundleID {
			continue
		}
		fn(evt)
		return true
	}
	return false
}

// Snapshot returns a copy of all events currently in the buffer,
// ordered oldest to newest.
func (b *RingBuffer) Snapshot() []activitymon.ActivityEvent {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.count == 0 {
		return nil
	}

	// Evict expired before snapshot
	now := time.Now().UnixMilli()
	b.evictExpired(now)

	result := make([]activitymon.ActivityEvent, b.count)
	for i := 0; i < b.count; i++ {
		idx := (b.head + i) % b.capacity
		result[i] = b.events[idx]
	}
	return result
}

// Len returns the number of events currently in the buffer.
func (b *RingBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.count
}

// Clear removes all events from the buffer.
func (b *RingBuffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.head = 0
	b.count = 0
}

// Drain atomically snapshots and clears the buffer in a single critical
// section, returning the events in oldest-to-newest order. Use this from
// extractSession-style callers so two concurrent invocations can't both
// observe the same events (which would otherwise be persisted under two
// distinct session IDs).
func (b *RingBuffer) Drain() []activitymon.ActivityEvent {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.count == 0 {
		return nil
	}

	now := time.Now().UnixMilli()
	b.evictExpired(now)

	result := make([]activitymon.ActivityEvent, b.count)
	for i := 0; i < b.count; i++ {
		idx := (b.head + i) % b.capacity
		result[i] = b.events[idx]
	}
	b.head = 0
	b.count = 0
	return result
}

// evictExpired removes events older than the time window.
// Must be called with b.mu held.
func (b *RingBuffer) evictExpired(nowMs int64) {
	cutoff := nowMs - b.window.Milliseconds()
	for b.count > 0 {
		if b.events[b.head].Timestamp >= cutoff {
			break
		}
		b.head = (b.head + 1) % b.capacity
		b.count--
	}
}
