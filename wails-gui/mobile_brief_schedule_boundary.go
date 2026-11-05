package main

import (
	"time"
)

// mobileBriefScheduleBoundary is the moment the schedule's shown next-run
// prediction changes: its predicted time, once it is after now. The page
// reads the schedule every time this passes, so nothing polls. ok is false
// when no prediction is shown (schedule off, or fixed times).
func mobileBriefScheduleBoundary(now time.Time, view MobileBriefSchedule) (time.Time, bool) {
	if view.NextPredictedAt == "" {
		return time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339, view.NextPredictedAt)
	if err != nil || !at.After(now) {
		return time.Time{}, false
	}
	return at, true
}

// mobileBriefScheduleConfigChanged reports whether a save changed what the
// person set, as opposed to re-saving the same schedule.
func mobileBriefScheduleConfigChanged(before, after MobileBriefSchedule) bool {
	if before.Enabled != after.Enabled || before.Morning != after.Morning || before.Evening != after.Evening || before.FeedEnabled != after.FeedEnabled {
		return true
	}
	if before.AdaptiveTiming == nil || after.AdaptiveTiming == nil {
		return before.AdaptiveTiming != after.AdaptiveTiming
	}
	return *before.AdaptiveTiming != *after.AdaptiveTiming
}

// mobileBriefScheduleArm owns the single timer for the schedule's shown
// prediction. A save, a new brief or a fired boundary re-arms it; it is never
// a ticker.
type mobileBriefScheduleArm struct {
	timer boundaryTimer
	// predicted is the prediction last armed, so a re-arm can tell whether
	// the page's view moved. Guarded by timer.run.
	predicted string
}

// armFor arms the timer for view's next boundary, replacing any earlier arm,
// and reports whether the shown prediction changed since the last arm.
func (m *mobileBriefScheduleArm) armFor(view MobileBriefSchedule, now time.Time, fire func()) bool {
	m.timer.run.Lock()
	defer m.timer.run.Unlock()
	predicted := view.NextPredictedSlot + "@" + view.NextPredictedAt
	changed := predicted != m.predicted
	m.predicted = predicted
	at, ok := mobileBriefScheduleBoundary(now, view)
	if !ok {
		m.timer.arm(time.Time{}, nil)
		return changed
	}
	m.timer.arm(at, fire)
	return changed
}

// refreshMobileBriefSchedule re-reads the schedule and re-arms the timer. It
// returns true when the shown prediction changed. Off the request path: it is
// called after a save, after a new brief and when a boundary fires.
func (a *App) refreshMobileBriefSchedule() bool {
	if a == nil {
		return false
	}
	svc := a.mobileBriefSource()
	if svc == nil {
		a.mobileSchedule.timer.arm(time.Time{}, nil)
		return false
	}
	view, err := svc.Schedule()
	if err != nil {
		a.mobileSchedule.timer.arm(time.Time{}, nil)
		return false
	}
	return a.mobileSchedule.armFor(view, time.Now(), func() {
		a.emitBrowserEvent("mobile:updated")
		a.refreshMobileBriefSchedule()
	})
}
