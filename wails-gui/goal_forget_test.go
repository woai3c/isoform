package main

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"wails-gui/storage"
)

// Guards for item 66: a goal the user marks done or forgets leaves the phone,
// Today and the brief at once, the goal finder never brings a forgotten one
// back from the same sources or the same words, and Undo restores it exactly.

func goalTestMove(t *testing.T, app *App, id, expected, action, key string) GoalMoveResult {
	t.Helper()
	result, err := app.MoveGoal(GoalMoveInput{GoalID: id, ExpectedEventID: expected, Action: action, Key: key})
	if err != nil {
		t.Fatalf("%s %s: %v", action, id, err)
	}
	return result
}

func goalTestBrief(purposeID string) MobileBriefV1 {
	return MobileBriefV1{
		NarrativeStatus: "ok",
		Narrative: []MobileBriefPara{
			{Text: "Discovery has not moved this week.", Refs: []string{"e1"}},
			{Text: "Your afternoon is clear.", Refs: []string{"e2"}},
		},
		Audio:    &MobileBriefAudio{},
		Offers:   []MobileBriefOffer{{Refs: []string{"e1"}}, {Refs: []string{"e2"}}},
		Cards:    []MobileBriefCard{{Refs: []string{"e1"}}, {Refs: []string{"e2"}}},
		Evidence: []MobileBriefRef{{Ref: "e1", SourceKind: "purpose", SourceID: purposeID}, {Ref: "e2", SourceKind: "calendar", SourceID: "day"}},
		Counts:   MobileBriefCounts{OpenLoopsByKind: map[string]int{PurposeKindGoal: 2}},
		Sections: MobileBriefSections{
			WorkingToward: []MobileBriefItem{{ID: "w1", Title: "Discovery", Kind: PurposeKindGoal, PurposeID: purposeID, Refs: []string{"e1"}}},
			// Older briefs name a Purpose row by its first citation only.
			OpenLoops: []MobileBriefItem{{ID: "o1", Title: "Discovery", Kind: PurposeKindGoal, Refs: []string{"e1"}}, {ID: "o2", Title: "Other", Kind: PurposeKindGoal, Refs: []string{"e2"}}},
		},
	}
}

func goalTestListed(t *testing.T, bridge *mobileBridge, id string) bool {
	t.Helper()
	code, body := mobileTestGoalCall(t, bridge, mobileTestGoalDevice(), http.MethodGet, "/goals", "")
	if code != http.StatusOK {
		t.Fatalf("list goals: %d %s", code, body)
	}
	for _, goal := range mobileTestDecodeGoalPage(t, body).Goals {
		if goal.ID == id {
			return true
		}
	}
	return false
}

