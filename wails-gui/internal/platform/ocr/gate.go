package ocr

import "sync"

// gate is a priority mutex: only one holder at a time, and higher-priority
// acquirers are admitted before lower-priority ones when multiple are waiting.
//
// Lower-priority work cannot starve higher-priority work (by design), but
// higher-priority work CAN starve lower if it keeps arriving. That's the
// intended trade-off: the background OCR worker may pause for long stretches
// while interactive requests fly through, which is the behavior we want.
type gate struct {
	mu      sync.Mutex
	cond    *sync.Cond
	busy    bool
	hiWait  int
	midWait int
}

func newGate() *gate {
	g := &gate{}
	g.cond = sync.NewCond(&g.mu)
	return g
}

// globalGate serializes Vision OCR calls across all engine instances in the
// process. Every RecognizeJPEG*/RecognizeJPEGWithBoxes call must pass through
// it. Callers that construct a new Engine on every invocation
// (e.g. interactive highlight) still share this gate with the long-lived
// engine owned by the activity monitor.
var globalGate = newGate()

// acquire blocks until this caller is admitted. The returned release function
// must be called exactly once (typically via defer).
func (g *gate) acquire(p Priority) func() {
	g.mu.Lock()
	switch p {
	case PriorityHigh:
		g.hiWait++
		for g.busy {
			g.cond.Wait()
		}
		g.hiWait--
	case PriorityMid:
		g.midWait++
		for g.busy || g.hiWait > 0 {
			g.cond.Wait()
		}
		g.midWait--
	default: // PriorityLow
		for g.busy || g.hiWait > 0 || g.midWait > 0 {
			g.cond.Wait()
		}
	}
	g.busy = true
	g.mu.Unlock()

	return func() {
		g.mu.Lock()
		g.busy = false
		g.cond.Broadcast()
		g.mu.Unlock()
	}
}
