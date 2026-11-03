package main

import (
	"context"
	"errors"
	"log"
	"sort"
	"strings"
	"time"

	"wails-gui/internal/calendar"
	"wails-gui/storage"
)

// The first day's calendar read. It works from stored calendar rows only, so
// it never waits on EventKit, and every row passes the same privacy gate the
// Today day strip uses.
const (
	calendarFirstDayNextWindow     = 3 * time.Hour
	calendarFirstDayFreeHour       = 18
	calendarFirstDayFreeMinute     = 30
	calendarFirstDayFreeMinimum    = time.Hour
	calendarFirstDayPeopleLimit    = 20
	calendarFirstDayResourceDomain = "@resource.calendar.google.com"
	// EKParticipantRole nonParticipant, lower-cased as stored.
	calendarFirstDayNonParticipantRole = "nonparticipant"
)

type CalendarWeekPerson struct {
	Key              string `json:"key"` // lowercased email, or name when no email
	Name             string `json:"name"`
	Email            string `json:"email"`
	PrincipalID      string `json:"principalId"`
	NextMeetingTitle string `json:"nextMeetingTitle"`
	NextMeetingAt    int64  `json:"nextMeetingAt"` // unix ms, 0 when none left this week
	Recurring        bool   `json:"recurring"`     // the next meeting is a recurring event, if the stored row knows it; false otherwise
	MeetingsThisWeek int    `json:"meetingsThisWeek"`
}

type CalendarFirstDay struct {
	Connected         bool                     `json:"connected"`
	Paused            bool                     `json:"paused"`
	ReadsOff          bool                     `json:"readsOff"`
	MeetingsLeftToday int                      `json:"meetingsLeftToday"` // non-all-day events today that haven't ended
	PeopleLeftToday   int                      `json:"peopleLeftToday"`   // distinct non-me attendees in those
	PeopleThisWeek    int                      `json:"peopleThisWeek"`    // distinct visible attendees, before the display list limit
	People            []CalendarWeekPerson     `json:"people"`            // distinct non-me, non-resource, not-declined attendees of this week's events (local Mon..Sun), soonest next meeting first; at most 20
	NextMeeting       *CalendarFirstDayMeeting `json:"nextMeeting"`       // the next non-all-day meeting starting within 3 hours, with at least one other attendee; nil otherwise
	FreeRanges        []CalendarFreeRange      `json:"freeRanges"`        // today's gaps of 60+ minutes between now and 6:30 PM local (or the end of the last event if later), clipped to now
}

type CalendarFirstDayMeeting struct {
	Title         string   `json:"title"`
	Start         int64    `json:"start"`
	AttendeeNames []string `json:"attendeeNames"`
}

type CalendarFreeRange struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

// GetCalendarFirstDay reads the stored calendar for the first day's Today and
// Chat. It is a read of local rows only and never asks for calendar access.
func (a *App) GetCalendarFirstDay() (perfValue CalendarFirstDay) {
	defer logPageBinding("GetCalendarFirstDay", time.Now(), &perfValue)
	day, err := a.calendarFirstDayAt(time.Now())
	if err != nil {
		log.Printf("[calendar-first-day] unavailable: %v", err)
	}
	return day
}

func emptyCalendarFirstDay() CalendarFirstDay {
	return CalendarFirstDay{People: []CalendarWeekPerson{}, FreeRanges: []CalendarFreeRange{}}
}

