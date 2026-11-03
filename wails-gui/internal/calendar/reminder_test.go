package calendar

import (
	"testing"
	"time"
)

func TestNormalizeReminder(t *testing.T) {
	loc := time.FixedZone("PDT", -7*3600)
	cases := []struct {
		name      string
		raw       RawReminder
		ok        bool
		wantDue   string
		allDay    bool
		priority  int
		completed bool
	}{
		{name: "no identity", raw: RawReminder{Title: "Call Ann"}},
		{name: "no title", raw: RawReminder{UID: "u1", Title: "  "}},
		{name: "local id fallback", raw: RawReminder{LocalID: "l1", Title: "Call Ann"}, ok: true},
		{
			name:    "due time",
			raw:     RawReminder{UID: "u1", Title: " Send  the deck ", Due: "2026-10-15T16:00:00Z", Priority: 1},
			ok:      true,
			wantDue: "2026-10-15T16:00:00Z", priority: 1,
		},
		{
			name:    "due day only",
			raw:     RawReminder{UID: "u2", Title: "Renew passport", DueDay: "2026-10-20"},
			ok:      true,
			wantDue: "2026-10-20", allDay: true,
		},
		{
			name: "out of range priority",
			raw:  RawReminder{UID: "u3", Title: "x", Priority: 42},
			ok:   true,
		},
		{
			name:      "completed",
			raw:       RawReminder{UID: "u4", Title: "Done thing", Completed: true, CompletedAt: "2026-10-14T10:00:00Z"},
			ok:        true,
			completed: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := NormalizeReminder(tc.raw, loc)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !ok {
				return
			}
			if got.DueValue(loc) != tc.wantDue || got.DueAllDay != tc.allDay || got.Priority != tc.priority || got.Completed != tc.completed {
				t.Fatalf("got %+v due %q", got, got.DueValue(loc))
			}
			if tc.completed && got.CompletedAt.IsZero() {
				t.Fatal("completed reminder lost its completion time")
			}
		})
	}
	r, _ := NormalizeReminder(RawReminder{UID: "u1", Title: " Send  the deck "}, loc)
	if r.Title != "Send the deck" {
		t.Fatalf("title %q", r.Title)
	}
}

func TestReminderOverdueAndDueBy(t *testing.T) {
	loc := time.FixedZone("PDT", -7*3600)
	day, _ := NormalizeReminder(RawReminder{UID: "d", Title: "Day", DueDay: "2026-10-14"}, loc)
	timed, _ := NormalizeReminder(RawReminder{UID: "t", Title: "Timed", Due: "2026-10-14T16:00:00Z"}, loc)
	none, _ := NormalizeReminder(RawReminder{UID: "n", Title: "None"}, loc)
	done, _ := NormalizeReminder(RawReminder{UID: "c", Title: "Done", Due: "2026-10-01T16:00:00Z", Completed: true}, loc)

	noon := time.Date(2026, 10, 14, 12, 0, 0, 0, loc) // 19:00 UTC
	if day.Overdue(noon, loc) {
		t.Fatal("a due day is not overdue before the day ends")
	}
	if !day.Overdue(time.Date(2026, 10, 15, 0, 0, 1, 0, loc), loc) {
		t.Fatal("a due day is overdue once the day ends")
	}
	if !timed.Overdue(noon, loc) {
		t.Fatal("a due time is overdue once it passes")
	}
	if none.Overdue(noon, loc) || done.Overdue(noon, loc) {
		t.Fatal("no due date or completed is never overdue")
	}
	if got := day.DueBy(loc); !got.Equal(time.Date(2026, 10, 15, 0, 0, 0, 0, loc)) {
		t.Fatalf("due by %v", got)
	}
	if day.Fingerprint(loc) == timed.Fingerprint(loc) {
		t.Fatal("different reminders share a fingerprint")
	}
	moved := day
	moved.Due = moved.Due.AddDate(0, 0, 1)
	if moved.Fingerprint(loc) == day.Fingerprint(loc) {
		t.Fatal("a moved due day must change the fingerprint")
	}
}

func TestPriorityBand(t *testing.T) {
	for priority, want := range map[int]string{0: "none", 1: "high", 4: "high", 5: "medium", 6: "low", 9: "low", 10: "none"} {
		if got := PriorityBand(priority); got != want {
			t.Fatalf("PriorityBand(%d) = %q, want %q", priority, got, want)
		}
	}
}
