package main

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"wails-gui/internal/calendar"
	"wails-gui/storage"
)

// calendarFirstDayNow is a Wednesday, so the local week runs Mon Oct 26 to Sun Nov 1.
func calendarFirstDayNow() time.Time {
	return time.Date(2026, 10, 28, 13, 0, 0, 0, time.Local)
}

func calendarFirstDayEvent(id, title string, start, end time.Time, attendees ...storage.CalendarAttendeeRow) storage.CalendarEventRow {
	return storage.CalendarEventRow{
		ID: id, Title: title, Source: calendar.SourceEventKit, Provider: "eventkit",
		StartAt: start.UnixMilli(), EndAt: end.UnixMilli(), Availability: "busy", Status: "confirmed",
		MyStatus: calendar.StatusAccepted, Attendees: attendees,
	}
}

func calendarFirstDayPerson(name, email string) storage.CalendarAttendeeRow {
	return storage.CalendarAttendeeRow{Name: name, Email: email, Status: calendar.StatusAccepted, Role: "required"}
}

func calendarFirstDayMe() storage.CalendarAttendeeRow {
	return storage.CalendarAttendeeRow{Name: "Me", Email: "me@example.test", IsMe: true, Status: calendar.StatusAccepted, Role: "required"}
}

// seedCalendarFirstDay writes the fixture the tests read. Times are local.
func seedCalendarFirstDay(t *testing.T, a *App, now time.Time) {
	t.Helper()
	day := func(h, m int) time.Time { return time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, time.Local) }
	maya := calendarFirstDayPerson("Maya", "maya@example.test")
	sam := calendarFirstDayPerson("Sam", "sam@example.test")
	dan := calendarFirstDayPerson("Dan", "dan@example.test")
	dan.Status = calendar.StatusDeclined
	room := storage.CalendarAttendeeRow{Name: "Room 4", Email: "room@resource.calendar.google.com", Role: "nonparticipant", Status: calendar.StatusAccepted}
	lee := calendarFirstDayPerson("Lee", "lee@example.test")
	kim := calendarFirstDayPerson("Kim", "kim@example.test")
	priya := storage.CalendarAttendeeRow{Name: "Priya"}
	ghost := calendarFirstDayPerson("Ghost", "ghost@example.test")
	pat := calendarFirstDayPerson("Pat", "pat@example.test")
	old := calendarFirstDayPerson("Old", "old@example.test")

	oneOnOne := calendarFirstDayEvent("maya-1on1", "Maya 1:1", day(14, 30), day(15, 0), calendarFirstDayMe(), maya)
	oneOnOne.Recurring = true
	standup := calendarFirstDayEvent("standup", "Halcyon standup", day(16, 0), day(17, 0), calendarFirstDayMe(), sam, dan, room)
	lunch := calendarFirstDayEvent("lunch", "Lunch", day(12, 0), day(13, 0), calendarFirstDayMe(), maya, priya)
	solo := calendarFirstDayEvent("focus", "Focus", day(17, 30), day(18, 0), calendarFirstDayMe())
	tomorrow := calendarFirstDayEvent("review", "Review", day(10, 0).AddDate(0, 0, 1), day(11, 0).AddDate(0, 0, 1), calendarFirstDayMe(), lee, maya)
	sunday := calendarFirstDayEvent("planning", "Sunday planning", day(9, 0).AddDate(0, 0, 4), day(10, 0).AddDate(0, 0, 4), calendarFirstDayMe(), kim)
	lastWeek := calendarFirstDayEvent("last-week", "Last week sync", day(9, 0).AddDate(0, 0, -6), day(10, 0).AddDate(0, 0, -6), calendarFirstDayMe(), old)
	allDay := calendarFirstDayEvent("offsite", "Offsite", day(0, 0), day(0, 0).AddDate(0, 0, 1), calendarFirstDayMe(), pat)
	allDay.AllDay = true
	declined := calendarFirstDayEvent("skipped", "Skipped", day(15, 0), day(15, 30), calendarFirstDayMe(), ghost)
	declined.MyStatus = calendar.StatusDeclined
	canceled := calendarFirstDayEvent("canceled", "Canceled", day(15, 0), day(15, 30), calendarFirstDayMe(), ghost)
	canceled.Status = "canceled"

	rows := []storage.CalendarEventRow{oneOnOne, standup, lunch, solo, tomorrow, sunday, lastWeek, allDay, declined, canceled}
	if err := a.store.UpsertCalendarEvents(t.Context(), rows, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
}

