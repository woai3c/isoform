package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// errSnapshotPending means no value exists yet and a refresh has been started.
// Bindings treat it as an empty result; the refresh's emit tells the page to
// read again.
var errSnapshotPending = errors.New("snapshot pending")

// snapshotRefresher serves one expensive read from its last good value.
//
//   - Get never waits for the loader. With a value it returns at once. Without
//     one it starts a refresh and returns errSnapshotPending (or the last
//     refresh error) at once; the refresh's emit tells the page to read again.
//     A value older than maxAge also starts a refresh, without delaying the read.
//   - Kick and Invalidate start a refresh in a goroutine. One load runs at a
//     time: a kick during a load marks exactly one follow-up.
//   - After a load, the fingerprint of the new value is compared with the last
//     published one. emit runs only when they differ.
//   - Drop removes the value and fences any load already running, so a value
//     read before a privacy withdrawal is never served again.
//   - readAfterWrites (optional, opt-in) makes a Get that follows an Invalidate
//     load inline before it answers, so the caller sees its own write. It only
//     applies when a value already exists; a cold read never loads inline.
//
// No lock is held while load, fingerprint or emit runs.
type snapshotRefresher[T any] struct {
	load        func() (T, error)
	fingerprint func(T) (string, error)
	emit        func()
	maxAge      time.Duration

	// runMu allows one load at a time, whether background or inline.
	runMu sync.Mutex

	mu           sync.Mutex
	value        T
	has          bool
	storedAt     time.Time
	lastErr      error
	running      bool
	again        bool
	generation   uint64
	published    string
	consistent   bool
	writeSeq     uint64
	attemptedSeq uint64
	// stamp, when set, names every input the value depends on. It is cheap to
	// compute and is read on each Get; a changed or empty stamp reloads inline.
	stamp          func() string
	attemptedStamp string
}

// newSnapshotRefresher builds a refresher. A nil fingerprint hashes the JSON
// of the value. A nil emit publishes nothing. A zero maxAge never revalidates
// on read.
func newSnapshotRefresher[T any](load func() (T, error), fingerprint func(T) (string, error), emit func(), maxAge time.Duration) *snapshotRefresher[T] {
	if fingerprint == nil {
		fingerprint = jsonFingerprint[T]
	}
	return &snapshotRefresher[T]{load: load, fingerprint: fingerprint, emit: emit, maxAge: maxAge}
}

// readAfterWrites makes a read that follows an Invalidate load again before it
// answers. Use it only where that load is acceptable on the request path after
// a write, and only once a value exists.
func (r *snapshotRefresher[T]) readAfterWrites() *snapshotRefresher[T] {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.consistent = true
	return r
}

// withStamp adds the input stamp. A read whose stamp moved since the last load
// loads again before it answers, so the value can never lag its inputs. A
// stamp that is empty counts as moved, because the inputs are unknown.
func (r *snapshotRefresher[T]) withStamp(stamp func() string) *snapshotRefresher[T] {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stamp = stamp
	return r
}

func (r *snapshotRefresher[T]) stampNow() string {
	if r.stamp == nil {
		return ""
	}
	return r.stamp()
}

// jsonFingerprint hashes the JSON encoding of a value.
func jsonFingerprint[T any](value T) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// Get returns the last good value. It answers in constant time: the only
// inline load is the opted-in read-after-write path for a value that exists.
func (r *snapshotRefresher[T]) Get() (T, error) {
	current := r.stampNow()
	r.mu.Lock()
	if r.consistent && r.has && (r.attemptedSeq != r.writeSeq || r.stampMoved(current)) {
		r.mu.Unlock()
		r.loadOnce()
		r.mu.Lock()
	}
	defer r.mu.Unlock()
	if r.has {
		if r.maxAge > 0 && time.Since(r.storedAt) > r.maxAge {
			r.startLocked()
		}
		return r.value, nil
	}
	r.startLocked()
	var zero T
	if r.lastErr != nil {
		return zero, r.lastErr
	}
	return zero, errSnapshotPending
}

