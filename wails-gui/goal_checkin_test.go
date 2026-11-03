package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// Guards for item 66 A3: one quiet check-in on Today for a goal more than 30
// days overdue with nothing on it for 30 days, asked about once.

func goalCheckInFixture(id string, now time.Time, edit func(*PurposeOpenLoopDetail)) PurposeOpenLoopDetail {
	old := now.AddDate(0, 0, -40).UnixMilli()
	detail := PurposeOpenLoopDetail{
		OpenLoop: PurposeOpenLoopRecord{ID: id},
		Current: PurposeOpenLoopEventRecord{ID: id + "-e1", Title: "Goal " + id, LifecycleState: PurposeStateActive,
			SupportState: PurposeSupportSupported, DueKind: PurposeDueDate, DueValue: now.AddDate(0, 0, -45).Format("2006-01-02"), OccurredAt: old},
		Evidence: []PurposeEvidenceRecord{{SourceKind: userPurposeSourceKind, SourceObservedAt: old}},
	}
	if edit != nil {
		edit(&detail)
	}
	return detail
}

func TestGoalCheckInAsksAboutOnlyStaleOverdueGoals(t *testing.T) {
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	quiet := func(PurposeOpenLoopDetail) (time.Time, bool) { return time.Time{}, true }
	cases := []struct {
		name    string
		edit    func(*PurposeOpenLoopDetail)
		touched func(PurposeOpenLoopDetail) (time.Time, bool)
		want    bool
	}{
		{"45 days overdue, quiet for 40", nil, quiet, true},
		{"waiting counts too", func(d *PurposeOpenLoopDetail) { d.Current.LifecycleState = PurposeStateWaiting }, quiet, true},
		{"only 20 days overdue", func(d *PurposeOpenLoopDetail) { d.Current.DueValue = now.AddDate(0, 0, -20).Format("2006-01-02") }, quiet, false},
		{"no due date", func(d *PurposeOpenLoopDetail) { d.Current.DueKind, d.Current.DueValue = PurposeDueNone, "" }, quiet, false},
		{"moved 10 days ago", func(d *PurposeOpenLoopDetail) { d.Current.OccurredAt = now.AddDate(0, 0, -10).UnixMilli() }, quiet, false},
		{"seen in a new source", func(d *PurposeOpenLoopDetail) {
			d.Evidence = append(d.Evidence, PurposeEvidenceRecord{SourceKind: "memory", SourceObservedAt: now.AddDate(0, 0, -3).UnixMilli()})
		}, quiet, false},
		{"worked on last week", nil, func(PurposeOpenLoopDetail) (time.Time, bool) { return now.AddDate(0, 0, -7), true }, false},
		{"private or mail-echoing", nil, func(PurposeOpenLoopDetail) (time.Time, bool) { return time.Time{}, false }, false},
		{"done", func(d *PurposeOpenLoopDetail) { d.Current.LifecycleState = PurposeStateResolved }, quiet, false},
		{"forgotten", func(d *PurposeOpenLoopDetail) { d.Current.LifecycleState = PurposeStateDismissed }, quiet, false},
		{"only a guess", func(d *PurposeOpenLoopDetail) { d.Current.SupportState = PurposeSupportHypothesis }, quiet, false},
	}
	for _, item := range cases {
		got := goalCheckInFor([]PurposeOpenLoopDetail{goalCheckInFixture("g", now, item.edit)}, nil, item.touched, now)
		if (got != nil) != item.want {
			t.Errorf("%s: check-in = %+v, want %v", item.name, got, item.want)
		}
	}
	// At most one at a time: the most overdue.
	details := []PurposeOpenLoopDetail{
		goalCheckInFixture("newer", now, nil),
		goalCheckInFixture("older", now, func(d *PurposeOpenLoopDetail) { d.Current.DueValue = now.AddDate(0, 0, -90).Format("2006-01-02") }),
	}
	if got := goalCheckInFor(details, nil, quiet, now); got == nil || got.GoalID != "older" || got.Title != "Goal older" || got.EventID != "older-e1" {
		t.Fatalf("check-in = %+v, want the most overdue goal", got)
	}
}

