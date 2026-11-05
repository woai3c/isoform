package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/calendar"
	"wails-gui/storage"
)

func dayShapeTestDay(t *testing.T) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	return time.Date(2026, 10, 23, 0, 0, 0, 0, loc)
}

func TestTodayDayShapeRangeWidening(t *testing.T) {
	day := dayShapeTestDay(t)
	event := func(id string, hour int, duration time.Duration) calendar.Event {
		return calendar.Event{ID: id, Title: id, Start: day.Add(time.Duration(hour) * time.Hour), End: day.Add(time.Duration(hour)*time.Hour + duration)}
	}
	for _, test := range []struct {
		name       string
		events     []calendar.Event
		start, end string
	}{
		{"empty", nil, "08:00", "19:00"},
		{"normal", []calendar.Event{event("review", 10, time.Hour)}, "08:00", "19:00"},
		{"early", []calendar.Event{event("early", 6, 30*time.Minute)}, "05:00", "19:00"},
		{"late", []calendar.Event{event("late", 20, time.Hour)}, "08:00", "22:00"},
		{"long overlapping event", []calendar.Event{event("long", 7, 14*time.Hour), event("short", 20, 30*time.Minute)}, "06:00", "22:00"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := shapeTodayCalendar(test.events, day, day.Add(9*time.Hour))
			if got.RangeStart != "2026-10-23T"+test.start+":00-07:00" || got.RangeEnd != "2026-10-23T"+test.end+":00-07:00" || got.Now != "2026-10-23T09:00:00-07:00" {
				t.Fatalf("shape=%+v", got)
			}
			if got.Events == nil || got.Hold != nil {
				t.Fatalf("empty contract=%+v", got)
			}
		})
	}
}

func TestTodayDayShapeKinds(t *testing.T) {
	for _, test := range []struct {
		name string
		ev   calendar.Event
		want string
	}{
		{"meeting", calendar.Event{Attendees: []calendar.Attendee{{Name: "Priya"}}}, "meeting"},
		{"no attendees", calendar.Event{}, "personal"},
		{"only me", calendar.Event{Attendees: []calendar.Attendee{{Name: "Me", IsMe: true}}}, "personal"},
		{"personal calendar with others", calendar.Event{CalendarTitle: "Personal", Attendees: []calendar.Attendee{{Name: "Teacher"}}}, "personal"},
		{"own conference block", calendar.Event{ConferenceURL: "https://meet.halcyon.example/room-1"}, "personal"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := todayEventKind(test.ev); got != test.want {
				t.Fatalf("kind=%s", got)
			}
		})
	}
}

func TestTodayDayShapeNamesComeFromTitleAndAttendees(t *testing.T) {
	meeting := func(title string) calendar.Event {
		return calendar.Event{Title: title, Attendees: []calendar.Attendee{{Name: "Alex"}, {Name: "Sam"}}}
	}
	for _, test := range []struct {
		name                     string
		ev                       calendar.Event
		short, shortest, initial string
	}{
		{"one-to-one attendee", calendar.Event{Title: "1:1 with Priya", Attendees: []calendar.Attendee{{Name: "Me", IsMe: true}, {Name: "Priya"}}}, "Priya", "Priya", "P"},
		{"one-to-one title", calendar.Event{Title: "1:1 with Xipeng"}, "Xipeng", "Xipeng", "X"},
		{"one-to-one title email", calendar.Event{Title: "1:1 with alex@example.com"}, "Alex", "Alex", "A"},
		{"one-to-one title dotted email", calendar.Event{Title: "1:1 with alex@example.com"}, "Alex", "Alex", "A"},
		{"one-to-one title URL", calendar.Event{Title: "1:1 with https://example.com/path"}, "", "", ""},
		{"one-to-one slash title", calendar.Event{Title: "1/1 with Xipeng"}, "Xipeng", "Xipeng", "X"},
		{"meeting title", meeting("Article - write the opening"), "Article", "Article", ""},
		{"meeting title with colon", meeting("Halcyon Spanish lesson: Maya C."), "Halcyon Spanish lesson", "Halcyon", ""},
		{"attendee email", calendar.Event{Title: "Design review", Attendees: []calendar.Attendee{{Email: "xipeng.li@example.com"}}}, "Xipeng", "Xipeng", "X"},
		{"email in attendee display name", calendar.Event{Title: "Design review", Attendees: []calendar.Attendee{{Name: "xipeng.li@example.com", Email: "xipeng.li@example.com"}}}, "Xipeng", "Xipeng", "X"},
		{"user and weekly words", calendar.Event{Title: "Alex, Isoform, NMG Weekly Check In", Attendees: []calendar.Attendee{{Name: "Alex", IsMe: true}, {Name: "Maya Chen"}, {Name: "Priya"}}}, "NMG Check In", "NMG", ""},
		{"isoform weekly check-in", meeting("Isoform, NMG Weekly Check In"), "NMG Check In", "NMG", ""},
		{"recurrence words", meeting("Isoform, Bi-weekly NMG Daily Monthly Meeting Check In"), "NMG Check In", "NMG", ""},
		{"email-only title", meeting("xipeng.li@example.com"), "", "", ""},
		{"URL-only meeting title", meeting("https://meet.halcyon.example/room-123"), "", "", ""},
		{"personal URL token", calendar.Event{Title: "Dinner at https://meet.halcyon.example/room-123", CalendarTitle: "Personal"}, "Dinner at", "Dinner", ""},
		{"personal title", calendar.Event{Title: " dinner  with   friends ", CalendarTitle: "Personal"}, "dinner with friends", "dinner", ""},
		{"Chinese title", meeting("项目评审"), "项目评审", "项目评审", ""},
		{"empty title", calendar.Event{}, "", "", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			short, shortest, initial := todayEventNames(test.ev)
			if short != test.short || shortest != test.shortest || initial != test.initial {
				t.Fatalf("%q names=%q,%q initial=%q", test.ev.Title, short, shortest, initial)
			}
		})
	}
}

