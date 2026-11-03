package calendar

import (
	"testing"
	"time"
)

func TestParseWebDate(t *testing.T) {
	loc := time.FixedZone("test", 8*3600)
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, loc)
	cases := []struct {
		text string
		want string
	}{
		{"October 14, 2026", "2026-10-14"},
		{"Oct 14 2026", "2026-10-14"},
		{"Wednesday, October 14th, 2026", "2026-10-14"},
		{"14 October 2026", "2026-10-14"},
		{"2026-10-14", "2026-10-14"},
		{"2026/10/14", "2026-10-14"},
		{"2026年10月14日", "2026-10-14"},
		{"10月 15日 (星期四)", "2026-10-15"},
		{"next week", ""},
		{"", ""},
		{"February 30, 2026", ""},
	}
	for _, tc := range cases {
		got, ok := ParseWebDate(tc.text, now, loc)
		if tc.want == "" {
			if ok {
				t.Errorf("%q: expected no date, got %v", tc.text, got)
			}
			continue
		}
		if !ok || got.Format("2006-01-02") != tc.want || got.Location() != loc || got.Hour() != 0 {
			t.Errorf("%q: got %v %v, want %s", tc.text, got, ok, tc.want)
		}
	}
}

func TestParseWebTimeRange(t *testing.T) {
	loc := time.UTC
	day := time.Date(2026, 10, 14, 0, 0, 0, 0, loc)
	cases := []struct {
		text       string
		start, end string
		allDay     bool
		ok         bool
	}{
		{"10am – 11am", "10:00", "11:00", false, true},
		{"10:30am to 11:15am", "10:30", "11:15", false, true},
		{"10:00 – 11:00", "10:00", "11:00", false, true},
		{"10 – 11 AM", "10:00", "11:00", false, true},
		{"11:30 – 1pm", "11:30", "13:00", false, true},
		{"from 2:00 PM to 3:30 PM", "14:00", "15:30", false, true},
		{"上午10:00 至 下午12:00", "10:00", "12:00", false, true},
		{"下午3:00", "15:00", "16:00", false, true},
		{"9am", "09:00", "10:00", false, true},
		{"12pm – 1pm", "12:00", "13:00", false, true},
		{"12am", "00:00", "01:00", false, true},
		{"23:00 – 00:30", "23:00", "00:30+1", false, true},
		{"All day", "00:00", "00:00+1", true, true},
		{"全天", "00:00", "00:00+1", true, true},
		{"", "", "", false, false},
		{"Standup", "", "", false, false},
	}
	for _, tc := range cases {
		start, end, allDay, ok := ParseWebTimeRange(tc.text, day, loc)
		if ok != tc.ok {
			t.Errorf("%q: ok = %v, want %v", tc.text, ok, tc.ok)
			continue
		}
		if !ok {
			continue
		}
		format := func(v time.Time) string {
			s := v.Format("15:04")
			if v.Day() != day.Day() {
				s += "+1"
			}
			return s
		}
		if format(start) != tc.start || format(end) != tc.end || allDay != tc.allDay {
			t.Errorf("%q: got %s to %s allDay=%v, want %s to %s allDay=%v", tc.text, format(start), format(end), allDay, tc.start, tc.end, tc.allDay)
		}
	}
}

func TestParseWebAllDayUsesLocalMidnightAcrossDST(t *testing.T) {
	for _, day := range calendarDSTTransitionDays(t) {
		t.Run(day.Format("2006-01-02"), func(t *testing.T) {
			start, end, allDay, ok := ParseWebTimeRange("all day", day, day.Location())
			want := day.AddDate(0, 0, 1)
			if !ok || !allDay || !start.Equal(day) || !end.Equal(want) {
				t.Fatalf("range = %s to %s, allDay=%v, ok=%v; want %s to %s", start, end, allDay, ok, day, want)
			}
		})
	}
}

func TestParseWebEvents(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, loc)
	events := ParseWebEvents([]WebEventItem{
		{Title: "Standup", Time: "10am – 10:15am", Date: "October 15, 2026", Location: "https://meet.google.com/abc-defg-hij"},
		{Title: "Lunch", Time: "12:00 – 13:00"}, // no date: dropped
		{Title: "Lunch", Time: "12:00 – 13:00", Date: "October 14, 2026"},
		{Title: "Standup", Time: "10am – 10:15am", Date: "October 15, 2026", Location: "https://meet.google.com/abc-defg-hij"},
		{Title: "No time", Time: "later"},
		{Title: "", Time: "10am"},
	}, "google", now, loc)
	if len(events) != 2 {
		t.Fatalf("events %+v", events)
	}
	lunch, standup := events[0], events[1]
	if lunch.Title != "Lunch" || lunch.Start.Format("2006-01-02 15:04") != "2026-10-14 12:00" {
		t.Fatalf("lunch %+v", lunch)
	}
	if standup.Start.Format("2006-01-02 15:04") != "2026-10-15 10:00" || standup.End.Format("15:04") != "10:15" {
		t.Fatalf("standup %+v", standup)
	}
	if standup.Source != SourceWeb || standup.Provider != "google" || standup.CalendarID != "web:google" || standup.ConferenceHost != "Google Meet" {
		t.Fatalf("standup fields %+v", standup)
	}
	again := ParseWebEvents([]WebEventItem{{Title: "standup", Time: "10:00 – 10:15", Date: "2026-10-15", Location: "https://meet.google.com/abc-defg-hij"}}, "google", now, loc)
	if len(again) != 1 || again[0].ID != standup.ID {
		t.Fatalf("the same event read again must keep its id: %+v vs %+v", again, standup)
	}
}
