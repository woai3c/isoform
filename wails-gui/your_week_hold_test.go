package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/calendar"
)

func TestYourWeekClockRange(t *testing.T) {
	day := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name               string
		startHour, endHour int
		en, zh             string
	}{
		{name: "same period", startHour: 9, endHour: 11, en: "9 to 11 AM", zh: "9点到11点"},
		{name: "ten to noon", startHour: 10, endHour: 12, en: "10 AM to 12 PM", zh: "10点到12点"},
		{name: "eleven to noon", startHour: 11, endHour: 12, en: "11 AM to 12 PM", zh: "11点到12点"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := day.Add(time.Duration(tc.startHour) * time.Hour)
			end := day.Add(time.Duration(tc.endHour) * time.Hour)
			if got := yourWeekClockRange(start, end, "en"); got != tc.en {
				t.Errorf("English range = %q, want %q", got, tc.en)
			}
			if got := yourWeekClockRange(start, end, "zh"); got != tc.zh {
				t.Errorf("Chinese range = %q, want %q", got, tc.zh)
			}
		})
	}
}

func TestYourWeekCalendarHoldFallsBackToFreeOneHour(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	ctx := context.Background()
	day := dayShapeTestDay(t).AddDate(0, 0, 3)
	now := day.Add(8 * time.Hour)
	if err := app.store.SetUIState(ctx, calendarLastSyncKey, now.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	storeEvent(t, app.store, calendar.Event{
		ID: "busy", Source: calendar.SourceEventKit, Title: "Busy",
		Start: day.Add(9 * time.Hour), End: day.Add(11 * time.Hour),
	})
	hold := app.yourWeekCalendarHold(ctx, day, now, "the launch plan")
	if hold == nil || hold.Start != day.Add(11*time.Hour).Format(time.RFC3339) || hold.End != day.Add(12*time.Hour).Format(time.RFC3339) {
		t.Fatalf("hold after 9–11 busy event: %+v", hold)
	}
	if hold.Label != "the launch plan" {
		t.Fatalf("hold label: %+v", hold)
	}
}

func TestYourWeekCalendarHoldUnavailableWhenMorningIsBusyOrCalendarUnreadable(t *testing.T) {
	for _, tc := range []struct {
		name       string
		unreadable bool
		busy       bool
	}{
		{name: "fully busy morning", busy: true},
		{name: "unreadable calendar", unreadable: true},
		{name: "calendar disconnected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := newTodayContextTestApp(t, true)
			ctx := context.Background()
			day := dayShapeTestDay(t).AddDate(0, 0, 3)
			now := day.Add(8 * time.Hour)
			if tc.name != "calendar disconnected" {
				if err := app.store.SetUIState(ctx, calendarLastSyncKey, now.Format(time.RFC3339)); err != nil {
					t.Fatal(err)
				}
			}
			if tc.unreadable {
				if err := app.store.SetUIState(ctx, permissionUIStateKey(calendarPermissionKey), "false"); err != nil {
					t.Fatal(err)
				}
			}
			if tc.busy {
				storeEvent(t, app.store, calendar.Event{
					ID: "busy-morning", Source: calendar.SourceEventKit, Title: "Busy",
					Start: day.Add(9 * time.Hour), End: day.Add(12 * time.Hour),
				})
			}
			if hold := app.yourWeekCalendarHold(ctx, day, now, "the launch plan"); hold != nil {
				t.Fatalf("unexpected hold: %+v", hold)
			}
		})
	}
}