func TestGoalCheckInShowsOncePerGoal(t *testing.T) {
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	quiet := func(PurposeOpenLoopDetail) (time.Time, bool) { return time.Time{}, true }
	goal := goalCheckInFixture("g", now, nil)
	cases := []struct {
		name   string
		record goalCheckInRecord
		want   bool
	}{
		{"shown earlier today, unchanged", goalCheckInRecord{EventID: "g-e1", ShownAt: now.Add(-2 * time.Hour).UnixMilli()}, true},
		{"shown yesterday", goalCheckInRecord{EventID: "g-e1", ShownAt: now.AddDate(0, 0, -1).UnixMilli()}, false},
		{"shown today, then moved", goalCheckInRecord{EventID: "g-e0", ShownAt: now.Add(-time.Hour).UnixMilli()}, false},
	}
	for _, item := range cases {
		got := goalCheckInFor([]PurposeOpenLoopDetail{goal}, map[string]goalCheckInRecord{"g": item.record}, quiet, now)
		if (got != nil) != item.want {
			t.Errorf("%s: check-in = %+v, want %v", item.name, got, item.want)
		}
	}
}

func TestGoalCheckInOnTodayAndKeep(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	store := NewPurposeMapStore(app.activity.store.db)
	now := time.Now()
	previous := goalCheckInNow
	goalCheckInNow = func() time.Time { return now }
	t.Cleanup(func() { goalCheckInNow = previous })
	stale := func(key, title string) PurposeOpenLoopDetail {
		at := now.AddDate(0, 0, -40)
		input, err := userPurposeCreateInput(userPurposeCreate{Key: "test:" + key, Origin: "test", Kind: PurposeKindGoal, Text: title}, at)
		if err != nil {
			t.Fatal(err)
		}
		input.DueKind, input.DueValue = PurposeDueDate, now.AddDate(0, 0, -45).Format("2006-01-02")
		created, _, err := store.Create(input)
		if err != nil {
			t.Fatal(err)
		}
		detail, err := store.GetDetail(created.OpenLoop.ID)
		if err != nil {
			t.Fatal(err)
		}
		return detail
	}
	discovery := stale("discovery", "Discovery")
	got, err := app.GetGoalCheckIn()
	if err != nil || got == nil || got.GoalID != discovery.OpenLoop.ID || got.Title != "Discovery" {
		t.Fatalf("check-in = %+v, %v; want Discovery", got, err)
	}
	if err := app.NoteGoalCheckInShown(got.GoalID, got.EventID); err != nil {
		t.Fatal(err)
	}
	if again, _ := app.GetGoalCheckIn(); again == nil || again.GoalID != got.GoalID {
		t.Fatalf("the check-in left Today on a later read the same day: %+v", again)
	}
	now = now.AddDate(0, 0, 1)
	if next, _ := app.GetGoalCheckIn(); next != nil {
		t.Fatalf("Today asked about the same goal again the next day: %+v", next)
	}

	// Keep: the goal stays tracked, waiting on the same thing, and is not asked about again.
	payroll := stale("payroll", "Run payroll")
	if _, err := app.moveUserPurpose(userPurposeTransition{Key: "wait", Origin: "test", OpenLoopID: payroll.OpenLoop.ID, Action: userPurposeWaiting, WaitingOn: "Finance"}, now.AddDate(0, 0, -35)); err != nil {
		t.Fatal(err)
	}
	asked, _ := app.GetGoalCheckIn()
	if asked == nil || asked.GoalID != payroll.OpenLoop.ID {
		t.Fatalf("check-in = %+v, want Run payroll", asked)
	}
	if err := app.NoteGoalCheckInShown(asked.GoalID, asked.EventID); err != nil {
		t.Fatal(err)
	}
	kept := goalTestMove(t, app, asked.GoalID, asked.EventID, userPurposeKeep, "keep")
	detail, err := store.GetDetail(payroll.OpenLoop.ID)
	if err != nil {
		t.Fatal(err)
	}
	if kept.State != PurposeStateWaiting || detail.Current.WaitingCondition != "Finance" || detail.Current.EventType != PurposeEventReinforced {
		t.Fatalf("Keep changed the goal: %q %+v", kept.State, detail.Current)
	}
	if after, _ := app.GetGoalCheckIn(); after != nil {
		t.Fatalf("Today still asks after Keep: %+v", after)
	}
}

func goalCheckInStaleGoal(t *testing.T, app *App, store *PurposeMapStore, key, title string, now time.Time, edit func(*CreatePurposeOpenLoopInput)) PurposeOpenLoopDetail {
	t.Helper()
	input, err := userPurposeCreateInput(userPurposeCreate{Key: "test:" + key, Origin: "test", Kind: PurposeKindGoal, Text: title}, now.AddDate(0, 0, -40))
	if err != nil {
		t.Fatal(err)
	}
	input.DueKind, input.DueValue = PurposeDueDate, now.AddDate(0, 0, -45).Format("2006-01-02")
	if edit != nil {
		edit(&input)
	}
	created, _, err := store.Create(input)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := store.GetDetail(created.OpenLoop.ID)
	if err != nil {
		t.Fatal(err)
	}
	return detail
}

