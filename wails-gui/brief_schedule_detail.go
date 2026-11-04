package main

import (
	"errors"
	"sort"
	"time"
)

// The brief's own detail on Tasks › Scheduled (tasks review v1.0.0 §3): its
// schedule, and the last three runs of its slot with the missed reason only
// when the run record has one.

const briefScheduleDetailRuns = 3

var errBriefScheduleSlot = errors.New("brief slot must be morning or evening")

// BriefScheduleDetail is one brief slot as its detail shows it. Time is the
// slot's local clock (HH:MM); NextRunAt is Unix milliseconds, 0 when none.
type BriefScheduleDetail struct {
	Slot      string             `json:"slot"`
	Enabled   bool               `json:"enabled"`
	Time      string             `json:"time"`
	NextRunAt int64              `json:"nextRunAt"`
	Runs      []BriefScheduleRun `json:"runs"`
}

// BriefScheduleRun is one slot whose time has passed, newest first: ok with
// the brief it made, missed with Reason (asleep, closed, or empty when the
// record doesn't say), or failed while its window is still open. Attempts is
// how many tries failed, when the record counts them, and NextTryAt (failed
// only) is the scheduler's next try of that slot. An ok run that came late is Late, with Reason when the record
// says why. MadeNow is a brief made on request (Make it now) after the slot's
// time, listed beside the scheduled run it stood in for. Times are RFC 3339.
type BriefScheduleRun struct {
	BriefID          string `json:"briefId"`
	Status           string `json:"status"`
	ScheduledAt      string `json:"scheduledAt"`
	At               string `json:"at"`
	Reason           string `json:"reason,omitempty"`
	Late             bool   `json:"late,omitempty"`
	MadeNow          bool   `json:"madeNow,omitempty"`
	Attempts         int    `json:"attempts,omitempty"`
	FailureCause     string `json:"failureCause,omitempty"`
	FailureAssistant string `json:"failureAssistant,omitempty"`
	WaitingForSignIn bool   `json:"waitingForSignIn,omitempty"`
	NextTryAt        string `json:"nextTryAt,omitempty"`
}

// GetBriefScheduleDetail returns the morning or evening brief's detail.
func (a *App) GetBriefScheduleDetail(slot string) (BriefScheduleDetail, error) {
	if slot != mobileBriefSlotMorning && slot != mobileBriefSlotEvening {
		return BriefScheduleDetail{}, errBriefScheduleSlot
	}
	reader, ok := a.mobileBriefSource().(upcomingBriefReader)
	if !ok {
		return BriefScheduleDetail{}, errMobileBriefUnavailable
	}
	return briefScheduleDetailFrom(time.Now(), time.Local, reader.upcomingBrief(), slot), nil
}

