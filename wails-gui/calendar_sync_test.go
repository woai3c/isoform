package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"wails-gui/internal/calendar"
	"wails-gui/storage"
)

// fakeCalendarSource stands in for EventKit.
type fakeCalendarSource struct {
	cals  []calendar.RawCalendar
	list  []calendar.RawEvent
	err   error
	calls int
}

func (f *fakeCalendarSource) calendars() ([]calendar.RawCalendar, error) { return f.cals, f.err }
func (f *fakeCalendarSource) events(start, end time.Time) ([]calendar.RawEvent, error) {
	f.calls++
	return f.list, f.err
}

func newCalendarTestService(t *testing.T, src *fakeCalendarSource, now time.Time) (*calendarSyncService, *storage.Store) {
	t.Helper()
	st, err := storage.New(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	svc := &calendarSyncService{
		store:       st,
		source:      src,
		now:         func() time.Time { return now },
		granted:     func() bool { return true },
		enabled:     func() bool { return true },
		changes:     make(chan struct{}, 1),
		kicks:       make(chan string, 4),
		consentPath: filepath.Join(t.TempDir(), "calendar-consent.json"),
	}
	return svc, st
}

func rawEventAt(uid, title string, start time.Time, minutes int, attendees ...calendar.RawAttendee) calendar.RawEvent {
	return calendar.RawEvent{
		UID: uid, LocalID: "local-" + uid, OccurrenceDate: start.Format(time.RFC3339),
		CalendarID: "cal-work", CalendarTitle: "Work", SourceType: "caldav", SourceTitle: "Google",
		Title: title, Start: start.Format(time.RFC3339), End: start.Add(time.Duration(minutes) * time.Minute).Format(time.RFC3339),
		Availability: "busy", Status: "confirmed", URL: "https://meet.google.com/abc-defg-hij",
		Attendees: attendees,
	}
}

func TestCalendarSyncUpsertsVanishesAndResolvesAttendees(t *testing.T) {
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	ctx := context.Background()
	src := &fakeCalendarSource{
		cals: []calendar.RawCalendar{
			{ID: "cal-work", Title: "Work", SourceIdentifier: "src-google", SourceTitle: "Google", SourceType: "caldav"},
			{ID: "cal-home", Title: "Home", SourceIdentifier: "src-icloud", SourceTitle: "iCloud", SourceType: "caldav"},
		},
	}
	svc, st := newCalendarTestService(t, src, now)

	ann, err := st.CreatePrincipal(ctx, storage.PrincipalRow{Kind: storage.PrincipalKindContact, DisplayName: "Ann"})
	if err != nil {
		t.Fatalf("create principal: %v", err)
	}
	if _, err := st.BindAppHandle(ctx, ann.ID, "email", "ann@example.com", "ann@example.com"); err != nil {
		t.Fatalf("bind handle: %v", err)
	}

	src.list = []calendar.RawEvent{
		rawEventAt("standup", "Standup", now.Add(time.Hour), 30,
			calendar.RawAttendee{Name: "Ann", Email: "mailto:Ann@Example.com", Status: "accepted"},
			calendar.RawAttendee{Name: "Bob", Email: "bob@example.com", Status: "pending"},
			calendar.RawAttendee{Name: "Me", Email: "me@example.com", Status: "accepted", IsMe: true}),
		rawEventAt("review", "Design review", now.Add(2*time.Hour), 60),
	}
	var synced []calendarSyncResult
	svc.onSynced = func(res calendarSyncResult) { synced = append(synced, res) }

	res, err := svc.Sync(ctx, "test")
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if res.Upserted != 2 || res.Sources != 2 || res.Vanished != 0 || len(synced) != 1 {
		t.Fatalf("result %+v, synced %d", res, len(synced))
	}
	events, err := st.ListCalendarEvents(ctx, 0, now.Add(48*time.Hour).UnixMilli(), false)
	if err != nil || len(events) != 2 {
		t.Fatalf("events %+v %v", events, err)
	}
	standup := events[0]
	if standup.Title != "Standup" || standup.ConferenceHost != "Google Meet" || standup.Provider != "google" || standup.AttendeeCount != 2 {
		t.Fatalf("standup %+v", standup)
	}
	var annRow, bobRow storage.CalendarAttendeeRow
	for _, att := range standup.Attendees {
		switch att.Email {
		case "ann@example.com":
			annRow = att
		case "bob@example.com":
			bobRow = att
		}
	}
	if annRow.PrincipalID != ann.ID {
		t.Fatalf("Ann should resolve to her principal: %+v", annRow)
	}
	if bobRow.PrincipalID != "" {
		t.Fatalf("an unknown address must not create a person: %+v", bobRow)
	}
	stats, err := st.GetPrincipalStats(ctx, ann.ID)
	if err != nil || stats.TotalMessages != 1 || stats.LastSeenAt != now.UnixMilli() {
		t.Fatalf("one sighting at now (the event is in the future): %+v %v", stats, err)
	}
	sources, err := st.ListCalendarSources(ctx)
	if err != nil || len(sources) != 2 || sources[0].LastReadAt != now.UnixMilli() {
		t.Fatalf("sources %+v %v", sources, err)
	}
	if _, err := os.Stat(svc.consentPath); err != nil {
		t.Fatalf("consent must be recorded before the first read: %v", err)
	}

	// Second sync: the review vanished, the standup is unchanged, and Ann's
	// sighting count does not grow.
	src.list = src.list[:1]
	src.cals = src.cals[:1]
	res, err = svc.Sync(ctx, "again")
	if err != nil || res.Vanished != 1 || res.Upserted != 1 {
		t.Fatalf("second sync %+v %v", res, err)
	}
	live, _ := st.ListCalendarEvents(ctx, 0, now.Add(48*time.Hour).UnixMilli(), false)
	if len(live) != 1 || live[0].ID != standup.ID {
		t.Fatalf("live after vanish %+v", live)
	}
	stats, _ = st.GetPrincipalStats(ctx, ann.ID)
	if stats.TotalMessages != 1 {
		t.Fatalf("a repeated sync must not add sightings: %d", stats.TotalMessages)
	}
	sources, _ = st.ListCalendarSources(ctx)
	if len(sources) != 1 || sources[0].ID != "eventkit:src-google" {
		t.Fatalf("a removed account disappears: %+v", sources)
	}
}

func TestCalendarSyncIfAllowedRespectsGrantAndSwitch(t *testing.T) {
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	src := &fakeCalendarSource{}
	svc, _ := newCalendarTestService(t, src, now)
	granted, enabled := false, true
	svc.granted = func() bool { return granted }
	svc.enabled = func() bool { return enabled }

	svc.syncIfAllowed(context.Background(), "startup")
	if src.calls != 0 {
		t.Fatal("no read without the grant")
	}
	granted = true
	enabled = false
	svc.syncIfAllowed(context.Background(), "interval")
	if src.calls != 0 {
		t.Fatal("no read while the switch is off")
	}
	enabled = true
	svc.syncIfAllowed(context.Background(), "interval")
	if src.calls != 1 {
		t.Fatalf("one read once granted and on, got %d", src.calls)
	}
	last, lastErr := svc.status()
	if !last.Equal(now) || lastErr != "" {
		t.Fatalf("status %v %q", last, lastErr)
	}
}

func TestCalendarSyncLoopDebouncesStoreChanges(t *testing.T) {
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	src := &fakeCalendarSource{}
	svc, _ := newCalendarTestService(t, src, now)
	changes := make(chan struct{}, 1)
	svc.changes = changes
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.wg.Add(1)
	go svc.loop(ctx)

	deadline := time.Now().Add(2 * time.Second)
	for src.calls < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if src.calls != 1 {
		t.Fatalf("startup sync expected, got %d", src.calls)
	}
	// A burst of change notifications becomes one sync after the debounce.
	for i := 0; i < 3; i++ {
		select {
		case changes <- struct{}{}:
		default:
		}
	}
	time.Sleep(calendarChangeDebounce / 2)
	if src.calls != 1 {
		t.Fatalf("no sync before the debounce elapses, got %d", src.calls)
	}
	deadline = time.Now().Add(calendarChangeDebounce + 2*time.Second)
	for src.calls < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if src.calls != 2 {
		t.Fatalf("one sync after the burst, got %d", src.calls)
	}
	svc.RequestSync("wake")
	deadline = time.Now().Add(2 * time.Second)
	for src.calls < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if src.calls != 3 {
		t.Fatalf("a requested sync runs, got %d", src.calls)
	}
	cancel()
	svc.wg.Wait()
}

func TestCalendarEventRowRoundTrip(t *testing.T) {
	start := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	ev, _ := calendar.NormalizeEventKit(rawEventAt("rt", "Round trip", start, 45,
		calendar.RawAttendee{Name: "Ann", Email: "ann@example.com", Role: "required", Status: "accepted"}))
	row := calendarEventRow(ev)
	for _, att := range ev.Attendees {
		row.Attendees = append(row.Attendees, storage.CalendarAttendeeRow{EventID: ev.ID, Email: att.Email, Name: att.Name, Role: att.Role, Status: att.Status, IsMe: att.IsMe})
	}
	back := calendarEventFromRow(row)
	if back.ID != ev.ID || !back.Start.Equal(ev.Start) || !back.End.Equal(ev.End) || back.ConferenceHost != ev.ConferenceHost || back.Title != ev.Title {
		t.Fatalf("round trip changed the event: %+v vs %+v", back, ev)
	}
	if len(back.Attendees) != 1 || back.Attendees[0].Email != "ann@example.com" || back.AttendeeCount() != 1 {
		t.Fatalf("attendees %+v", back.Attendees)
	}
}