func TestForgetGoalLeavesThePhoneTodayAndTheBriefAndUndoRestoresIt(t *testing.T) {
	bridge, store, now := mobileTestGoalBridge(t)
	app := bridge.app
	goal := mobileTestPurpose(t, store, "discovery", PurposeKindGoal, "Discovery", false, nil)
	if _, err := app.moveUserPurpose(userPurposeTransition{Key: "wait", Origin: "test", OpenLoopID: goal.OpenLoop.ID, Action: userPurposeWaiting, WaitingOn: "Ana"}, now); err != nil {
		t.Fatal(err)
	}
	if !goalTestListed(t, bridge, goal.OpenLoop.ID) {
		t.Fatal("the goal is not on the phone before it is forgotten")
	}

	forgot := goalTestMove(t, app, goal.OpenLoop.ID, "", userPurposeForget, "forget-1")
	if forgot.State != PurposeStateDismissed {
		t.Fatalf("forget state = %q", forgot.State)
	}
	detail, err := store.GetDetail(goal.OpenLoop.ID)
	if err != nil {
		t.Fatalf("a forgotten goal is erased instead of hidden: %v", err)
	}
	if mobileBriefPurposeCurrent(detail) {
		t.Fatal("the next brief would still collect the forgotten goal")
	}
	if goalTestListed(t, bridge, goal.OpenLoop.ID) {
		t.Fatal("the phone still lists the forgotten goal")
	}
	if code, _ := mobileTestGoalCall(t, bridge, mobileTestGoalDevice(), http.MethodGet, "/goals/"+goal.OpenLoop.ID, ""); code != http.StatusNotFound {
		t.Fatalf("the phone can still open the forgotten goal: %d", code)
	}
	brief := app.mobileBriefWithoutClosedPurposes(goalTestBrief(goal.OpenLoop.ID))
	if len(brief.Sections.WorkingToward) != 0 || len(brief.Sections.OpenLoops) != 1 || brief.Sections.OpenLoops[0].ID != "o2" {
		t.Fatalf("the brief still shows the forgotten goal: %+v", brief.Sections)
	}
	if len(brief.Narrative) != 1 || brief.Narrative[0].Refs[0] != "e2" || brief.NarrativeStatus != "ok" || brief.Audio != nil {
		t.Fatalf("the written brief still names the forgotten goal: %+v audio=%v", brief.Narrative, brief.Audio)
	}
	if len(brief.Offers) != 1 || len(brief.Cards) != 1 || len(brief.Evidence) != 1 || brief.Counts.OpenLoopsByKind[PurposeKindGoal] != 1 {
		t.Fatalf("offers, cards, evidence or counts still carry the forgotten goal: %+v %+v %+v %+v", brief.Offers, brief.Cards, brief.Evidence, brief.Counts)
	}

	restored := goalTestMove(t, app, goal.OpenLoop.ID, forgot.EventID, userPurposeRestore, "undo-1")
	detail, err = store.GetDetail(goal.OpenLoop.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.State != PurposeStateWaiting || detail.Current.WaitingCondition != "Ana" {
		t.Fatalf("Undo did not put the goal back to waiting on Ana: %q %+v", restored.State, detail.Current)
	}
	events := len(detail.Events)
	again := goalTestMove(t, app, goal.OpenLoop.ID, forgot.EventID, userPurposeRestore, "undo-1")
	if detail, _ = store.GetDetail(goal.OpenLoop.ID); len(detail.Events) != events || again.EventID != restored.EventID {
		t.Fatalf("a retried Undo moved the goal again: %d events, want %d", len(detail.Events), events)
	}
	if !goalTestListed(t, bridge, goal.OpenLoop.ID) {
		t.Fatal("the phone does not list the goal after Undo")
	}
	if brief := app.mobileBriefWithoutClosedPurposes(goalTestBrief(goal.OpenLoop.ID)); len(brief.Sections.WorkingToward) != 1 || len(brief.Narrative) != 2 || brief.Audio == nil {
		t.Fatalf("the brief does not show the goal as written after Undo: %+v", brief)
	}

	done := goalTestMove(t, app, goal.OpenLoop.ID, restored.EventID, userPurposeResolve, "done-1")
	if brief := app.mobileBriefWithoutClosedPurposes(goalTestBrief(goal.OpenLoop.ID)); len(brief.Sections.WorkingToward) != 0 {
		t.Fatal("a goal marked done still shows in What you're working toward")
	}
	if undone := goalTestMove(t, app, goal.OpenLoop.ID, done.EventID, userPurposeRestore, "undo-2"); undone.State != PurposeStateWaiting {
		t.Fatalf("Undo after done = %q, want waiting", undone.State)
	}
	if _, err := app.MoveGoal(GoalMoveInput{GoalID: goal.OpenLoop.ID, ExpectedEventID: done.EventID, Action: userPurposeForget, Key: "stale"}); !errors.Is(err, errGoalMoveChanged) {
		t.Fatalf("a move against a goal that changed = %v, want goal_changed", err)
	}
	if _, err := app.MoveGoal(GoalMoveInput{GoalID: goal.OpenLoop.ID, Action: "erase", Key: "bad"}); !errors.Is(err, errGoalMoveInvalid) {
		t.Fatalf("an unknown action = %v, want goal_invalid", err)
	}
}

func TestRestoreNeedsADoneOrForgottenGoal(t *testing.T) {
	bridge, store, _ := mobileTestGoalBridge(t)
	goal := mobileTestPurpose(t, store, "active", PurposeKindGoal, "Plant the garden", false, nil)
	if _, err := bridge.app.MoveGoal(GoalMoveInput{GoalID: goal.OpenLoop.ID, ExpectedEventID: goal.Current.ID, Action: userPurposeRestore, Key: "undo"}); !errors.Is(err, errGoalMoveChanged) {
		t.Fatalf("restore of an open goal = %v, want goal_changed", err)
	}
	forgot := goalTestMove(t, bridge.app, goal.OpenLoop.ID, "", userPurposeForget, "forget")
	// Undo names the forget it takes back.
	if _, err := bridge.app.MoveGoal(GoalMoveInput{GoalID: goal.OpenLoop.ID, Action: userPurposeRestore, Key: "blind"}); !errors.Is(err, errGoalMoveInvalid) {
		t.Fatalf("restore without the event it undoes = %v, want goal_invalid", err)
	}
	if restored := goalTestMove(t, bridge.app, goal.OpenLoop.ID, forgot.EventID, userPurposeRestore, "undo"); restored.State != PurposeStateActive {
		t.Fatalf("Undo of a forgotten active goal = %q, want active", restored.State)
	}
}

func TestThePhoneCannotRestoreAGoalItNeverSawClosed(t *testing.T) {
	bridge, store, _ := mobileTestGoalBridge(t)
	device := mobileTestGoalDevice()
	// Dismissed by someone else (the goal finder, a deleted reminder): the phone never saw that event.
	goal := mobileTestPurpose(t, store, "dismissed-elsewhere", PurposeKindGoal, "Discovery", false, nil)
	goalTestMove(t, bridge.app, goal.OpenLoop.ID, "", userPurposeForget, "elsewhere")
	path := "/goals/" + goal.OpenLoop.ID + "/events"
	if code, body := mobileTestGoalCall(t, bridge, device, http.MethodPost, path, `{"idempotencyKey":"r1","action":"restore"}`); code != http.StatusBadRequest {
		t.Fatalf("restore without expectedEventId = %d %s, want 400", code, body)
	}
	if code, _ := mobileTestGoalCall(t, bridge, device, http.MethodPost, path, `{"idempotencyKey":"r2","action":"restore","expectedEventId":"guessed"}`); code != http.StatusConflict {
		t.Fatalf("restore naming another event = %d, want 409", code)
	}
	if detail, _ := store.GetDetail(goal.OpenLoop.ID); detail.Current.LifecycleState != PurposeStateDismissed {
		t.Fatalf("the goal came back: %q", detail.Current.LifecycleState)
	}
}

func TestGoalFinderNeverBringsBackAForgottenGoal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	store := newTestStore(t)
	app := &App{activity: &activityMonitorState{store: store}}
	minute := 0
	scan := func(sourceID, point, canonical, title string, sourceIDs ...string) PurposeReconciliationReceiptRecord {
		t.Helper()
		minute += 2
		if point != "" {
			purposeWriteMemory(t, home, sourceID, purposeExtractionTestTime(minute), memorySource("manual"), point)
		}
		request := PurposeExtractionRequest{
			IdempotencyKey: "goal-forget-" + canonical + "-" + sourceID,
			CutoffAt:       purposeExtractionTestTime(minute + 1).UnixMilli(),
		}
		var refs []string
		for _, id := range append([]string{sourceID}, sourceIDs...) {
			refs = append(refs, purposeExtractionSourceRef(t, store, request, id))
		}
		hypothesis := purposeExtractionHypothesis(sourceID+canonical, canonical, PurposeKindGoal, PurposeOperationSupport, title, title, refs...)
		runner := &purposeExtractionFakeRunner{responses: []string{purposeExtractionOutputJSON(t, hypothesis)}}
		result, err := purposeNewRuntime(t, store, runner).Run(context.Background(), request)
		if err != nil {
			t.Fatalf("scan %s: %v", canonical, err)
		}
		return result.Detail.Receipts[0]
	}
	first := scan("discovery-notes", "I am running Discovery for the Acme account.", "discovery", "Discovery")
	if first.Disposition != PurposeDispositionCreated {
		t.Fatalf("first scan = %q", first.Disposition)
	}
	forgot := goalTestMove(t, app, first.OpenLoopID, "", userPurposeForget, "forget")
	if forgot.State != PurposeStateDismissed {
		t.Fatalf("forget = %q", forgot.State)
	}
	cases := []struct {
		name, source, point, canonical, title string
	}{
		{"the same sources under another key", "discovery-notes", "", "acme-discovery", "Acme account research"},
		{"new sources in the same words", "discovery-again", "Discovery is still on my list.", "discovery-2", "Discovery"},
		{"new sources naming the same aim", "discovery-phase", "The Discovery phase continues.", "discovery-phase", "Finish the Discovery phase"},
		{"the same key with new sources", "discovery-later", "Discovery came up in the standup.", "discovery", "Discovery"},
	}
	for _, item := range cases {
		receipt := scan(item.source, item.point, item.canonical, item.title)
		if receipt.Disposition == PurposeDispositionCreated || receipt.Disposition == PurposeDispositionReinforced {
			t.Fatalf("%s brought the forgotten goal back: %q", item.name, receipt.Disposition)
		}
	}
	details, err := NewPurposeMapStore(store.db).ListDetailsAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(details) != 1 || details[0].Current.LifecycleState != PurposeStateDismissed {
		t.Fatalf("the ledger after the scans = %+v", details)
	}
	// A clearly new mention, in new sources and other words, is a goal of its own.
	if receipt := scan("garden-notes", "I want to plant the spring garden.", "garden", "Plant the spring garden"); receipt.Disposition != PurposeDispositionCreated {
		t.Fatalf("a new goal after a forget = %q, want created", receipt.Disposition)
	}
}

