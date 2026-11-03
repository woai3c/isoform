package main

import (
	"sync"
	"time"
)

type passiveTextAutomationRead struct {
	done     chan struct{}
	deadline time.Time
	value    textAutomationState
}

// This coalesces uninterruptible OS IO, not permission answers. No completed
// result is retained. Unknown uses the text channel's existing rule: keep the
// last recorded answer, and require a live grant again before sending.
type passiveTextAutomationProbe struct {
	mu      sync.Mutex
	pending *passiveTextAutomationRead
}

func (p *passiveTextAutomationProbe) read(budget time.Duration, check func() textAutomationState) textAutomationState {
	p.mu.Lock()
	read := p.pending
	if read == nil {
		read = &passiveTextAutomationRead{done: make(chan struct{}), deadline: time.Now().Add(budget)}
		p.pending = read
		go func() {
			value := check()
			p.mu.Lock()
			read.value = value
			p.pending = nil
			close(read.done)
			p.mu.Unlock()
		}()
	}
	p.mu.Unlock()
	// Concurrent readers share a normal live answer. Once the original wait
	// expires, later readers return Unknown immediately until the OS finishes.
	remaining := min(budget, time.Until(read.deadline))
	if remaining <= 0 {
		return textAutomationUnknown
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-read.done:
		return read.value
	case <-timer.C:
		return textAutomationUnknown
	}
}
