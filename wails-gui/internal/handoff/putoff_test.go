package handoff

import (
	"testing"
	"time"
)

func TestPutOffComesBackWithTheNextBriefOrTwoHoursBeforeDue(t *testing.T) {
	now := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
	ms := func(d time.Duration) int64 { return now.Add(d).UnixMilli() }
	for _, tc := range []struct {
		name             string
		brief, due, want int64
	}{
		{"next brief", ms(8 * time.Hour), 0, ms(8 * time.Hour)},
		{"due sooner than the brief", ms(8 * time.Hour), ms(3 * time.Hour), ms(time.Hour)},
		{"brief sooner than due", ms(2 * time.Hour), ms(10 * time.Hour), ms(2 * time.Hour)},
		{"no brief, due", 0, ms(5 * time.Hour), ms(3 * time.Hour)},
		{"neither", 0, 0, ms(24 * time.Hour)},
	} {
		if got := PutOffReturn(now, tc.brief, tc.due, 24*time.Hour); got != tc.want {
			t.Errorf("%s: until = %v, want %v", tc.name, time.UnixMilli(got).UTC(), time.UnixMilli(tc.want).UTC())
		}
	}
}

func TestPutOffComesBackWhenSomethingNewHappens(t *testing.T) {
	now := time.Now()
	e := Entry{ID: "t", Status: StatusActive, Thread: &Thread{}}
	e.Thread.PutOff = e.Thread.PutOff.Next(now, now.Add(time.Hour).UnixMilli(), EntryMark(e))
	if !PutOffAway(e, now) {
		t.Fatal("put-off task shows")
	}
	e.Status = StatusBlocked
	if PutOffAway(e, now) {
		t.Fatal("a task that changed stayed put off")
	}
	e.Status = StatusActive
	e.Thread.Events = append(e.Thread.Events, Event{ID: "new"})
	if PutOffAway(e, now) {
		t.Fatal("a task with a new message stayed put off")
	}
}

func TestPutOffAsksKeepOnTheSecondReturn(t *testing.T) {
	now := time.Now()
	var p *PutOff
	p = p.Next(now, now.Add(-time.Minute).UnixMilli(), "m")
	if p.AsksKeep(now.UnixMilli(), "m") {
		t.Fatal("first return asks Keep")
	}
	p = p.Next(now, now.Add(time.Hour).UnixMilli(), "m")
	if p.AsksKeep(now.UnixMilli(), "m") {
		t.Fatal("asks Keep while still put off")
	}
	if !p.AsksKeep(now.Add(2*time.Hour).UnixMilli(), "m") {
		t.Fatal("second return does not ask Keep")
	}
}