func setCalendarFirstDayPermission(t *testing.T, a *App, on bool) {
	t.Helper()
	value := "true"
	if !on {
		value = "false"
	}
	if err := a.store.SetUIState(t.Context(), permissionUIStateKey(calendarPermissionKey), value); err != nil {
		t.Fatal(err)
	}
}

func TestCalendarFirstDayReadsStoredRowsForToday(t *testing.T) {
	a := newMCPReadTestApp(t)
	setCalendarFirstDayPermission(t, a, true)
	now := calendarFirstDayNow()
	seedCalendarFirstDay(t, a, now)

	got, err := a.calendarFirstDayAt(now)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Connected {
		t.Fatal("connected = false with stored rows")
	}
	// Maya 1:1, Halcyon standup and Focus are left today; Lunch has ended, the
	// all-day offsite and the declined and canceled events never count.
	if got.MeetingsLeftToday != 3 {
		t.Fatalf("meetingsLeftToday = %d, want 3", got.MeetingsLeftToday)
	}
	// Maya and Sam: Dan declined and the room is a resource.
	if got.PeopleLeftToday != 2 {
		t.Fatalf("peopleLeftToday = %d, want 2", got.PeopleLeftToday)
	}
	if got.NextMeeting == nil {
		t.Fatal("nextMeeting = nil, want Maya 1:1")
	}
	wantNext := &CalendarFirstDayMeeting{Title: "Maya 1:1", Start: time.Date(2026, 10, 28, 14, 30, 0, 0, time.Local).UnixMilli(), AttendeeNames: []string{"Maya"}}
	if !reflect.DeepEqual(got.NextMeeting, wantNext) {
		t.Fatalf("nextMeeting = %+v, want %+v", got.NextMeeting, wantNext)
	}
	// Maya, Sam, Lee and Kim this week, then Priya (no email, no meeting left).
	// Maya's next meeting is her 1:1 today; Priya sorts last with no next meeting.
	var keys []string
	for _, person := range got.People {
		keys = append(keys, person.Key)
	}
	wantKeys := []string{"maya@example.test", "sam@example.test", "lee@example.test", "kim@example.test", "priya"}
	if !reflect.DeepEqual(keys, wantKeys) {
		t.Fatalf("people keys = %v, want %v", keys, wantKeys)
	}
	maya := got.People[0]
	if maya.NextMeetingTitle != "Maya 1:1" || maya.NextMeetingAt != wantNext.Start || !maya.Recurring || maya.MeetingsThisWeek != 3 {
		t.Fatalf("maya = %+v", maya)
	}
	priya := got.People[4]
	if priya.Name != "Priya" || priya.Email != "" || priya.NextMeetingAt != 0 || priya.MeetingsThisWeek != 1 {
		t.Fatalf("priya = %+v", priya)
	}
	for _, person := range got.People {
		if person.Key == "dan@example.test" || person.Key == "ghost@example.test" || person.Key == "old@example.test" || person.Key == "pat@example.test" {
			t.Fatalf("excluded person admitted: %+v", person)
		}
	}
}

func TestCalendarFirstDayFreeRangesAreGapsOfAnHourUpTo630(t *testing.T) {
	a := newMCPReadTestApp(t)
	setCalendarFirstDayPermission(t, a, true)
	now := calendarFirstDayNow()
	seedCalendarFirstDay(t, a, now)

	got, err := a.calendarFirstDayAt(now)
	if err != nil {
		t.Fatal(err)
	}
	day := func(h, m int) int64 { return time.Date(2026, 10, 28, h, m, 0, 0, time.Local).UnixMilli() }
	want := []CalendarFreeRange{
		{Start: day(13, 0), End: day(14, 30)},
		{Start: day(15, 0), End: day(16, 0)},
	}
	if !reflect.DeepEqual(got.FreeRanges, want) {
		t.Fatalf("freeRanges = %+v, want %+v", got.FreeRanges, want)
	}
}

