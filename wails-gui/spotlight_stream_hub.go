package main

import (
	"errors"
	"sync"
)

const spotlightBacklogCap = 4096
const spotlightBacklogBytes = 8 << 20

var errSpotlightReplayUnavailable = errors.New("spotlight stream history unavailable")

type spotlightStreamEvent struct {
	Kind     string
	Data     string
	Sequence uint64
}

type spotlightReplay struct {
	next   uint64
	bytes  int
	events []spotlightStreamEvent
}

type spotlightStreamHub struct {
	mu      sync.Mutex
	subs    map[string][]chan spotlightStreamEvent
	history map[string]*spotlightReplay
}

func newSpotlightStreamHub() *spotlightStreamHub {
	return &spotlightStreamHub{subs: make(map[string][]chan spotlightStreamEvent), history: make(map[string]*spotlightReplay)}
}

// Subscribe atomically replays missed events and registers for new events.
// An expired cursor fails explicitly instead of silently omitting output.
func (h *spotlightStreamHub) Subscribe(sessionID string, after uint64) (<-chan spotlightStreamEvent, func(), error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	replay := h.history[sessionID]
	var events []spotlightStreamEvent
	if replay != nil {
		if after > replay.next || (after < replay.next && (len(replay.events) == 0 || after+1 < replay.events[0].Sequence)) {
			return nil, nil, errSpotlightReplayUnavailable
		}
		for _, event := range replay.events {
			if event.Sequence > after {
				events = append(events, event)
			}
		}
	} else if after != 0 {
		return nil, nil, errSpotlightReplayUnavailable
	}
	ch := make(chan spotlightStreamEvent, len(events)+64)
	for _, event := range events {
		ch <- event
	}
	h.subs[sessionID] = append(h.subs[sessionID], ch)
	cancel := func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		list := h.subs[sessionID]
		for i, subscriber := range list {
			if subscriber == ch {
				h.subs[sessionID] = append(list[:i], list[i+1:]...)
				close(ch)
				return
			}
		}
	}
	return ch, cancel, nil
}

// A slow subscriber is disconnected rather than silently losing events; it
// can then resume from its last received sequence while the history is held.
func (h *spotlightStreamHub) Publish(sessionID string, event spotlightStreamEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	replay := h.history[sessionID]
	if replay == nil {
		replay = &spotlightReplay{}
		h.history[sessionID] = replay
	}
	replay.next++
	event.Sequence = replay.next
	replay.events = append(replay.events, event)
	replay.bytes += len(event.Kind) + len(event.Data)
	for len(replay.events) > spotlightBacklogCap || (replay.bytes > spotlightBacklogBytes && len(replay.events) > 0) {
		replay.bytes -= len(replay.events[0].Kind) + len(replay.events[0].Data)
		replay.events[0] = spotlightStreamEvent{}
		replay.events = replay.events[1:]
	}
	subscribers := h.subs[sessionID]
	kept := subscribers[:0]
	for _, ch := range subscribers {
		select {
		case ch <- event:
			kept = append(kept, ch)
		default:
			close(ch)
		}
	}
	h.subs[sessionID] = kept
}

func (h *spotlightStreamHub) LatestSequence(sessionID string) uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if replay := h.history[sessionID]; replay != nil {
		return replay.next
	}
	return 0
}

func (h *spotlightStreamHub) CloseSession(sessionID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ch := range h.subs[sessionID] {
		close(ch)
	}
	delete(h.subs, sessionID)
	delete(h.history, sessionID)
}
