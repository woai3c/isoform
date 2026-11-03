package storage

import (
	"fmt"
	"testing"
)

func TestNativeCalendarTimingSelectsOnlyEligibleMetadata(t *testing.T) {
	s := newTestStore(t)
	base := calendarEventFixture("eligible", 1000, 2000)
	rows := []CalendarEventRow{base}
	changes := []func(*CalendarEventRow){
		func(row *CalendarEventRow) { row.Source = "web" },
		func(row *CalendarEventRow) { row.SessionID = "private-session" },
		func(row *CalendarEventRow) { row.AllDay = true },
		func(row *CalendarEventRow) { row.Status = "canceled" },
		func(row *CalendarEventRow) { row.MyStatus = "declined" },
		func(row *CalendarEventRow) { row.Availability = "free" },
		func(row *CalendarEventRow) { row.EndAt = row.StartAt },
		func(row *CalendarEventRow) { row.StartAt, row.EndAt = 500, 900 },
		func(row *CalendarEventRow) { row.StartAt, row.EndAt = 3000, 4000 },
	}
	for index, change := range changes {
		row := base
		row.ID = fmt.Sprintf("excluded-%d", index)
		change(&row)
		rows = append(rows, row)
	}
	deleted := base
	deleted.ID = "deleted"
	rows = append(rows, deleted)
	if err := s.UpsertCalendarEvents(ctx, rows, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE calendar_events SET deleted_at=2 WHERE id='deleted'`); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListNativeCalendarTiming(ctx, 1000, 3000)
	if err != nil || len(got) != 1 || got[0].StartAt != 1000 || got[0].EndAt != 2000 || got[0].Provider != base.Provider {
		t.Fatalf("calendar timing = %+v, %v", got, err)
	}
}

func TestNativeCalendarTimingSelectsOverlappingIntervals(t *testing.T) {
	s := newTestStore(t)
	rows := []CalendarEventRow{
		calendarEventFixture("spanning-range", 500, 4000),
		calendarEventFixture("ending-in-range", 600, 2000),
		calendarEventFixture("ends-at-lower-bound", 700, 1000),
		calendarEventFixture("starts-at-upper-bound", 3000, 4000),
	}
	if err := s.UpsertCalendarEvents(ctx, rows, 1); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListNativeCalendarTiming(ctx, 1000, 3000)
	if err != nil || len(got) != 2 {
		t.Fatalf("overlapping calendar timing = %+v, %v", got, err)
	}
	for index, want := range rows[:2] {
		if got[index].StartAt != want.StartAt || got[index].EndAt != want.EndAt {
			t.Fatalf("overlap lost original interval: got %+v, want %d-%d", got[index], want.StartAt, want.EndAt)
		}
	}
}

func TestMobileBriefOpenTimesRequireExplicitBriefNotificationOpen(t *testing.T) {
	s := newTestStore(t)
	cases := []struct {
		kind, channel, state string
		at                   int64
	}{
		{"brief_ready", "desktop", EngagementReceiptOpened, 1000},
		{"brief_ready", "push", EngagementReceiptOpened, 2000},
		{"brief_ready", "desktop", EngagementReceiptOpened, 2000},
		{"brief_ready", "push", EngagementReceiptDelivered, 1200},
		{"brief_ready", "push", EngagementReceiptActed, 1300},
		{"brief_ready", "email", EngagementReceiptOpened, 1400},
		{"meeting_prep", "push", EngagementReceiptOpened, 1500},
		{"brief_ready", "push", EngagementReceiptOpened, 900},
		{"brief_ready", "push", EngagementReceiptOpened, 3000},
	}
	for index, sample := range cases {
		moment, err := s.CreateEngagementMoment(ctx, EngagementMomentRow{Kind: sample.kind, DedupeKey: fmt.Sprintf("timing-%d", index)})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.RecordEngagementReceipt(ctx, EngagementReceiptRow{MomentID: moment.ID, Channel: sample.channel, State: sample.state, At: sample.at}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListMobileBriefOpenTimes(ctx, 1000, 3000)
	if err != nil || len(got) != 2 || got[0] != 1000 || got[1] != 2000 {
		t.Fatalf("brief opens = %v, %v", got, err)
	}
}
