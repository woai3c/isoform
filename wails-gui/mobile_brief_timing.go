package main

import (
	"sort"
	"time"

	"wails-gui/internal/engagement"
)

const (
	mobileBriefTimingHistory = 14
	mobileBriefTimingMinDays = 3
	mobileBriefTimingLead    = 10 * time.Minute
	mobileBriefTimingSettle  = 30 * time.Minute
)

// Timing has no content, identity, model input or execution authority.
type mobileBriefTimingDay struct {
	Date             string
	FirstActivity    time.Time
	LastActivity     time.Time
	FirstPhone       time.Time
	FirstOpen        time.Time
	FirstMeeting     time.Time
	LastMeeting      time.Time
	LastMeetingStart time.Time
}

type mobileBriefTimingInput struct {
	Days     []mobileBriefTimingDay
	Today    mobileBriefTimingDay
	Sleeping bool
	Quiet    engagement.QuietWindow
}

type mobileBriefTimingPrediction struct {
	mobileBriefSlotOccurrence
	// Deadline is when the brief can no longer be made: the next brief is
	// due, or its day ends (mobileBriefSlotCloses).
	Deadline time.Time
	Reason   string
	// Held is what pushed a slot with no window past its deadline.
	Held string
}

// mobileBriefTimingQuiet uses wall-clock minutes, including on DST transition
// days. Elapsed duration since midnight is not a local clock on those days.
func mobileBriefTimingQuiet(at time.Time, quiet engagement.QuietWindow) bool {
	minute := time.Duration(at.Hour())*time.Hour + time.Duration(at.Minute())*time.Minute
	if quiet.Start == quiet.End {
		return false
	}
	if quiet.Start < quiet.End {
		return minute >= quiet.Start && minute < quiet.End
	}
	return minute >= quiet.Start || minute < quiet.End
}

func mobileBriefTimingQuietEnd(at time.Time, quiet engagement.QuietWindow) time.Time {
	if !mobileBriefTimingQuiet(at, quiet) {
		return at
	}
	// Walk real minutes across DST gaps/repeats; constructing a nonexistent
	// wall-clock end can normalize backwards or skip a whole local day.
	end := at.Truncate(time.Minute)
	for i := 0; i < 26*60; i++ {
		end = end.Add(time.Minute)
		if !mobileBriefTimingQuiet(end, quiet) {
			return end
		}
	}
	return end
}

// The predictor is pure: fixture days and the current source projection are
// its only inputs. At least three distinct days are needed for a learned time.
func mobileBriefPredictDay(day time.Time, loc *time.Location, settings mobileBriefSettings, input mobileBriefTimingInput) []mobileBriefTimingPrediction {
	if loc == nil {
		loc = time.Local
	}
	day = day.In(loc)
	date := day.Format("2006-01-02")
	floor := day.AddDate(0, 0, -mobileBriefTimingHistory).Format("2006-01-02")
	var opens, phones, starts, ends []int
	seen := map[string]bool{}
	for _, sample := range input.Days {
		if sample.Date < floor || sample.Date >= date || seen[sample.Date] {
			continue
		}
		seen[sample.Date] = true
		minute := func(at time.Time) (int, bool) {
			local := at.In(loc)
			return local.Hour()*60 + local.Minute(), !at.IsZero() && local.Format("2006-01-02") == sample.Date
		}
		if m, ok := minute(sample.FirstOpen); ok && m < 12*60 {
			opens = append(opens, m)
		}
		if m, ok := minute(sample.FirstPhone); ok && m < 12*60 {
			phones = append(phones, m)
		}
		// A lone wake mark is not a meaningful work day.
		_, firstOK := minute(sample.FirstActivity)
		_, lastOK := minute(sample.LastActivity)
		meaningful := firstOK && lastOK && sample.LastActivity.Sub(sample.FirstActivity) >= 5*time.Minute
		if meaningful {
			if m, ok := minute(sample.FirstActivity); ok && m < 12*60 {
				starts = append(starts, m)
			}
		}
		last := sample.LastActivity
		if !meaningful {
			last = time.Time{}
		}
		if sample.LastMeeting.After(last) {
			last = sample.LastMeeting
		}
		if m, ok := minute(last); ok && m >= 12*60 {
			ends = append(ends, m)
		}
	}
	median := func(values []int) time.Time {
		sort.Ints(values)
		m := values[len(values)/2]
		if len(values)%2 == 0 {
			m = (m + values[len(values)/2-1]) / 2
		}
		return time.Date(day.Year(), day.Month(), day.Day(), m/60, m%60, 0, 0, loc)
	}
	sameDay := func(at time.Time) bool {
		return !at.IsZero() && at.In(loc).Format("2006-01-02") == date
	}
	var out []mobileBriefTimingPrediction
	for _, fixed := range mobileBriefTodaySlots(day, loc, settings) {
		// The fixed slot is the latest start; a scheduled build begins
		// mobileBriefBuildLead earlier, as learned times do.
		p := mobileBriefTimingPrediction{mobileBriefSlotOccurrence: fixed, Deadline: mobileBriefSlotCloses(fixed, loc, settings), Reason: "fixed_slot"}
		p.ScheduledAt = fixed.ScheduledAt.Add(-mobileBriefBuildLead)
		switch fixed.Slot {
		case mobileBriefSlotMorning:
			values, reason := starts, "learned_activity"
			if len(phones) >= mobileBriefTimingMinDays {
				values, reason = phones, "learned_presence"
			}
			if len(opens) >= mobileBriefTimingMinDays {
				values, reason = opens, "learned_opens"
			}
			if len(values) >= mobileBriefTimingMinDays {
				at := median(values).Add(-mobileBriefTimingLead)
				if at.Before(p.ScheduledAt) {
					p.ScheduledAt, p.Reason = at, reason
				}
			}
			if input.Today.Date == date && sameDay(input.Today.FirstMeeting) {
				at := input.Today.FirstMeeting.In(loc).Add(-mobileBriefTimingLead)
				if at.Before(p.ScheduledAt) {
					p.ScheduledAt, p.Reason = at, "before_meeting"
				}
			}
		case mobileBriefSlotEvening:
			if len(ends) >= mobileBriefTimingMinDays {
				at := median(ends).Add(mobileBriefTimingSettle)
				if at.Before(p.ScheduledAt) {
					p.ScheduledAt, p.Reason = at, "work_settled"
				}
			}
		}
		midnight := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
		if p.ScheduledAt.Before(midnight) {
			p.ScheduledAt = midnight
		}
		if !p.ScheduledAt.Before(p.Deadline) {
			p.ScheduledAt, p.Reason, p.Held = time.Time{}, "no_window", p.Reason
		}
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].ScheduledAt.Before(out[j].ScheduledAt)
	})
	return out
}