func (a *App) calendarFirstDayAt(now time.Time) (CalendarFirstDay, error) {
	out := emptyCalendarFirstDay()
	if a == nil || a.store == nil {
		return out, errors.New("the store is not ready")
	}
	ctx := context.Background()
	connected, err := a.todayCalendarConnected(ctx)
	if err != nil {
		return out, err
	}
	out.Connected = connected
	// The native permission can already be granted before the first sync has
	// written its timestamp. Checking status does not request access or read
	// events, and remains meaningful while observation is paused.
	for _, permission := range todayBriefPermissionStatus(a).Items {
		if permission.Key == calendarPermissionKey && permission.Granted {
			out.Connected = true
		}
	}
	calendarSetting, err := a.store.GetUIState(ctx, permissionUIStateKey(calendarPermissionKey))
	if err != nil {
		return out, err
	}
	out.ReadsOff = calendarSetting == "false"
	policy, err := a.mcpReadPrivacy(ctx)
	if errors.Is(err, errMCPReadPaused) {
		out.Paused = true
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if !policy.calendar {
		return out, nil
	}

	now = now.In(time.Local)
	y, m, d := now.Date()
	dayStart := time.Date(y, m, d, 0, 0, 0, 0, time.Local)
	dayEnd := dayStart.AddDate(0, 0, 1)
	weekStart := dayStart.AddDate(0, 0, -((int(now.Weekday()) + 6) % 7))
	weekEnd := weekStart.AddDate(0, 0, 7)
	horizon := now.Add(calendarFirstDayNextWindow)
	queryEnd := weekEnd
	if horizon.After(queryEnd) {
		queryEnd = horizon
	}
	rows, err := a.store.ListCalendarEvents(ctx, weekStart.UnixMilli(), queryEnd.UnixMilli(), false)
	if err != nil {
		return out, err
	}

	nowMs := now.UnixMilli()
	dayStartMs, dayEndMs := dayStart.UnixMilli(), dayEnd.UnixMilli()
	weekStartMs, weekEndMs := weekStart.UnixMilli(), weekEnd.UnixMilli()
	var allowed []storage.CalendarEventRow
	var meetings []storage.CalendarEventRow
	for _, row := range rows {
		if !policy.calendarAllowed(row) {
			continue
		}
		allowed = append(allowed, row)
		if row.AllDay || row.EndAt <= row.StartAt {
			continue
		}
		meetings = append(meetings, row)
	}
	out.Connected = out.Connected || len(allowed) > 0
	if !out.Connected {
		return out, nil
	}

	people := map[string]*CalendarWeekPerson{}
	leftToday := map[string]bool{}
	for _, row := range meetings {
		if row.StartAt < weekStartMs || row.StartAt >= weekEndMs {
			continue
		}
		seen := map[string]bool{}
		for _, att := range calendarFirstDayOthers(row) {
			key := calendarFirstDayKey(att)
			if key == "" {
				continue
			}
			person := people[key]
			if person == nil {
				person = &CalendarWeekPerson{Key: key}
				people[key] = person
			}
			if person.Name == "" {
				person.Name = strings.TrimSpace(att.Name)
			}
			if person.Email == "" {
				person.Email = calendar.NormalizeEmail(att.Email)
			}
			if person.PrincipalID == "" {
				person.PrincipalID = att.PrincipalID
			}
			if !seen[key] {
				seen[key] = true
				person.MeetingsThisWeek++
			}
			if row.StartAt >= nowMs && (person.NextMeetingAt == 0 || row.StartAt < person.NextMeetingAt) {
				person.NextMeetingTitle = row.Title
				person.NextMeetingAt = row.StartAt
				person.Recurring = row.Recurring
			}
		}
		if row.StartAt >= dayStartMs && row.StartAt < dayEndMs && row.EndAt > nowMs {
			out.MeetingsLeftToday++
			for _, att := range calendarFirstDayOthers(row) {
				if key := calendarFirstDayKey(att); key != "" {
					leftToday[key] = true
				}
			}
		}
	}
	out.PeopleLeftToday = len(leftToday)
	out.PeopleThisWeek = len(people)
	out.People = calendarFirstDayPeople(people)
	out.NextMeeting = calendarFirstDayNext(meetings, nowMs, horizon.UnixMilli())
	out.FreeRanges = calendarFirstDayFreeRanges(meetings, now, dayStartMs, dayEndMs, dayStart)
	return out, nil
}

// calendarFirstDayOthers is the event's attendees other than me, leaving out
// declined people and resources.
func calendarFirstDayOthers(row storage.CalendarEventRow) []storage.CalendarAttendeeRow {
	var out []storage.CalendarAttendeeRow
	for _, att := range row.Attendees {
		if att.IsMe || att.Status == calendar.StatusDeclined || calendarFirstDayResource(att) {
			continue
		}
		out = append(out, att)
	}
	return out
}

// Stored rows carry no EventKit participant type, so a room is recognised by
// its role or a resource-calendar address.
func calendarFirstDayResource(att storage.CalendarAttendeeRow) bool {
	if strings.TrimSpace(strings.ToLower(att.Role)) == calendarFirstDayNonParticipantRole {
		return true
	}
	return strings.HasSuffix(calendar.NormalizeEmail(att.Email), calendarFirstDayResourceDomain)
}

func calendarFirstDayKey(att storage.CalendarAttendeeRow) string {
	if email := calendar.NormalizeEmail(att.Email); email != "" {
		return email
	}
	return strings.ToLower(strings.TrimSpace(att.Name))
}

func calendarFirstDayPeople(byKey map[string]*CalendarWeekPerson) []CalendarWeekPerson {
	list := make([]CalendarWeekPerson, 0, len(byKey))
	for _, person := range byKey {
		if person.Name == "" {
			person.Name = person.Email
		}
		list = append(list, *person)
	}
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i].NextMeetingAt, list[j].NextMeetingAt
		if (a == 0) != (b == 0) {
			return b == 0
		}
		if a != b {
			return a < b
		}
		if list[i].Name != list[j].Name {
			return list[i].Name < list[j].Name
		}
		return list[i].Key < list[j].Key
	})
	if len(list) > calendarFirstDayPeopleLimit {
		list = list[:calendarFirstDayPeopleLimit]
	}
	return list
}

