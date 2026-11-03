package activitymon

import "time"

func coalesceStateEvents(events []ActivityEvent, interval time.Duration) []ActivityEvent {
	if len(events) <= 1 || interval <= 0 {
		return events
	}

	intervalMs := int64(interval / time.Millisecond)
	if intervalMs <= 0 {
		return events
	}

	out := make([]ActivityEvent, 0, len(events))
	var pending ActivityEvent
	hasPending := false
	pendingWindowStart := int64(0)

	flushPending := func() {
		if hasPending {
			out = append(out, pending)
			hasPending = false
			pendingWindowStart = 0
		}
	}

	for _, evt := range events {
		if !isCoalescibleStateEvent(evt.Kind) {
			flushPending()
			out = append(out, evt)
			continue
		}

		if !hasPending {
			pending = evt
			hasPending = true
			pendingWindowStart = evt.Timestamp
			continue
		}

		if evt.Timestamp-pendingWindowStart < intervalMs {
			pending = evt
			continue
		}

		flushPending()
		pending = evt
		hasPending = true
		pendingWindowStart = evt.Timestamp
	}

	flushPending()
	return out
}

func isCoalescibleStateEvent(kind EventKind) bool {
	switch kind {
	case EventAppSwitch, EventFocus:
		return true
	default:
		return false
	}
}