func TestCalendarFirstDayAfterTheWorkdayHasNoMeetingsOrGaps(t *testing.T) {
	a := newMCPReadTestApp(t)
	setCalendarFirstDayPermission(t, a, true)
	seedCalendarFirstDay(t, a, calendarFirstDayNow())
	evening := time.Date(2026, 10, 28, 19, 0, 0, 0, time.Local)

	got, err := a.calendarFirstDayAt(evening)
	if err != nil {
		t.Fatal(err)
	}
	if got.MeetingsLeftToday != 0 || got.PeopleLeftToday != 0 || got.NextMeeting != nil || len(got.FreeRanges) != 0 {
		t.Fatalf("evening = %+v", got)
	}
	// The week's people still show, and Maya's next meeting is tomorrow's review.
	tomorrow := time.Date(2026, 10, 29, 10, 0, 0, 0, time.Local).UnixMilli()
	if len(got.People) == 0 || got.People[0].Key != "lee@example.test" && got.People[0].Key != "maya@example.test" {
		t.Fatalf("people = %+v", got.People)
	}
	for _, person := range got.People {
		if person.Key == "maya@example.test" && person.NextMeetingAt != tomorrow {
			t.Fatalf("maya next = %d, want tomorrow review %d", person.NextMeetingAt, tomorrow)
		}
	}
}

func TestCalendarFirstDayNextMeetingNeedsAnotherPersonWithinThreeHours(t *testing.T) {
	a := newMCPReadTestApp(t)
	setCalendarFirstDayPermission(t, a, true)
	now := calendarFirstDayNow()
	solo := calendarFirstDayEvent("solo", "Solo block", now.Add(time.Hour), now.Add(2*time.Hour), calendarFirstDayMe())
	later := calendarFirstDayEvent("later", "Far away", now.Add(3*time.Hour+time.Minute), now.Add(4*time.Hour), calendarFirstDayMe(), calendarFirstDayPerson("Lee", "lee@example.test"))
	if err := a.store.UpsertCalendarEvents(t.Context(), []storage.CalendarEventRow{solo, later}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	got, err := a.calendarFirstDayAt(now)
	if err != nil {
		t.Fatal(err)
	}
	if got.NextMeeting != nil {
		t.Fatalf("nextMeeting = %+v, want nil", got.NextMeeting)
	}
	// The later event has Lee, so one person is left today; the solo block has none.
	if got.MeetingsLeftToday != 2 || got.PeopleLeftToday != 1 {
		t.Fatalf("left today = %d meetings, %d people", got.MeetingsLeftToday, got.PeopleLeftToday)
	}
}

func TestCalendarFirstDayOffWithoutCalendarAccess(t *testing.T) {
	a := newMCPReadTestApp(t)
	setCalendarFirstDayPermission(t, a, false)
	now := calendarFirstDayNow()
	seedCalendarFirstDay(t, a, now)

	got, err := a.calendarFirstDayAt(now)
	if err != nil {
		t.Fatal(err)
	}
	want := emptyCalendarFirstDay()
	want.ReadsOff = true
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("calendar off = %+v, want %+v", got, want)
	}
}

func TestCalendarFirstDayConnectionSurvivesPrivacyPause(t *testing.T) {
	for _, mode := range []string{"reads-off", "paused"} {
		t.Run(mode, func(t *testing.T) {
			a := newMCPReadTestApp(t)
			setCalendarFirstDayPermission(t, a, mode != "reads-off")
			if err := a.store.SetUIState(t.Context(), calendarLastSyncKey, time.Now().Format(time.RFC3339)); err != nil {
				t.Fatal(err)
			}
			seedCalendarFirstDay(t, a, calendarFirstDayNow())
			if mode == "paused" {
				if err := a.SetClosedEyesModeEnabled(true); err != nil {
					t.Fatal(err)
				}
			}
			got, err := a.calendarFirstDayAt(calendarFirstDayNow())
			if err != nil {
				t.Fatal(err)
			}
			if !got.Connected || got.Paused != (mode == "paused") || got.ReadsOff != (mode == "reads-off") || len(got.People) != 0 || got.PeopleThisWeek != 0 || got.NextMeeting != nil || len(got.FreeRanges) != 0 || got.MeetingsLeftToday != 0 {
				t.Fatalf("privacy state: %+v", got)
			}
		})
	}
}

