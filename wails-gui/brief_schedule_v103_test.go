package main

import (
	"context"
	"testing"
	"time"
)

// tasks-review v1.0.3: one next time for each brief slot, from the scheduler.

func TestBriefNextRunWaitsForTomorrowWhenABriefStoodIn(t *testing.T) {
	settings := mobileBriefSettings{Enabled: true, Morning: "07:00", Evening: "18:00"}
	now := mobileBriefTestDay(17, 45)
	standsIn := func(time.Time) bool { return true }
	at, catchUp := mobileBriefSlotNextRun(now, time.UTC, settings, mobileBriefSlotMorning, mobileBriefScheduleFacts{StandsIn: standsIn})
	if !at.Equal(mobileBriefTestDay(7, 0).AddDate(0, 0, 1)) || catchUp {
		t.Fatalf("morning next = %v catchUp=%v, want tomorrow 07:00", at, catchUp)
	}
	at, _ = mobileBriefSlotNextRun(now, time.UTC, settings, mobileBriefSlotEvening, mobileBriefScheduleFacts{StandsIn: standsIn})
	if !at.Equal(mobileBriefTestDay(18, 0)) {
		t.Fatalf("evening next = %v, want today 18:00", at)
	}
	// A catch-up the scheduler really makes keeps its true time: its retry.
	retryAt := func(retry time.Time) mobileBriefScheduleFacts {
		return mobileBriefScheduleFacts{RetryAt: func(id string) time.Time {
			if id == "2026-10-09-morning" {
				return retry
			}
			return time.Time{}
		}}
	}
	retry := mobileBriefTestDay(11, 50)
	at, catchUp = mobileBriefSlotNextRun(mobileBriefTestDay(11, 45), time.UTC, settings, mobileBriefSlotMorning, retryAt(retry))
	if !at.Equal(retry) || !catchUp {
		t.Fatalf("catch-up next = %v catchUp=%v, want the retry at 11:50", at, catchUp)
	}
	// From noon the morning's retries give way: tomorrow (today-review v1.1.21 R49).
	at, catchUp = mobileBriefSlotNextRun(now, time.UTC, settings, mobileBriefSlotMorning, retryAt(mobileBriefTestDay(17, 50)))
	if !at.Equal(mobileBriefTestDay(7, 0).AddDate(0, 0, 1)) || catchUp {
		t.Fatalf("after-noon next = %v catchUp=%v, want tomorrow 07:00", at, catchUp)
	}
	// A slot the scheduler won't make late waits for tomorrow too.
	at, _ = mobileBriefSlotNextRun(now, time.UTC, settings, mobileBriefSlotMorning, mobileBriefScheduleFacts{CatchUpAllowed: func(time.Time) bool { return false }})
	if !at.Equal(mobileBriefTestDay(7, 0).AddDate(0, 0, 1)) {
		t.Fatalf("not-allowed catch-up next = %v, want tomorrow", at)
	}
	settings.Enabled = false
	if at, _ := mobileBriefSlotNextRun(now, time.UTC, settings, mobileBriefSlotMorning, mobileBriefScheduleFacts{}); !at.IsZero() {
		t.Fatalf("a brief that is off has a next run: %v", at)
	}
}