func yourWeekCalendarHoldFixture(t *testing.T) (*App, time.Time, time.Time) {
	t.Helper()
	app := newTodayContextTestApp(t, true)
	ctx := context.Background()
	day := dayShapeTestDay(t).AddDate(0, 0, 3)
	now := day.Add(8 * time.Hour)
	if err := app.store.SetUIState(ctx, calendarLastSyncKey, now.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	return app, day, now
}

func TestYourWeekCalendarHoldWithheldMailEventFailsClosed(t *testing.T) {
	app, brief, _ := yourWeekFrozenWindowFixture(t,
		[]time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday},
		"2026-10-19", intPointer(5), 3,
		[]yourWeekSnapshotRef{{ID: "t-halcyon", Minutes: 6 * 60}})
	ctx := context.Background()
	if err := app.store.SetUIState(ctx, calendarLastSyncKey, time.Now().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	secretTitle := "Quarterly acquisition proposal"
	seedMobileBriefSubject(t, app, secretTitle)
	storeEvent(t, app.store, calendar.Event{
		ID: "held-mail", Source: calendar.SourceEventKit, Title: secretTitle,
		Start: day.Add(9 * time.Hour), End: day.Add(12 * time.Hour),
	})
	brief.Sections.Commitments = []MobileBriefItem{{
		ID: "launch", Key: "commitments:launch", Kind: "commitment", Title: "the launch plan",
	}}
	week, _ := app.yourWeekForDelivery(brief)
	var ask *MobileBriefWeekAsk
	if week != nil {
		ask = week.Ask
	}
	var label string
	if ask != nil && ask.Hold != nil {
		label = ask.Hold.Label
	}
	if ask == nil || strings.Contains(ask.Text+" "+ask.Subject+" "+ask.Message, secretTitle) ||
		strings.Contains(label, secretTitle) || ask.Hold != nil {
		t.Fatalf("withheld mail event affected a hold or leaked into copy: week=%+v ask=%+v", week, ask)
	}
}

func TestYourWeekDoesNotOfferCalendarHold(t *testing.T) {
	app, brief, now := yourWeekFrozenWindowFixture(t,
		[]time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday},
		"2026-10-19", intPointer(5), 3,
		[]yourWeekSnapshotRef{{ID: "t-halcyon", Minutes: 6 * 60}})
	if err := app.store.SetUIState(context.Background(), calendarLastSyncKey, now.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	brief.Sections.Commitments = []MobileBriefItem{{
		ID: "launch", Key: "commitments:launch", Kind: "commitment", Title: "the launch plan",
	}}
	day := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	if app.yourWeekCalendarHold(context.Background(), day, now, "the launch plan") == nil {
		t.Fatal("fixture must have a free, connected calendar")
	}
	week, _ := app.yourWeekForDelivery(brief)
	if week == nil || week.Ask == nil || week.Ask.Hold != nil || strings.Contains(week.Ask.Text, "hold") {
		t.Fatalf("unexpected hold offer: %+v", week)
	}
}

func TestYourWeekCalendarHoldProtectedSourceFailsClosed(t *testing.T) {
	app, day, now := yourWeekCalendarHoldFixture(t)
	if err := app.store.SetUIState(context.Background(), "sensitive_apps", `{"com.apple.iCal":"Calendar"}`); err != nil {
		t.Fatal(err)
	}
	storeEvent(t, app.store, calendar.Event{
		ID: "protected-calendar", Source: calendar.SourceEventKit, Title: "Private event",
		Start: day.Add(9 * time.Hour), End: day.Add(12 * time.Hour),
	})
	if hold := app.yourWeekCalendarHold(context.Background(), day, now, "the launch plan"); hold != nil {
		t.Fatalf("protected source offered a hold: %+v", hold)
	}
}

func TestYourWeekCalendarHoldAllDayAvailability(t *testing.T) {
	t.Run("busy all-day event blocks hold", func(t *testing.T) {
		app, day, now := yourWeekCalendarHoldFixture(t)
		storeEvent(t, app.store, calendar.Event{
			ID: "busy-all-day", Source: calendar.SourceEventKit, Title: "Busy all day",
			Start: day, End: day.AddDate(0, 0, 1), AllDay: true,
		})
		if hold := app.yourWeekCalendarHold(context.Background(), day, now, "the launch plan"); hold != nil {
			t.Fatalf("busy all-day event offered a hold: %+v", hold)
		}
	})
	t.Run("free all-day event does not block hold", func(t *testing.T) {
		app, day, now := yourWeekCalendarHoldFixture(t)
		storeEvent(t, app.store, calendar.Event{
			ID: "free-all-day", Source: calendar.SourceEventKit, Title: "Free all day",
			Start: day, End: day.AddDate(0, 0, 1), AllDay: true, Availability: "free",
		})
		hold := app.yourWeekCalendarHold(context.Background(), day, now, "the launch plan")
		if hold == nil || hold.Start != day.Add(9*time.Hour).Format(time.RFC3339) || hold.End != day.Add(11*time.Hour).Format(time.RFC3339) {
			t.Fatalf("free all-day event blocked the first window: %+v", hold)
		}
	})
}

func TestYourWeekCalendarHoldGenuinelyFreeDay(t *testing.T) {
	for _, withFreeEvent := range []bool{false, true} {
		t.Run(map[bool]string{false: "no events", true: "free timed event"}[withFreeEvent], func(t *testing.T) {
			app, day, now := yourWeekCalendarHoldFixture(t)
			if withFreeEvent {
				storeEvent(t, app.store, calendar.Event{
					ID: "free", Source: calendar.SourceEventKit, Title: "Free",
					Start: day.Add(9 * time.Hour), End: day.Add(10 * time.Hour), Availability: "free",
				})
			}
			hold := app.yourWeekCalendarHold(context.Background(), day, now, "the launch plan")
			if hold == nil || hold.Start != day.Add(9*time.Hour).Format(time.RFC3339) || hold.End != day.Add(11*time.Hour).Format(time.RFC3339) {
				t.Fatalf("free day did not offer the first window: %+v", hold)
			}
		})
	}
}

func TestYourWeekCalendarHoldUnparseableBusyEventFailsClosed(t *testing.T) {
	app, day, now := yourWeekCalendarHoldFixture(t)
	storeEvent(t, app.store, calendar.Event{
		ID: "invalid-times", Source: calendar.SourceEventKit, Title: "Invalid",
		Start: day.Add(11 * time.Hour), End: day.Add(8 * time.Hour),
	})
	if hold := app.yourWeekCalendarHold(context.Background(), day, now, "the launch plan"); hold != nil {
		t.Fatalf("unparseable busy event offered a hold: %+v", hold)
	}
}

func TestYourWeekNextFirstDayUsesConfiguredWorkdays(t *testing.T) {
	thursday := time.Date(2026, 10, 22, 0, 0, 0, 0, time.Local)
	workDays := []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday}
	next := yourWeekNextFirstDay(thursday, workDays)
	if got := next.Format("2006-01-02 Mon"); got != "2026-10-26 Mon" {
		t.Fatalf("next first workday: %s", got)
	}
	hold := &TodayDayShapeHold{
		Start: next.Add(9 * time.Hour).Format(time.RFC3339),
		End:   next.Add(11 * time.Hour).Format(time.RFC3339),
		Label: "the launch plan",
	}
	text, message := yourWeekHoldAsk("the launch plan", *hold, "en")
	if !strings.Contains(text, "Monday 9 to 11 AM") || !strings.Contains(message, "Monday, October 26") || !strings.Contains(message, "Don't invite anyone.") {
		t.Fatalf("hold copy: %q / %q", text, message)
	}
	zhText, zhMessage := yourWeekHoldAsk("the launch plan", *hold, "zh")
	if !strings.Contains(zhText, "周一9点到11点") || !strings.Contains(zhMessage, "10月26日") || !strings.Contains(zhMessage, "不要邀请任何人") {
		t.Fatalf("Chinese hold copy: %q / %q", zhText, zhMessage)
	}
}

func TestYourWeekAskWithoutHoldPlansWithoutWritingCalendar(t *testing.T) {
	week := yourWeekFixture("en")
	if week.Ask == nil || week.Ask.Text != "Start next week on the Q4 plan?" ||
		!strings.Contains(week.Ask.Message, "start Monday on the Q4 plan") ||
		!strings.Contains(week.Ask.Message, "Don't write to my calendar.") {
		t.Fatalf("no-hold copy: %+v", week.Ask)
	}
	week = yourWeekFixture("zh")
	if week.Ask == nil || week.Ask.Text != "下周从the Q4 plan开始？" ||
		!strings.Contains(week.Ask.Message, "规划周一如何从the Q4 plan开始") ||
		!strings.Contains(week.Ask.Message, "不要写入日历") {
		t.Fatalf("Chinese no-hold copy: %+v", week.Ask)
	}
}