func TestGoalFinderTellsADoneGoalFromNextMonths(t *testing.T) {
	done := PurposeOpenLoopDetail{
		OpenLoop: PurposeOpenLoopRecord{ID: "payroll"},
		Current:  PurposeOpenLoopEventRecord{LifecycleState: PurposeStateResolved},
		Events:   []PurposeOpenLoopEventRecord{{Title: "Run payroll", DesiredOutcome: "Payroll is run"}},
		Evidence: []PurposeEvidenceRecord{{SourceKind: "memory", SourceID: "march", SourceVersion: "v1", SourceHash: "h1"}},
	}
	forgotten := done
	forgotten.Current.LifecycleState = PurposeStateDismissed
	sources := map[string]PurposeExtractionSource{
		"march": {SourceKind: "memory", SourceID: "march", SourceVersion: "v1", SourceHash: "h1"},
		"april": {SourceKind: "memory", SourceID: "april", SourceVersion: "v1", SourceHash: "h2"},
	}
	hypothesis := func(title string, refs ...string) PurposeExtractionHypothesisRecord {
		return PurposeExtractionHypothesisRecord{Title: title, DesiredOutcome: title, EvidenceRefs: refs}
	}
	cases := []struct {
		name   string
		closed PurposeOpenLoopDetail
		in     PurposeExtractionHypothesisRecord
		want   bool
	}{
		{"done, its own source and words", done, hypothesis("Run payroll", "march"), true},
		{"done, new source, same words", done, hypothesis("Run payroll", "april"), false},
		{"done, its own source, other words", done, hypothesis("Order team lunch", "march"), false},
		{"forgotten, its own source, other words", forgotten, hypothesis("Order team lunch", "march"), true},
		{"forgotten, new source, same words", forgotten, hypothesis("Run payroll for April", "april"), true},
		{"forgotten, new source, other words", forgotten, hypothesis("Order team lunch", "april"), false},
		{"a short word does not stand for every aim", PurposeOpenLoopDetail{
			Current: PurposeOpenLoopEventRecord{LifecycleState: PurposeStateDismissed},
			Events:  []PurposeOpenLoopEventRecord{{Title: "Email"}},
		}, hypothesis("Email the landlord about the lease", "april"), false},
	}
	for _, item := range cases {
		if _, got := purposeClosedMatch(item.in, sources, []PurposeOpenLoopDetail{item.closed}); got != item.want {
			t.Errorf("%s: match = %v, want %v", item.name, got, item.want)
		}
	}
}

