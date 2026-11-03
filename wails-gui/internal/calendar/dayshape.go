package calendar

import (
	"fmt"
	"strings"
	"time"
)

// Working hours bound the free blocks a day shape reports, so an empty
// evening is not listed as free time.
const (
	WorkdayStartHour = 9
	WorkdayEndHour   = 18
	// MinFreeBlock is the shortest gap worth calling free time.
	MinFreeBlock = 30 * time.Minute
	// BackToBackGap is the longest gap between two meetings that still
	// counts as back to back.
	BackToBackGap = 5 * time.Minute
	// PendingInviteHorizon is how far ahead a pending invite still matters.
	PendingInviteHorizon = 48 * time.Hour
)

// Block is a span of the day.
type Block struct {
	Start time.Time
	End   time.Time
	// Events is how many events a back-to-back block holds; zero for a free
	// block.
	Events int
}

// Conflict is a pair of busy, accepted events that overlap.
type Conflict struct {
	First  Event
	Second Event
}

// DayShape is what the morning brief says about the day before any meeting
// starts: when the first one is, where the back-to-back stretches and the
// free blocks are, what clashes, and which invites still wait for an answer.
type DayShape struct {
	Date           time.Time
	Events         []Event
	FirstMeeting   *Event
	BackToBack     []Block
	FreeBlocks     []Block
	Conflicts      []Conflict
	PendingInvites []Event
	MeetingMinutes int
}

// ShapeDay reduces the events of one local day to its shape. day is any
// instant of that day in loc; events outside the day are ignored, all-day
// events count only as pending invites and conflicts never include them.
func ShapeDay(events []Event, day time.Time, loc *time.Location, now time.Time) DayShape {
	if loc == nil {
		loc = time.UTC
	}
	local := day.In(loc)
	dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	dayEnd := dayStart.AddDate(0, 0, 1)

	shape := DayShape{Date: dayStart}
	var timed []Event
	for _, ev := range events {
		if !ev.Overlaps(dayStart, dayEnd) {
			continue
		}
		shape.Events = append(shape.Events, ev)
		if ev.MyStatus == StatusPending && ev.End.After(now) && ev.Start.Before(now.Add(PendingInviteHorizon)) {
			shape.PendingInvites = append(shape.PendingInvites, ev)
		}
		if ev.AllDay || !ev.Busy() {
			continue
		}
		timed = append(timed, ev)
	}
	SortByStart(shape.Events)
	SortByStart(timed)

	for i := range timed {
		ev := timed[i]
		if ev.IsMeetingLike() && shape.FirstMeeting == nil {
			shape.FirstMeeting = &timed[i]
		}
		shape.MeetingMinutes += int(ev.End.Sub(ev.Start).Minutes())
		for j := i + 1; j < len(timed); j++ {
			other := timed[j]
			if !other.Start.Before(ev.End) {
				break
			}
			if ev.MyStatus == StatusDeclined || other.MyStatus == StatusDeclined {
				continue
			}
			shape.Conflicts = append(shape.Conflicts, Conflict{First: ev, Second: other})
		}
	}

	// Back-to-back blocks: runs of two or more busy events with at most
	// BackToBackGap between one's end and the next one's start.
	var run Block
	for i := range timed {
		ev := timed[i]
		if run.Events == 0 {
			run = Block{Start: ev.Start, End: ev.End, Events: 1}
			continue
		}
		// Overlapping events are a conflict, not a back-to-back run.
		if gap := ev.Start.Sub(run.End); gap >= 0 && gap <= BackToBackGap {
			if ev.End.After(run.End) {
				run.End = ev.End
			}
			run.Events++
			continue
		}
		if run.Events >= 2 {
			shape.BackToBack = append(shape.BackToBack, run)
		}
		run = Block{Start: ev.Start, End: ev.End, Events: 1}
	}
	if run.Events >= 2 {
		shape.BackToBack = append(shape.BackToBack, run)
	}

	// Free blocks inside working hours, between busy events.
	workStart := time.Date(local.Year(), local.Month(), local.Day(), WorkdayStartHour, 0, 0, 0, loc)
	workEnd := time.Date(local.Year(), local.Month(), local.Day(), WorkdayEndHour, 0, 0, 0, loc)
	cursor := workStart
	for _, ev := range timed {
		if !ev.End.After(cursor) {
			continue
		}
		if ev.Start.Sub(cursor) >= MinFreeBlock {
			end := ev.Start
			if end.After(workEnd) {
				end = workEnd
			}
			if end.Sub(cursor) >= MinFreeBlock {
				shape.FreeBlocks = append(shape.FreeBlocks, Block{Start: cursor, End: end})
			}
		}
		if ev.End.After(cursor) {
			cursor = ev.End
		}
		if !cursor.Before(workEnd) {
			break
		}
	}
	if cursor.Before(workEnd) && workEnd.Sub(cursor) >= MinFreeBlock {
		shape.FreeBlocks = append(shape.FreeBlocks, Block{Start: cursor, End: workEnd})
	}
	return shape
}