// Fresh is for callers that build something from the data itself (the brief,
// first-day reads, a confirmation): they must never see an empty cold
// snapshot. With no value yet it loads inline, once, serialised with any
// background load by runMu. With a value it answers like Get. Only the
// Today UI bindings use Get, which never blocks.
func (r *snapshotRefresher[T]) Fresh() (T, error) {
	r.mu.Lock()
	has := r.has
	r.mu.Unlock()
	if !has {
		r.loadOnce()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.has {
		return r.value, nil
	}
	var zero T
	if r.lastErr != nil {
		return zero, r.lastErr
	}
	return zero, errSnapshotPending
}

// Kick starts a refresh, or marks one follow-up when a refresh is running.
func (r *snapshotRefresher[T]) Kick() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.startLocked()
}

// Invalidate is the writer hook. It marks the value stale and starts a
// refresh. Until that refresh lands, a background read keeps serving the last
// good value; a read-after-write refresher loads again before it answers.
func (r *snapshotRefresher[T]) Invalidate() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.writeSeq++
	r.startLocked()
}

// Drop forgets the value and fences any load already running. The next Get
// starts a fresh refresh, so nothing read before the drop is served again.
func (r *snapshotRefresher[T]) Drop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.generation++
	var zero T
	r.value, r.has, r.storedAt, r.lastErr, r.published = zero, false, time.Time{}, nil, ""
}

// startLocked requires r.mu. It starts the single runner or marks a follow-up.
func (r *snapshotRefresher[T]) startLocked() {
	if r.running {
		r.again = true
		return
	}
	r.running = true
	go r.run()
}

func (r *snapshotRefresher[T]) run() {
	for {
		r.loadOnce()
		r.mu.Lock()
		again := r.again
		r.again = false
		r.running = again
		r.mu.Unlock()
		if !again {
			return
		}
	}
}

// loadOnce runs the loader once and publishes the result. A load fenced by a
// Drop stores nothing.
func (r *snapshotRefresher[T]) loadOnce() {
	r.runMu.Lock()
	defer r.runMu.Unlock()

	stampBefore := r.stampNow()
	r.mu.Lock()
	gen, seq := r.generation, r.writeSeq
	r.mu.Unlock()

	value, err := r.load()
	var fp string
	var fpErr error
	if err == nil {
		fp, fpErr = r.fingerprint(value)
	}

	r.mu.Lock()
	emit := false
	if gen == r.generation {
		r.attemptedSeq, r.attemptedStamp = seq, stampBefore
		if err != nil {
			r.lastErr = err
		} else {
			r.value, r.has, r.storedAt, r.lastErr = value, true, time.Now(), nil
			// A fingerprint that cannot be computed counts as a change.
			if fpErr != nil || fp != r.published {
				r.published = fp
				emit = r.emit != nil
			}
		}
	}
	r.mu.Unlock()

	if emit {
		r.emit()
	}
}

// snapshotCache holds one refresher per key, such as a date, or "" for a
// single snapshot. Writers and privacy paths reach every refresher through it.
type snapshotCache[T any] struct {
	mu    sync.Mutex
	items map[string]*snapshotRefresher[T]
}

// get returns the refresher for key, building it on first use. build must not
// call back into the cache.
func (c *snapshotCache[T]) get(key string, build func() *snapshotRefresher[T]) *snapshotRefresher[T] {
	c.mu.Lock()
	defer c.mu.Unlock()
	if r := c.items[key]; r != nil {
		return r
	}
	if c.items == nil {
		c.items = map[string]*snapshotRefresher[T]{}
	}
	r := build()
	c.items[key] = r
	return r
}

func (c *snapshotCache[T]) each(fn func(*snapshotRefresher[T])) {
	c.mu.Lock()
	list := make([]*snapshotRefresher[T], 0, len(c.items))
	for _, r := range c.items {
		list = append(list, r)
	}
	c.mu.Unlock()
	for _, r := range list {
		fn(r)
	}
}

func (c *snapshotCache[T]) invalidateAll() {
	c.each(func(r *snapshotRefresher[T]) { r.Invalidate() })
}

func (c *snapshotCache[T]) dropAll() {
	c.each(func(r *snapshotRefresher[T]) { r.Drop() })
}

// snapshotPendingIsEmpty maps the "no value yet" state to an empty result. The
// refresh that is running will emit when there is something to show.
func snapshotPendingIsEmpty(err error) error {
	if errors.Is(err, errSnapshotPending) {
		return nil
	}
	return err
}

// stampMoved reports whether the inputs changed since the last load began. The
// caller holds r.mu; current was read before taking it.
func (r *snapshotRefresher[T]) stampMoved(current string) bool {
	return r.stamp != nil && (current == "" || current != r.attemptedStamp)
}
