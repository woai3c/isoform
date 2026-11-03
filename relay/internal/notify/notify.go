// Package notify wakes long-poll handlers when the state they wait on changes.
// It is an in-process hint only: waiters also re-check storage on a timer.
package notify

import "sync"

// Notifier wakes every watcher of a key on its next Notify.
type Notifier struct {
	mu      sync.Mutex
	waiters map[string]*waiter
}

type waiter struct {
	ch    chan struct{}
	count int
}

// New returns an empty notifier.
func New() *Notifier {
	return &Notifier{waiters: map[string]*waiter{}}
}

// Watch returns a channel closed by the next Notify for key. The returned
// function releases the watch and must be called exactly once or more.
func (n *Notifier) Watch(key string) (<-chan struct{}, func()) {
	n.mu.Lock()
	defer n.mu.Unlock()
	w := n.waiters[key]
	if w == nil {
		w = &waiter{ch: make(chan struct{})}
		n.waiters[key] = w
	}
	w.count++
	var once sync.Once
	return w.ch, func() {
		once.Do(func() {
			n.mu.Lock()
			defer n.mu.Unlock()
			if n.waiters[key] == w {
				w.count--
				if w.count == 0 {
					delete(n.waiters, key)
				}
			}
		})
	}
}

// Notify closes the channel of every current watcher of key.
func (n *Notifier) Notify(key string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if w := n.waiters[key]; w != nil {
		close(w.ch)
		delete(n.waiters, key)
	}
}

// PairingKey names the wake-up channel for one pairing's state.
func PairingKey(pairingID string) string { return "pairing:" + pairingID }

// DeviceKey names the wake-up channel for one device's responses and access.
func DeviceKey(deviceID string) string { return "device:" + deviceID }
