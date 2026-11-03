package handoff

import (
	"strings"
	"time"
)

// PutOff is a Not now on Needs you (phone threads v1.1.0). The item is away
// until the next brief or two hours before it's due, whichever comes first,
// or until something new happens to it (its Mark changes).
type PutOff struct {
	At    int64  `json:"at"`    // unix ms it was put off
	Until int64  `json:"until"` // unix ms it comes back
	Times int    `json:"times"` // how many times it was put off since the last Keep
	Mark  string `json:"mark,omitempty"`
}

// PutOffDueLead is how long before an item is due it comes back.
const PutOffDueLead = 2 * time.Hour

// PutOffReturn is when an item put off at now comes back: the next brief, or
// two hours before it's due when that is sooner. Without either it comes back
// after fallback.
func PutOffReturn(now time.Time, nextBrief, due int64, fallback time.Duration) int64 {
	until := int64(0)
	if nextBrief > now.UnixMilli() {
		until = nextBrief
	}
	if due > 0 {
		lead := due - PutOffDueLead.Milliseconds()
		if until == 0 || lead < until {
			until = lead
		}
	}
	if until == 0 {
		until = now.Add(fallback).UnixMilli()
	}
	return until
}

// Next is the record after one more Not now.
func (p *PutOff) Next(now time.Time, until int64, mark string) *PutOff {
	times := 1
	if p != nil {
		times = p.Times + 1
	}
	return &PutOff{At: now.UnixMilli(), Until: until, Times: times, Mark: mark}
}

// Away reports whether the item is still put off.
func (p *PutOff) Away(now int64, mark string) bool {
	return p != nil && p.Until > now && p.Mark == mark
}

// AsksKeep reports the second return: Needs you asks Keep · Done · Forget.
func (p *PutOff) AsksKeep(now int64, mark string) bool {
	return p != nil && p.Times >= 2 && !p.Away(now, mark)
}

// EntryMark is what "something new happened" compares: the task's status,
// why it stopped, and its latest thread message.
func EntryMark(e Entry) string {
	last := ""
	if e.Thread != nil && len(e.Thread.Events) > 0 {
		last = e.Thread.Events[len(e.Thread.Events)-1].ID
	}
	return strings.Join([]string{string(e.Status), e.BlockedReason, e.StopCause, last}, "|")
}

// PutOffAway reports whether a waiting entry is put off now. A NotNowUntil
// written before PutOff existed still counts.
func PutOffAway(e Entry, now time.Time) bool {
	if e.Thread == nil || !IsChipStatus(e.Status) {
		return false
	}
	return e.Thread.NotNowUntil > now.UnixMilli() || e.Thread.PutOff.Away(now.UnixMilli(), EntryMark(e))
}