func TestTodayDayShapeHoldValidation(t *testing.T) {
	day := dayShapeTestDay(t)
	asOf := day.Add(8 * time.Hour)
	valid := &TodayDayShapeHold{Start: day.Add(11 * time.Hour).Format(time.RFC3339), End: day.Add(12 * time.Hour).Format(time.RFC3339), Label: "Memo"}
	got, err := validatedTodayHold(valid, day, asOf)
	if err != nil || !reflect.DeepEqual(got, valid) {
		t.Fatalf("hold=%+v, %v", got, err)
	}
	if got, err := validatedTodayHold(nil, day, asOf); got != nil || err != nil {
		t.Fatalf("non-hold=%+v, %v", got, err)
	}
	for _, test := range []struct{ name, start, end, label string }{
		{"wrong day", day.AddDate(0, 0, 1).Format(time.RFC3339), day.AddDate(0, 0, 1).Add(time.Hour).Format(time.RFC3339), "Memo"},
		{"overnight", valid.Start, day.AddDate(0, 0, 1).Add(time.Hour).Format(time.RFC3339), "Memo"},
		{"past", day.Add(7 * time.Hour).Format(time.RFC3339), valid.End, "Memo"},
		{"reversed", valid.End, valid.Start, "Memo"},
		{"zero duration", valid.Start, valid.Start, "Memo"},
		{"no offset", "2026-10-23T11:00:00", valid.End, "Memo"},
		{"no label", valid.Start, valid.End, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			if hold, err := validatedTodayHold(&TodayDayShapeHold{Start: test.start, End: test.end, Label: test.label}, day, asOf); err == nil || hold != nil {
				t.Fatalf("accepted=%+v", hold)
			}
		})
	}
}

