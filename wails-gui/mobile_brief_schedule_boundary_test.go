package main

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestMobileBriefScheduleBoundaryIsThePredictedRun(t *testing.T) {
	now := time.Date(2026, 11, 4, 8, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		view   MobileBriefSchedule
		want   time.Time
		wantOK bool
	}{
		{name: "no prediction shown", view: MobileBriefSchedule{Enabled: true}, wantOK: false},
		{name: "future prediction is the boundary", view: MobileBriefSchedule{NextPredictedAt: "2026-11-04T09:30:00Z"}, want: time.Date(2026, 11, 4, 9, 30, 0, 0, time.UTC), wantOK: true},
		{name: "predicted time already passed", view: MobileBriefSchedule{NextPredictedAt: "2026-11-04T07:59:00Z"}, wantOK: false},
		{name: "predicted time equal to now is not a future boundary", view: MobileBriefSchedule{NextPredictedAt: now.Format(time.RFC3339)}, wantOK: false},
		{name: "unparsable prediction arms nothing", view: MobileBriefSchedule{NextPredictedAt: "tomorrow morning"}, wantOK: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := mobileBriefScheduleBoundary(now, tc.view)
			if ok != tc.wantOK || (ok && !got.Equal(tc.want)) {
				t.Fatalf("boundary = %v, %v; want %v, %v", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestMobileBriefScheduleConfigChangedIgnoresAResave(t *testing.T) {
	on, off := true, false
	base := MobileBriefSchedule{Enabled: true, Morning: "08:00", Evening: "18:00", AdaptiveTiming: &on, NextPredictedAt: "2026-11-04T09:00:00Z"}
	if mobileBriefScheduleConfigChanged(base, base) {
		t.Fatal("re-saving the same schedule counted as a change")
	}
	moved := base
	moved.NextPredictedAt = "2026-11-04T10:00:00Z"
	if mobileBriefScheduleConfigChanged(base, moved) {
		t.Fatal("a moved prediction alone is not a settings change")
	}
	evening := base
	evening.Evening = "19:00"
	if !mobileBriefScheduleConfigChanged(base, evening) {
		t.Fatal("a new evening time was not a change")
	}
	adaptive := base
	adaptive.AdaptiveTiming = &off
	if !mobileBriefScheduleConfigChanged(base, adaptive) {
		t.Fatal("switching adaptive timing was not a change")
	}
}

// The schedule has one timer. A changed prediction replaces the armed time,
// so the earlier boundary never fires, and an empty prediction cancels it.
func TestMobileBriefScheduleArmReArmsOnChange(t *testing.T) {
	var m mobileBriefScheduleArm
	var fired atomic.Int32
	fire := func() { fired.Add(1) }
	now := time.Now()
	later := MobileBriefSchedule{NextPredictedSlot: "morning", NextPredictedAt: now.Add(300 * time.Millisecond).Format(time.RFC3339Nano)}
	sooner := MobileBriefSchedule{NextPredictedSlot: "morning", NextPredictedAt: now.Add(60 * time.Millisecond).Format(time.RFC3339Nano)}

	if !m.armFor(later, time.Now(), fire) {
		t.Fatal("first prediction did not count as a change")
	}
	if m.armFor(later, time.Now(), fire) {
		t.Fatal("the same prediction counted as a change again")
	}
	if !m.armFor(sooner, time.Now(), fire) {
		t.Fatal("a moved prediction did not count as a change")
	}
	time.Sleep(150 * time.Millisecond)
	if got := fired.Load(); got != 1 {
		t.Fatalf("boundary fired %d times after the re-arm, want 1", got)
	}
	time.Sleep(250 * time.Millisecond)
	if got := fired.Load(); got != 1 {
		t.Fatalf("the replaced boundary fired too: %d", got)
	}

	if !m.armFor(MobileBriefSchedule{}, time.Now(), fire) {
		t.Fatal("cancelling the prediction did not count as a change")
	}
	time.Sleep(20 * time.Millisecond)
	if got := fired.Load(); got != 1 {
		t.Fatalf("a cancelled prediction fired: %d", got)
	}
}