// calendarFirstDayNext picks the earliest meeting that starts from now up to
// the horizon and has at least one other person in it.
func calendarFirstDayNext(meetings []storage.CalendarEventRow, nowMs, horizonMs int64) *CalendarFirstDayMeeting {
	var next *storage.CalendarEventRow
	var names []string
	for i := range meetings {
		row := meetings[i]
		if row.StartAt < nowMs || row.StartAt > horizonMs {
			continue
		}
		others := calendarFirstDayOthers(row)
		if len(others) == 0 {
			continue
		}
		if next == nil || row.StartAt < next.StartAt {
			next = &meetings[i]
			names = names[:0]
			for _, att := range others {
				name := strings.TrimSpace(att.Name)
				if name == "" {
					name = calendar.NormalizeEmail(att.Email)
				}
				names = append(names, name)
			}
		}
	}
	if next == nil {
		return nil
	}
	return &CalendarFirstDayMeeting{Title: next.Title, Start: next.StartAt, AttendeeNames: append([]string{}, names...)}
}

// calendarFirstDayFreeRanges returns the gaps of at least an hour between now
// and the end of the day's window. The window ends at 6:30 PM or at the end of
// the day's last busy event if that is later.
func calendarFirstDayFreeRanges(meetings []storage.CalendarEventRow, now time.Time, dayStartMs, dayEndMs int64, dayStart time.Time) []CalendarFreeRange {
	nowMs := now.UnixMilli()
	windowEnd := time.Date(dayStart.Year(), dayStart.Month(), dayStart.Day(), calendarFirstDayFreeHour, calendarFirstDayFreeMinute, 0, 0, time.Local)
	type span struct{ start, end int64 }
	var busy []storage.CalendarEventRow
	for _, row := range meetings {
		if row.StartAt < dayStartMs || row.StartAt >= dayEndMs || row.Availability == "free" {
			continue
		}
		busy = append(busy, row)
		if end := time.UnixMilli(row.EndAt).In(time.Local); end.After(windowEnd) {
			windowEnd = end
		}
	}
	windowEndMs := windowEnd.UnixMilli()
	var spans []span
	for _, row := range busy {
		start, end := max(row.StartAt, nowMs), min(row.EndAt, windowEndMs)
		if end > start {
			spans = append(spans, span{start, end})
		}
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	minMs := calendarFirstDayFreeMinimum.Milliseconds()
	out := []CalendarFreeRange{}
	cursor := nowMs
	for _, sp := range spans {
		if sp.start-cursor >= minMs {
			out = append(out, CalendarFreeRange{Start: cursor, End: sp.start})
		}
		if sp.end > cursor {
			cursor = sp.end
		}
	}
	if windowEndMs-cursor >= minMs {
		out = append(out, CalendarFreeRange{Start: cursor, End: windowEndMs})
	}
	return out
}