// briefScheduleDetailFrom composes the detail from the brief's state. It reads
// no clock of its own. Today's run is always listed once its time has passed
// and something happened: its record, or a brief made on request after it.
func briefScheduleDetailFrom(now time.Time, loc *time.Location, state upcomingBriefState, slot string) BriefScheduleDetail {
	if loc == nil {
		loc = time.Local
	}
	item := upcomingBriefItem(now, loc, state, slot)
	detail := BriefScheduleDetail{Slot: slot, Enabled: item.Enabled, Time: item.Time, NextRunAt: item.NextRunAt, Runs: []BriefScheduleRun{}}
	type dated struct {
		run BriefScheduleRun
		at  time.Time
	}
	var runs []dated
	recorded := map[string]bool{}
	// madeNow lists the brief made on request inside a missed slot's window.
	madeNow := func(occurrence mobileBriefSlotOccurrence) {
		if state.MadeOnRequest == nil {
			return
		}
		id, at, ok := state.MadeOnRequest(occurrence.ScheduledAt, mobileBriefSlotCloses(occurrence, loc, state.Settings))
		if !ok {
			return
		}
		runs = append(runs, dated{BriefScheduleRun{BriefID: id, Status: mobileBriefRunOK, ScheduledAt: formatMobileBriefTime(occurrence.ScheduledAt), At: formatMobileBriefTime(at), MadeNow: true}, at})
	}
	for _, run := range state.Runs {
		if run.Slot != slot || (run.Status != mobileBriefRunOK && run.Status != mobileBriefRunMissed && run.Status != mobileBriefRunFailed) {
			continue
		}
		scheduled, err := time.Parse(time.RFC3339Nano, run.ScheduledAt)
		if err != nil {
			continue
		}
		projectedSchedule := run.ScheduledAt
		// Older adaptive runs stored the default preparation time rather than
		// the ready time. Correct that recognizable ten-minute offset in this
		// projection; other historical times may reflect a different schedule.
		if run.Slot == mobileBriefSlotEvening && run.Status == mobileBriefRunOK && !run.ScheduledReady {
			for _, fixed := range mobileBriefTodaySlots(scheduled.In(loc), loc, state.Settings) {
				if fixed.Slot == run.Slot && scheduled.Equal(fixed.ScheduledAt.Add(-mobileBriefBuildLead)) {
					scheduled = fixed.ScheduledAt
					projectedSchedule = formatMobileBriefTime(scheduled)
					break
				}
			}
		}
		recorded[run.BriefID] = true
		out := BriefScheduleRun{BriefID: run.BriefID, Status: run.Status, ScheduledAt: projectedSchedule, At: run.At, Reason: briefScheduleRecordedReason(run.Reason)}
		if run.Status == mobileBriefRunOK {
			at, err := time.Parse(time.RFC3339Nano, run.At)
			out.Late = err == nil && at.Sub(scheduled) > mobileBriefLateAfter
			if !out.Late {
				out.Reason = ""
			}
		} else {
			occurrence := mobileBriefSlotOccurrence{BriefID: run.BriefID, Slot: slot, ScheduledAt: scheduled}
			madeNow(occurrence)
			out.Attempts = run.Attempts
			out.FailureCause = run.FailureCause
			out.FailureAssistant = run.FailureAssistant
			out.WaitingForSignIn = state.Settings.Enabled && run.Status == mobileBriefRunFailed && run.FailureCause == "signed_out" && run.FailureAssistant != "" && !now.Before(scheduled) && now.Before(mobileBriefSlotCloses(occurrence, loc, state.Settings))
			// The next run is this slot's next try only while its window is open.
			if run.Status == mobileBriefRunFailed && run.FailureCause != "signed_out" && item.NextRunAt > 0 {
				if next := time.UnixMilli(item.NextRunAt); next.Before(mobileBriefSlotCloses(occurrence, loc, state.Settings)) {
					out.NextTryAt = formatMobileBriefTime(next)
				}
			}
		}
		runs = append(runs, dated{out, scheduled})
	}
	if state.Settings.Enabled {
		for _, occurrence := range mobileBriefTodaySlots(now, loc, state.Settings) {
			if occurrence.Slot != slot || now.Before(occurrence.ScheduledAt) || recorded[occurrence.BriefID] {
				continue
			}
			before := len(runs)
			madeNow(occurrence)
			if len(runs) == before {
				continue
			}
			reason := ""
			if state.MissedReason != nil {
				reason = briefScheduleRecordedReason(state.MissedReason(occurrence.ScheduledAt))
			}
			runs = append(runs, dated{BriefScheduleRun{BriefID: occurrence.BriefID, Status: mobileBriefRunMissed, ScheduledAt: formatMobileBriefTime(occurrence.ScheduledAt), Reason: reason}, occurrence.ScheduledAt})
		}
	}
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].at.After(runs[j].at) })
	for i := 0; i < len(runs) && i < briefScheduleDetailRuns; i++ {
		detail.Runs = append(detail.Runs, runs[i].run)
	}
	return detail
}

// briefScheduleRecordedReason keeps only the reasons Isoform observes.
func briefScheduleRecordedReason(reason string) string {
	switch reason {
	case mobileBriefMissedAsleep, mobileBriefMissedClosed:
		return reason
	default:
		return ""
	}
}