func TestGoalCheckInAsksNothingWhileObservationIsPaused(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	store := NewPurposeMapStore(app.activity.store.db)
	now := time.Now()
	previous := goalCheckInNow
	goalCheckInNow = func() time.Time { return now }
	t.Cleanup(func() { goalCheckInNow = previous })
	goalCheckInStaleGoal(t, app, store, "paused", "Discovery", now, nil)
	asked := func() bool {
		t.Helper()
		got, err := app.GetGoalCheckIn()
		if err != nil {
			t.Fatal(err)
		}
		return got != nil
	}
	if !asked() {
		t.Fatal("the fixture is not asked about")
	}
	app.closedEyes.enabled.Store(true)
	if asked() {
		t.Fatal("Today asked about a goal with Closed Eyes on")
	}
	app.closedEyes.enabled.Store(false)
	app.saveObservationPauses([]observationPause{{Start: now.Add(-time.Hour).UnixMilli()}})
	if asked() {
		t.Fatal("Today asked about a goal while observation is paused")
	}
	app.saveObservationPauses([]observationPause{{Start: now.Add(-3 * time.Hour).UnixMilli(), End: now.Add(-2 * time.Hour).UnixMilli()}})
	if !asked() {
		t.Fatal("an ended pause still stops the check-in")
	}
	if err := app.store.SetUIState(context.Background(), observationPausesKey, "not json"); err != nil {
		t.Fatal(err)
	}
	if asked() {
		t.Fatal("a pause log that cannot be read must count as paused")
	}

	// Nothing is read while paused: with the goal ledger unreadable, a read
	// before the pause check would answer an error instead of nothing.
	app.saveObservationPauses([]observationPause{{Start: now.Add(-time.Minute).UnixMilli()}})
	if err := app.activity.store.db.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := app.GetGoalCheckIn(); got != nil || err != nil {
		t.Fatalf("paused check-in = %+v, %v; want nothing and no read", got, err)
	}
	app.closedEyes.enabled.Store(true)
	app.saveObservationPauses(nil)
	if got, err := app.GetGoalCheckIn(); got != nil || err != nil {
		t.Fatalf("Closed Eyes check-in = %+v, %v; want nothing and no read", got, err)
	}
}

func TestGoalCheckInNeverLosesARecordToAConcurrentNote(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	// Hold the first note between its read and its write until the second
	// note has had every chance to read the same records. With the lock the
	// second waits and both records survive; without it both read nothing
	// and the later write drops the first record.
	firstHeld, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	previous := goalCheckInBeforeWrite
	goalCheckInBeforeWrite = func() {
		held := false
		once.Do(func() { held = true })
		if held {
			close(firstHeld)
			<-release
		}
	}
	t.Cleanup(func() { goalCheckInBeforeWrite = previous })
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := app.NoteGoalCheckInShown("goal-a", "e"); err != nil {
			t.Error(err)
		}
	}()
	<-firstHeld
	second := make(chan struct{})
	go func() {
		defer wg.Done()
		defer close(second)
		if err := app.NoteGoalCheckInShown("goal-b", "e"); err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-second:
		t.Fatal("the second note finished while the first held its read: the records have no single writer")
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	wg.Wait()
	records, err := app.goalCheckInRecords()
	if err != nil || len(records) != 2 {
		t.Fatalf("records = %v, %v; want both goals (a lost record would ask that goal twice)", records, err)
	}
}

func TestKeepIsOnlyForGoalsBeingTracked(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	store := NewPurposeMapStore(app.activity.store.db)
	now := time.Now()
	candidate := goalCheckInStaleGoal(t, app, store, "candidate", "Maybe learn the cello", now, func(input *CreatePurposeOpenLoopInput) {
		input.Evidence[0].EvidenceClass = PurposeEvidenceObservedUser
	})
	if candidate.Current.LifecycleState != PurposeStateCandidate {
		t.Fatalf("fixture state = %q, want candidate", candidate.Current.LifecycleState)
	}
	if _, err := app.MoveGoal(GoalMoveInput{GoalID: candidate.OpenLoop.ID, Action: userPurposeKeep, Key: "keep-candidate"}); !errors.Is(err, errGoalMoveChanged) {
		t.Fatalf("keep of a candidate = %v, want goal_changed", err)
	}
	active := goalCheckInStaleGoal(t, app, store, "active", "Discovery", now, nil)
	if kept := goalTestMove(t, app, active.OpenLoop.ID, active.Current.ID, userPurposeKeep, "keep-active"); kept.State != PurposeStateActive {
		t.Fatalf("keep of an active goal = %q", kept.State)
	}
}