func TestCalendarFirstDayGrantedBeforeFirstSync(t *testing.T) {
	previous := todayBriefPermissionStatus
	todayBriefPermissionStatus = func(*App) PlatformPermissions {
		return PlatformPermissions{Items: []PermissionItem{{Key: calendarPermissionKey, Granted: true}}}
	}
	t.Cleanup(func() { todayBriefPermissionStatus = previous })
	for _, mode := range []string{"reads-off", "paused"} {
		t.Run(mode, func(t *testing.T) {
			a := newMCPReadTestApp(t)
			setCalendarFirstDayPermission(t, a, mode != "reads-off")
			if mode == "paused" {
				if err := a.SetClosedEyesModeEnabled(true); err != nil {
					t.Fatal(err)
				}
			}
			got, err := a.calendarFirstDayAt(calendarFirstDayNow())
			if err != nil {
				t.Fatal(err)
			}
			if !got.Connected || got.ReadsOff != (mode == "reads-off") || got.Paused != (mode == "paused") {
				t.Fatalf("permission before sync: %+v", got)
			}
		})
	}
}

func TestCalendarFirstDayPeopleCappedAtTwentySoonestFirst(t *testing.T) {
	a := newMCPReadTestApp(t)
	setCalendarFirstDayPermission(t, a, true)
	now := calendarFirstDayNow()
	var attendees []storage.CalendarAttendeeRow
	attendees = append(attendees, calendarFirstDayMe())
	for i := 0; i < 25; i++ {
		attendees = append(attendees, calendarFirstDayPerson(fmt.Sprintf("Person %02d", i), fmt.Sprintf("p%02d@example.test", i)))
	}
	attendees = append(attendees, storage.CalendarAttendeeRow{Status: calendar.StatusAccepted})
	// Person 00 sits in the earliest meeting, the rest in later ones.
	start := now.Add(2 * time.Hour)
	rows := []storage.CalendarEventRow{calendarFirstDayEvent("big", "Big one", start, start.Add(time.Hour), attendees...)}
	later := calendarFirstDayEvent("later", "Later", start.Add(48*time.Hour), start.Add(49*time.Hour), calendarFirstDayMe(), calendarFirstDayPerson("Zed", "zed@example.test"))
	rows = append(rows, later)
	if err := a.store.UpsertCalendarEvents(t.Context(), rows, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	got, err := a.calendarFirstDayAt(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.People) != calendarFirstDayPeopleLimit {
		t.Fatalf("people = %d, want %d", len(got.People), calendarFirstDayPeopleLimit)
	}
	if got.PeopleThisWeek != 26 {
		t.Fatalf("peopleThisWeek = %d, want 26 unique named people before the cap", got.PeopleThisWeek)
	}
	// The 25 people in the earlier meeting fill the cap; Zed (later) is cut.
	for _, person := range got.People {
		if person.Key == "zed@example.test" {
			t.Fatal("person beyond the cap admitted")
		}
		if person.NextMeetingAt != start.UnixMilli() {
			t.Fatalf("person %s next = %d, want %d", person.Key, person.NextMeetingAt, start.UnixMilli())
		}
	}
}

func TestCalendarFirstDayAttendeeHelpers(t *testing.T) {
	room := storage.CalendarAttendeeRow{Email: "room@resource.calendar.google.com"}
	if !calendarFirstDayResource(room) {
		t.Fatal("resource calendar address not treated as a resource")
	}
	if !calendarFirstDayResource(storage.CalendarAttendeeRow{Role: "NonParticipant", Email: "x@example.test"}) {
		t.Fatal("nonparticipant role not treated as a resource")
	}
	if got := calendarFirstDayKey(storage.CalendarAttendeeRow{Name: "  Priya Shah ", Email: " Priya@Example.Test "}); got != "priya@example.test" {
		t.Fatalf("key = %q", got)
	}
	if got := calendarFirstDayKey(storage.CalendarAttendeeRow{Name: "  Priya Shah "}); got != "priya shah" {
		t.Fatalf("name key = %q", got)
	}
}