// ClockLayout is the default clock format: 24-hour, "15:04".
const ClockLayout = "15:04"

// Facts renders the shape as short plain sentences for the brief. It returns
// nothing for a day without timed events.
func (s DayShape) Facts(loc *time.Location) []string {
	return s.FactsIn(loc, ClockLayout)
}

// FactsIn is Facts with times in the given clock layout ("3:04 PM").
func (s DayShape) FactsIn(loc *time.Location, layout string) []string {
	if loc == nil {
		loc = time.UTC
	}
	if layout == "" {
		layout = ClockLayout
	}
	var facts []string
	if s.FirstMeeting != nil && strings.TrimSpace(s.FirstMeeting.Title) != "" {
		if s.FirstMeeting.Start.IsZero() {
			facts = append(facts, fmt.Sprintf("First meeting: %s.", s.FirstMeeting.Title))
		} else {
			facts = append(facts, fmt.Sprintf("First meeting at %s: %s.", s.FirstMeeting.Start.In(loc).Format(layout), s.FirstMeeting.Title))
		}
	}
	for _, block := range s.BackToBack {
		facts = append(facts, fmt.Sprintf("Back to back %s to %s (%d meetings).", block.Start.In(loc).Format(layout), block.End.In(loc).Format(layout), block.Events))
	}
	if len(s.FreeBlocks) > 0 {
		parts := make([]string, 0, len(s.FreeBlocks))
		for _, block := range s.FreeBlocks {
			if block.Start.IsZero() || block.End.IsZero() || !block.End.After(block.Start) {
				continue
			}
			parts = append(parts, block.Start.In(loc).Format(layout)+" to "+block.End.In(loc).Format(layout))
		}
		if len(parts) > 0 {
			facts = append(facts, "Free "+strings.Join(parts, ", ")+".")
		}
	}
	for _, c := range s.Conflicts {
		facts = append(facts, fmt.Sprintf("Conflict: %s overlaps %s.", c.First.Title, c.Second.Title))
	}
	if n := len(s.PendingInvites); n == 1 {
		facts = append(facts, fmt.Sprintf("1 invite waits for an answer: %s.", s.PendingInvites[0].Title))
	} else if n > 1 {
		facts = append(facts, fmt.Sprintf("%d invites wait for an answer.", n))
	}
	return facts
}

// TimeRange formats an event's span in loc as "15:04 to 16:00", or "All day".
func TimeRange(ev Event, loc *time.Location) string {
	return TimeRangeIn(ev, loc, ClockLayout)
}

// TimeRangeIn is TimeRange with times in the given clock layout.
func TimeRangeIn(ev Event, loc *time.Location, layout string) string {
	if loc == nil {
		loc = time.UTC
	}
	if layout == "" {
		layout = ClockLayout
	}
	if ev.AllDay {
		return "All day"
	}
	return ev.Start.In(loc).Format(layout) + " to " + ev.End.In(loc).Format(layout)
}

// Place is where an event happens: the conference platform when it has a
// link, otherwise the location, otherwise nothing.
func Place(ev Event) string {
	if ev.ConferenceHost != "" {
		return ev.ConferenceHost
	}
	return strings.TrimSpace(ev.Location)
}

// AttendeeNames lists the other attendees' names (or addresses), bounded to
// limit entries, with "+N" when more exist.
func AttendeeNames(ev Event, limit int) string {
	var names []string
	for _, a := range ev.Attendees {
		if a.IsMe {
			continue
		}
		name := a.Name
		if name == "" {
			name = a.Email
		}
		if name != "" {
			names = append(names, name)
		}
	}
	if limit > 0 && len(names) > limit {
		rest := len(names) - limit
		names = append(names[:limit], fmt.Sprintf("+%d", rest))
	}
	return strings.Join(names, ", ")
}