func TestTodayDayShapeCalendarNotConnectedAndEmptyConnected(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	day := dayShapeTestDay(t)
	got, err := app.todayDayShapeAt(day, day, MobileBriefV1{})
	if err != nil || got.CalendarConnected || len(got.Events) != 0 {
		t.Fatalf("disconnected=%+v %v", got, err)
	}
	if err := app.store.SetUIState(context.Background(), calendarLastSyncKey, day.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	got, err = app.todayDayShapeAt(day, day, MobileBriefV1{})
	if err != nil || !got.CalendarConnected || len(got.Events) != 0 {
		t.Fatalf("connected empty=%+v %v", got, err)
	}
}

func TestTodayDayShapePrivacyGate(t *testing.T) {
	for _, gate := range []string{"closed eyes", "calendar off", "screen off", "hidden calendar", "mail echo", "web without provenance"} {
		t.Run(gate, func(t *testing.T) {
			app := newTodayContextTestApp(t, true)
			day := dayShapeTestDay(t)
			ev := calendar.Event{ID: "private", Source: calendar.SourceEventKit, Title: "Private event", Start: day.Add(6 * time.Hour), End: day.Add(7 * time.Hour)}
			switch gate {
			case "closed eyes":
				app.closedEyes.enabled.Store(true)
			case "calendar off":
				_ = app.store.SetUIState(context.Background(), permissionUIStateKey(calendarPermissionKey), "false")
			case "screen off":
				_ = app.store.SetUIState(context.Background(), permissionUIStateKey("screen_recording"), "false")
			case "hidden calendar":
				_ = app.store.SetUIState(context.Background(), "sensitive_apps", `{"com.apple.iCal":"Calendar"}`)
			case "mail echo":
				seedMobileBriefSubject(t, app, ev.Title)
			case "web without provenance":
				ev.Source = calendar.SourceWeb
			}
			storeEvent(t, app.store, ev)
			got, err := app.todayDayShapeAt(day, day, MobileBriefV1{})
			if err != nil || len(got.Events) != 0 || got.Hold != nil || !strings.Contains(got.RangeStart, "T08:00:00") {
				t.Fatalf("gate=%+v %v", got, err)
			}
		})
	}
}

func TestTodayDayShapeBindingAndPhoneContract(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	now := time.Now()
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	ev := calendar.Event{ID: "meeting", Source: calendar.SourceEventKit, Title: "1:1 with Priya", Start: day.Add(14 * time.Hour), End: day.Add(15 * time.Hour), Attendees: []calendar.Attendee{{Name: "Priya"}}}
	storeEvent(t, app.store, ev)
	settleDayShape(t, app, day.Format("2006-01-02"))
	desktop, err := app.GetTodayDayShape(day.Format("2006-01-02"))
	if err != nil || len(desktop.Events) != 1 || !desktop.CalendarConnected {
		t.Fatalf("binding=%+v %v", desktop, err)
	}
	if desktop.Events[0].ShortName != "Priya" || desktop.Events[0].ShortestName != "Priya" || desktop.Events[0].Initial != "P" {
		t.Fatalf("day-strip name=%+v", desktop.Events[0])
	}
	if _, err := app.GetTodayDayShape("2026-10-23T00:00:00Z"); err == nil {
		t.Fatal("accepted timestamp as a date")
	}
	brief := buildMobileBriefV1(mobileBriefInput{Slot: mobileBriefSlotMorning, AsOf: now, Location: time.Local})
	phone := app.mobileBriefWithDayShape(brief)
	if phone.DayShape == nil || phone.DayShape.TodayDayShape == nil {
		t.Fatal("phone strip missing")
	}
	desktop.Now = phone.DayShape.Now
	if !reflect.DeepEqual(desktop, *phone.DayShape.TodayDayShape) {
		t.Fatalf("desktop=%+v phone=%+v", desktop, phone.DayShape)
	}
	svc, _ := newTestMobileBriefService(t, &fakeMobileBriefClock{now: now})
	svc.app, app.mobileBriefSvc = app, svc
	if err := svc.store.save(brief, now); err != nil {
		t.Fatal(err)
	}
	bridge := newMobileBriefPrivacyBridge(t, app)
	response := httptest.NewRecorder()
	bridge.serveLatestBrief(response)
	var wire struct {
		Data struct {
			DayShape TodayDayShape `json:"dayShape"`
		} `json:"data"`
	}
	// The brief package never carries the live calendar (PROTOCOL.md section
	// 8.1); phones read the day from the Today snapshot, built from the same
	// binding the desktop strip uses.
	if err := json.Unmarshal(response.Body.Bytes(), &wire); err != nil || response.Code != http.StatusOK || len(wire.Data.DayShape.Events) != 0 {
		t.Fatalf("route=%s %v", response.Body.String(), err)
	}
}

func TestTodayDayShapeQuestionHoldSurvivesStorageAndPrivacy(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	day := dayShapeTestDay(t)
	event := calendar.Event{ID: "meeting", Source: calendar.SourceEventKit, Title: "Review", Start: day.Add(14 * time.Hour), End: day.Add(15 * time.Hour)}
	storeEvent(t, app.store, event)
	in := mobileBriefInput{Slot: mobileBriefSlotMorning, AsOf: day.Add(8 * time.Hour), Location: day.Location()}
	collectMobileBriefCalendar(app, &in)
	brief := buildMobileBriefV1(in)
	refs := []string{brief.Evidence[0].Ref}
	hold := &TodayDayShapeHold{Start: day.Add(13 * time.Hour).Format(time.RFC3339), End: day.Add(14 * time.Hour).Format(time.RFC3339), Label: "Review prep"}
	brief.Narrative = []MobileBriefPara{{Text: "Want me to hold time to prep for Review?", Refs: refs, Hold: hold}}
	svc, _ := newTestMobileBriefService(t, &fakeMobileBriefClock{now: in.AsOf})
	if err := svc.store.save(brief, in.AsOf); err != nil {
		t.Fatal(err)
	}
	loaded, ok := svc.store.load(brief.BriefID)
	if !ok || !reflect.DeepEqual(mobileBriefClosingParagraph(loaded).Hold, hold) {
		t.Fatal("question hold lost in storage")
	}
	shape, err := app.todayDayShapeAt(day, in.AsOf, loaded)
	if err != nil || shape.Hold != nil {
		t.Fatalf("preview=%+v %v", shape, err)
	}
	projected := app.mobileBriefForDelivery(loaded)
	if mobileBriefClosingParagraph(projected).Hold != nil || len(projected.Narrative) != 0 {
		t.Fatal("delivered brief retained the hold offer")
	}
	// A withdrawn question cannot leave an independent preview behind.
	app.closedEyes.enabled.Store(true)
	shape, err = app.todayDayShapeAt(day, in.AsOf, loaded)
	if err != nil || shape.Hold != nil {
		t.Fatalf("withdrawn preview=%+v %v", shape, err)
	}
	rows, err := app.store.ListCalendarEvents(context.Background(), day.UnixMilli(), day.AddDate(0, 0, 1).UnixMilli(), false)
	if err != nil || len(rows) != 1 {
		t.Fatalf("preview wrote calendar=%+v %v", rows, err)
	}
}

// A source detection without a successful read is not a connection.
func TestTodayDayShapeDetectedCalendarIsNotConnected(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	day := dayShapeTestDay(t)
	err := app.store.UpsertCalendarSource(context.Background(), storage.CalendarSourceRow{ID: "google", Kind: "web", Provider: "google", Title: "calendar.google.com", DetectedBy: "history", LastSeenAt: day.UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	shape, err := app.todayDayShapeAt(day, day, MobileBriefV1{})
	if err != nil || shape.CalendarConnected {
		t.Fatalf("detected only=%+v %v", shape, err)
	}
}

// The strip endpoint must not expose a stored preview-only hold.
func TestTodayDayShapeEndpointReadsStoredHold(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	day := dayShapeTestDay(t)
	clock := &fakeMobileBriefClock{now: day.Add(8 * time.Hour)}
	svc, _ := newTestMobileBriefService(t, clock)
	svc.app, app.mobileBriefSvc = app, svc
	storeEvent(t, app.store, calendar.Event{ID: "meeting", Source: calendar.SourceEventKit, Title: "Review", Start: day.Add(14 * time.Hour), End: day.Add(15 * time.Hour)})
	in := mobileBriefInput{Slot: mobileBriefSlotMorning, AsOf: clock.Now(), Location: day.Location()}
	collectMobileBriefCalendar(app, &in)
	brief := buildMobileBriefV1(in)
	brief.Narrative = []MobileBriefPara{{Text: "Want me to hold time for prep?", Refs: []string{brief.Evidence[0].Ref}, Hold: &TodayDayShapeHold{Start: day.Add(13 * time.Hour).Format(time.RFC3339), End: day.Add(14 * time.Hour).Format(time.RFC3339), Label: "Review prep"}}}
	if err := svc.store.save(brief, clock.Now()); err != nil {
		t.Fatal(err)
	}
	shape, err := app.GetTodayDayShape(day.Format("2006-01-02"))
	if err != nil || shape.Hold != nil {
		t.Fatalf("endpoint hold=%+v err=%v", shape.Hold, err)
	}
}

// GetTodayDayShape must answer from the persisted brief without waiting on
// the today refresh (LatestMobileBrief), which holds todayMu while gathering
// sources. With todayMu held, the old delivered/live path would block.
func TestTodayDayShapeEndpointSkipsTodayRefresh(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	day := dayShapeTestDay(t)
	svc, _ := newTestMobileBriefService(t, &fakeMobileBriefClock{now: day.Add(8 * time.Hour)})
	svc.app, app.mobileBriefSvc = app, svc
	svc.todayMu.Lock()
	defer svc.todayMu.Unlock()
	done := make(chan error, 1)
	go func() {
		_, err := app.GetTodayDayShape(day.Format("2006-01-02"))
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("day shape read waited for the today refresh")
	}
}