func TestRemindersNeverBringBackAForgottenReminderGoal(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	store := NewPurposeMapStore(app.activity.store.db)
	now := time.Now()
	row := storage.ReminderRow{UID: "forget-reminder", Title: "Send the lease", DueAt: now.Add(time.Hour).UnixMilli(), List: "Home"}
	created, _, err := store.Create(reminderCreateInput(reminderFromRow(row), now.Add(-time.Hour).UnixMilli(), time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	goalTestMove(t, app, created.OpenLoop.ID, "", userPurposeForget, "forget")
	for _, pass := range []storage.ReminderRow{row, {UID: row.UID, Title: "Send the signed lease", DueAt: row.DueAt, List: "Home"}} {
		if _, err := reconcileReminderCommitments(store, []storage.ReminderRow{pass}, now, time.UTC); err != nil {
			t.Fatal(err)
		}
		detail, err := store.GetDetail(created.OpenLoop.ID)
		if err != nil {
			t.Fatal(err)
		}
		if detail.Current.LifecycleState != PurposeStateDismissed {
			t.Fatalf("a Reminders read brought the forgotten goal back: %q", detail.Current.LifecycleState)
		}
	}
}

func TestPhoneForgetsAGoalAndUndoesIt(t *testing.T) {
	bridge, store, _ := mobileTestGoalBridge(t)
	goal := mobileTestPurpose(t, store, "phone-forget", PurposeKindGoal, "Discovery", false, nil)
	device := mobileTestGoalDevice()
	path := "/goals/" + goal.OpenLoop.ID + "/events"
	code, body := mobileTestGoalCall(t, bridge, device, http.MethodPost, path,
		`{"idempotencyKey":"f1","action":"forget","expectedEventId":"`+goal.Current.ID+`"}`)
	forgot := mobileTestDecodeGoal(t, body)
	if code != http.StatusOK || forgot.State != PurposeStateDismissed || forgot.Group != mobileGoalGroupForgotten || forgot.Status != "Forgotten today." {
		t.Fatalf("forget from the phone = %d %+v", code, forgot)
	}
	if goalTestListed(t, bridge, goal.OpenLoop.ID) {
		t.Fatal("the phone still lists the goal it forgot")
	}
	if code, _ := mobileTestGoalCall(t, bridge, device, http.MethodPost, path, `{"idempotencyKey":"w1","action":"waiting","waitingOn":"Ana"}`); code != http.StatusNotFound {
		t.Fatalf("a forgotten goal could still be moved from the phone: %d", code)
	}
	code, body = mobileTestGoalCall(t, bridge, device, http.MethodPost, path,
		`{"idempotencyKey":"u1","action":"restore","expectedEventId":"`+forgot.EventID+`"}`)
	if restored := mobileTestDecodeGoal(t, body); code != http.StatusOK || restored.State != PurposeStateActive || restored.Group != mobileGoalGroupTracking {
		t.Fatalf("Undo from the phone = %d %+v", code, restored)
	}
	if !goalTestListed(t, bridge, goal.OpenLoop.ID) {
		t.Fatal("the phone does not list the goal after Undo")
	}
	if code, _ := mobileTestGoalCall(t, bridge, device, http.MethodPost, path, `{"idempotencyKey":"x1","action":"erase"}`); code != http.StatusBadRequest {
		t.Fatalf("an unknown action = %d, want 400", code)
	}
}