// Bo's afternoon at ab2d86ee: the morning brief was made with Make it now at
// 09:16. Today, Tasks, the brief's detail and the phone agree.
func TestBriefNextTimeAgreesOnTodayTasksAndThePhone(t *testing.T) {
	clock := &fakeMobileBriefClock{now: mobileBriefTestDay(9, 16)}
	svc, _ := missedBriefRig(t, clock)
	if err := svc.config.save(mobileBriefSettings{Enabled: true, Morning: "07:00", Evening: "18:00"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.generate(context.Background(), mobileBriefSlotOnRequest, mobileBriefReasonSettings); err != nil {
		t.Fatal(err)
	}
	clock.Set(mobileBriefTestDay(17, 45))
	now := clock.Now()
	state := svc.upcomingBrief()
	items := assembleUpcoming(now, time.UTC, nil, nil, state)
	byID := map[string]UpcomingItem{}
	for _, item := range items {
		byID[item.ID] = item
	}
	tomorrow := mobileBriefTestDay(7, 0).AddDate(0, 0, 1).UnixMilli()
	if got := byID["brief:morning"].NextRunAt; got != tomorrow {
		t.Fatalf("Tasks morning next = %v, want tomorrow 07:00", time.UnixMilli(got).UTC())
	}
	if byID["brief:morning"].DidntRunAt != 0 {
		t.Fatalf("a morning made with Make it now warns: %+v", byID["brief:morning"])
	}
	if detail := briefScheduleDetailFrom(now, time.UTC, state, mobileBriefSlotMorning); detail.NextRunAt != tomorrow {
		t.Fatalf("detail next = %v, want tomorrow", time.UnixMilli(detail.NextRunAt).UTC())
	}
	evening := byID["brief:evening"].NextRunAt
	if evening != mobileBriefTestDay(18, 0).UnixMilli() {
		t.Fatalf("Tasks evening next = %v", time.UnixMilli(evening).UTC())
	}
	health := svc.Health()
	if health.NextSlot != mobileBriefSlotEvening || health.NextRunAt != formatMobileBriefTime(time.UnixMilli(evening).UTC()) {
		t.Fatalf("Today next = %s %s, want the evening Tasks shows", health.NextSlot, health.NextRunAt)
	}
	phone := map[string]string{}
	for _, row := range mobileScheduledTasks(items, nil, now) {
		phone[row.ID] = row.At
	}
	if phone["brief.morning"] != mobileTaskTime(tomorrow) || phone["brief.evening"] != mobileTaskTime(evening) {
		t.Fatalf("phone scheduled = %v, want Tasks' times", phone)
	}
}

// Today's next time skips a slot already made early (the build lead).
func TestBriefHealthNextRunSkipsASlotAlreadyMade(t *testing.T) {
	settings := mobileBriefSettings{Enabled: true, Morning: "07:00", Evening: "18:00"}
	facts := mobileBriefScheduleFacts{Exists: func(id string) bool { return id == "2026-10-09-morning" }}
	next, ok := mobileBriefScheduledNextRun(mobileBriefTestDay(6, 55), time.UTC, settings, facts)
	if !ok || next.Slot != mobileBriefSlotEvening || !next.ScheduledAt.Equal(mobileBriefTestDay(18, 0)) {
		t.Fatalf("next = %+v, want today's evening", next)
	}
}

func TestBriefScheduleDetailListsTodaysRunFirstWithWhatTheRecordSays(t *testing.T) {
	settings := mobileBriefSettings{Enabled: true, Morning: "07:00", Evening: "18:00"}
	now := mobileBriefTestDay(17, 45)
	at := func(hour, minute, day int) string {
		return formatMobileBriefTime(mobileBriefTestDay(hour, minute).AddDate(0, 0, day))
	}
	yesterday := mobileBriefRun{BriefID: "2026-10-08-morning", Slot: "morning", ScheduledAt: at(7, 0, -1), Status: mobileBriefRunOK, At: at(7, 1, -1)}

	// It came late, with a reason, then without one.
	for _, reason := range []string{mobileBriefMissedAsleep, ""} {
		state := upcomingBriefState{Available: true, Settings: settings, Runs: []mobileBriefRun{yesterday,
			{BriefID: "2026-10-09-morning", Slot: "morning", ScheduledAt: at(7, 0, 0), Status: mobileBriefRunOK, At: at(9, 16, 0), Reason: reason}}}
		runs := briefScheduleDetailFrom(now, time.UTC, state, "morning").Runs
		want := BriefScheduleRun{BriefID: "2026-10-09-morning", Status: mobileBriefRunOK, ScheduledAt: at(7, 0, 0), At: at(9, 16, 0), Reason: reason, Late: true}
		if len(runs) != 2 || runs[0] != want || runs[1].Late || runs[1].Reason != "" {
			t.Fatalf("reason %q: runs = %+v, want today's late run first", reason, runs)
		}
	}

	// Make it now made it at 09:16: its own row, then the scheduled run.
	state := upcomingBriefState{Available: true, Settings: settings, Runs: []mobileBriefRun{yesterday},
		MadeOnRequest: func(from, to time.Time) (string, time.Time, bool) {
			made := mobileBriefTestDay(9, 16)
			if made.Before(from) || !made.Before(to) {
				return "", time.Time{}, false
			}
			return "2026-10-09-on_request-091600", made, true
		},
		MissedReason: func(time.Time) string { return mobileBriefMissedClosed }}
	runs := briefScheduleDetailFrom(now, time.UTC, state, "morning").Runs
	if len(runs) != 3 {
		t.Fatalf("runs = %+v, want Make it now, the scheduled run and yesterday", runs)
	}
	if runs[0] != (BriefScheduleRun{BriefID: "2026-10-09-on_request-091600", Status: mobileBriefRunOK, ScheduledAt: at(7, 0, 0), At: at(9, 16, 0), MadeNow: true}) {
		t.Fatalf("first row = %+v, want the brief made with Make it now", runs[0])
	}
	if runs[1] != (BriefScheduleRun{BriefID: "2026-10-09-morning", Status: mobileBriefRunMissed, ScheduledAt: at(7, 0, 0), Reason: mobileBriefMissedClosed}) {
		t.Fatalf("second row = %+v, want the scheduled run that didn't run", runs[1])
	}
	if runs[2].BriefID != yesterday.BriefID {
		t.Fatalf("third row = %+v, want yesterday", runs[2])
	}
	// Once its window closed, the missed record and the same brief agree.
	state.Runs = append(state.Runs, mobileBriefRun{BriefID: "2026-10-09-morning", Slot: "morning", ScheduledAt: at(7, 0, 0), Status: mobileBriefRunMissed, At: at(18, 0, 0)})
	runs = briefScheduleDetailFrom(mobileBriefTestDay(19, 0), time.UTC, state, "morning").Runs
	if len(runs) != 3 || !runs[0].MadeNow || runs[1].Status != mobileBriefRunMissed || runs[1].Reason != "" {
		t.Fatalf("after the window: runs = %+v", runs)
	}
}

func TestBriefRowSaysDidntRunOnlyWhenNoBriefWasMadeAfterItsTime(t *testing.T) {
	settings := mobileBriefSettings{Enabled: true, Morning: "07:00", Evening: "18:00"}
	now := mobileBriefTestDay(19, 0)
	missed := mobileBriefRun{BriefID: "2026-10-09-morning", Slot: "morning", ScheduledAt: "2026-10-09T07:00:00Z", Status: mobileBriefRunMissed, At: "2026-10-09T18:00:00Z"}
	late := mobileBriefRun{BriefID: "2026-10-09-morning", Slot: "morning", ScheduledAt: "2026-10-09T07:00:00Z", Status: mobileBriefRunOK, At: "2026-10-09T09:16:00Z", Reason: mobileBriefMissedAsleep}
	none := func(time.Time, time.Time) (string, time.Time, bool) { return "", time.Time{}, false }
	madeNow := func(from, to time.Time) (string, time.Time, bool) {
		return "2026-10-09-on_request-091600", mobileBriefTestDay(9, 16), true
	}
	cases := []struct {
		name string
		run  mobileBriefRun
		made func(time.Time, time.Time) (string, time.Time, bool)
		want int64
	}{
		{"missed with nothing made", missed, none, mobileBriefTestDay(7, 0).UnixMilli()},
		{"missed but Make it now made it", missed, madeNow, 0},
		{"came late", late, none, 0},
	}
	for _, tc := range cases {
		state := upcomingBriefState{Available: true, Settings: settings, Runs: []mobileBriefRun{tc.run}, MadeOnRequest: tc.made}
		if got := upcomingBriefItem(now, time.UTC, state, "morning").DidntRunAt; got != tc.want {
			t.Fatalf("%s: didntRunAt = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// A brief made late keeps why in its run record, only when Isoform saw it.
func TestBriefLateRunRecordsTheObservedReason(t *testing.T) {
	clock := &fakeMobileBriefClock{now: mobileBriefTestDay(10, 0)}
	svc, _ := missedBriefRig(t, clock)
	svc.startedAt = mobileBriefTestDay(9, 55)
	svc.checkDue(context.Background())
	runs := svc.runs.list()
	if len(runs) != 1 || runs[0].Status != mobileBriefRunOK || runs[0].Reason != mobileBriefMissedClosed {
		t.Fatalf("runs = %+v, want the late morning with Isoform closed", runs)
	}
	detail := briefScheduleDetailFrom(clock.Now(), time.UTC, svc.upcomingBrief(), "morning")
	if len(detail.Runs) != 1 || !detail.Runs[0].Late || detail.Runs[0].Reason != mobileBriefMissedClosed {
		t.Fatalf("detail runs = %+v", detail.Runs)
	}
}
