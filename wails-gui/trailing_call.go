package main

import "sync"

// trailingCall runs fn one at a time. A caller that arrives while a run is in
// progress waits for the next run, which starts after that arrival, so it never
// receives a result computed before it asked. Callers that arrive during the same
// run share the run that follows it. Unlike a plain singleflight, nothing is
// shared across an arrival, so a newer item or a withdrawal is never hidden
// behind an older result.
type trailingCall[T any] struct {
	mu      sync.Mutex
	running bool
	next    *trailingWaiters[T]
}

type trailingWaiters[T any] struct {
	done chan struct{}
	val  T
}

// Do returns fn's result for a run that started after this call arrived.
func (c *trailingCall[T]) Do(fn func() T) T {
	c.mu.Lock()
	if c.running {
		if c.next == nil {
			c.next = &trailingWaiters[T]{done: make(chan struct{})}
		}
		w := c.next
		c.mu.Unlock()
		<-w.done
		return w.val
	}
	c.running = true
	c.mu.Unlock()
	val := fn()
	c.mu.Lock()
	queued := c.next != nil
	if !queued {
		c.running = false
	}
	c.mu.Unlock()
	if queued {
		go c.drain(fn)
	}
	return val
}

// drain serves the queued callers, one run each, until none are queued.
func (c *trailingCall[T]) drain(fn func() T) {
	for {
		c.mu.Lock()
		w := c.next
		c.next = nil
		if w == nil {
			c.running = false
			c.mu.Unlock()
			return
		}
		c.mu.Unlock()
		w.val = fn()
		close(w.done)
	}
}
