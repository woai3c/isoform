package main

import (
	"testing"
	"time"

	"wails-gui/internal/handoff"
)

func setNeedsPutOffNextBriefForTest(t *testing.T, at int64) {
	t.Helper()
	previous := needsPutOffNextBrief
	needsPutOffNextBrief = func(*App) int64 { return at }
	t.Cleanup(func() { needsPutOffNextBrief = previous })
}

func TestNeedsYouNotNowIsStoredUntilTheNextBrief(t *testing.T) {
	rig := newHandoffTestRig(t)
	brief := time.Now().Add(3 * time.Hour).UnixMilli()
	setNeedsPutOffNextBriefForTest(t, brief)
	if _, err := rig.handoffs.Save(handoff.Entry{ID: "waiting", Title: "Approve the task", Status: handoff.StatusActive}); err != nil {
		t.Fatal(err)
	}
	if err := rig.app.PutOffNeedsYou("waiting", needsPutOffActionPutOff); err != nil {
		t.Fatal(err)
	}
	page, err := rig.app.GetNeedsYou(0)
	if err != nil || page.Total != 0 || len(page.PutOff) != 1 || page.PutOff[0].TargetID != "waiting" || page.PutOff[0].PutOffUntil != brief || page.NextReturnAt != brief {
		t.Fatalf("after Not now = %+v, %v", page, err)
	}
	saved, err := rig.handoffs.Get("waiting")
	if err != nil || saved.Thread == nil || saved.Thread.PutOff == nil || saved.Thread.PutOff.Until != brief || saved.Thread.PutOff.Times != 1 {
		t.Fatalf("Not now is not stored on the task: %+v, %v", saved.Thread, err)
	}
	if _, err := rig.handoffs.MutateThread("waiting", func(thread *handoff.Thread) { thread.PutOff.Until = time.Now().Add(-time.Second).UnixMilli() }); err != nil {
		t.Fatal(err)
	}
	page, err = rig.app.GetNeedsYou(0)
	if err != nil || page.Total != 1 || len(page.PutOff) != 0 || page.Items[0].AsksKeep {
		t.Fatalf("task did not come back with the brief: %+v, %v", page, err)
	}
}

func TestNeedsYouNotNowComesBackWhenSomethingNewHappens(t *testing.T) {
	rig := newHandoffTestRig(t)
	setNeedsPutOffNextBriefForTest(t, time.Now().Add(3*time.Hour).UnixMilli())
	if _, err := rig.handoffs.Save(handoff.Entry{ID: "waiting", Title: "Approve the task", Status: handoff.StatusActive}); err != nil {
		t.Fatal(err)
	}
	if err := rig.app.PutOffNeedsYou("waiting", needsPutOffActionPutOff); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.handoffs.MutateThread("waiting", func(thread *handoff.Thread) {
		thread.Events = append(thread.Events, handoff.Event{ID: "e-new", At: time.Now().UnixMilli()})
	}); err != nil {
		t.Fatal(err)
	}
	page, err := rig.app.GetNeedsYou(0)
	if err != nil || page.Total != 1 || len(page.PutOff) != 0 {
		t.Fatalf("task with news stayed put off: %+v, %v", page, err)
	}
}

func TestNeedsYouBringBackNowAndTheSecondReturnAsksKeep(t *testing.T) {
	rig := newHandoffTestRig(t)
	setNeedsPutOffNextBriefForTest(t, time.Now().Add(3*time.Hour).UnixMilli())
	if _, err := rig.handoffs.Save(handoff.Entry{ID: "waiting", Title: "Approve the task", Status: handoff.StatusActive}); err != nil {
		t.Fatal(err)
	}
	for round := 1; round <= 2; round++ {
		if err := rig.app.PutOffNeedsYou("waiting", needsPutOffActionPutOff); err != nil {
			t.Fatal(err)
		}
		if err := rig.app.PutOffNeedsYou("waiting", needsPutOffActionBringBack); err != nil {
			t.Fatal(err)
		}
		page, err := rig.app.GetNeedsYou(0)
		if err != nil || page.Total != 1 || page.Items[0].AsksKeep != (round == 2) {
			t.Fatalf("return %d = %+v, %v", round, page, err)
		}
	}
	if err := rig.app.PutOffNeedsYou("waiting", needsPutOffActionKeep); err != nil {
		t.Fatal(err)
	}
	page, err := rig.app.GetNeedsYou(0)
	if err != nil || page.Total != 1 || page.Items[0].AsksKeep {
		t.Fatalf("Keep still asks: %+v, %v", page, err)
	}
	saved, _ := rig.handoffs.Get("waiting")
	if saved.Thread.PutOff != nil {
		t.Fatalf("Keep left the put-off record: %+v", saved.Thread.PutOff)
	}
}

func TestNeedsYouForgetKeepsItAwayUntilSomethingNewHappens(t *testing.T) {
	rig := newHandoffTestRig(t)
	setNeedsPutOffNextBriefForTest(t, time.Now().Add(time.Hour).UnixMilli())
	if _, err := rig.handoffs.Save(handoff.Entry{ID: "waiting", Title: "Approve the task", Status: handoff.StatusActive}); err != nil {
		t.Fatal(err)
	}
	if err := rig.app.PutOffNeedsYou("waiting", needsPutOffActionForget); err != nil {
		t.Fatal(err)
	}
	saved, _ := rig.handoffs.Get("waiting")
	if saved.Thread.PutOff == nil || saved.Thread.PutOff.Until < time.Now().Add(365*24*time.Hour).UnixMilli() {
		t.Fatalf("Forget put-off = %+v, want it away well past the next brief", saved.Thread.PutOff)
	}
	page, err := rig.app.GetNeedsYou(0)
	if err != nil || page.Total != 0 || len(page.PutOff) != 1 {
		t.Fatalf("after Forget = %+v, %v", page, err)
	}
	if _, err := rig.handoffs.MutateThread("waiting", func(thread *handoff.Thread) {
		thread.Events = append(thread.Events, handoff.Event{ID: "e-new", At: time.Now().UnixMilli()})
	}); err != nil {
		t.Fatal(err)
	}
	page, err = rig.app.GetNeedsYou(0)
	if err != nil || page.Total != 1 {
		t.Fatalf("something new happened but it stayed away: %+v, %v", page, err)
	}
}

func TestNeedsYouNotNowOnAWaitingItemThatIsNotATask(t *testing.T) {
	rig := newHandoffTestRig(t)
	if rig.app.store == nil {
		t.Skip("no store")
	}
	now := time.Now()
	need := TodayNeed{ID: "chat-row", Kind: "waiting_on_reply", Sentence: "A reply is waiting", TargetKind: "chat_item", TargetID: "chat-row"}
	state := map[string]*handoff.PutOff{}
	state[need.ID] = state[need.ID].Next(now, now.Add(time.Hour).UnixMilli(), todayNeedMark(need))
	if err := rig.app.saveNeedsPutOff(state); err != nil {
		t.Fatal(err)
	}
	shown, away, _ := rig.app.partitionPutOff([]TodayNeed{need}, nil, nil, now)
	if len(shown) != 0 || len(away) != 1 {
		t.Fatalf("shown = %+v, away = %+v", shown, away)
	}
	changed := need
	changed.Sentence = "A new reply is waiting"
	shown, away, _ = rig.app.partitionPutOff([]TodayNeed{changed}, nil, nil, now)
	if len(shown) != 1 || len(away) != 0 {
		t.Fatalf("changed item stayed put off: shown = %+v, away = %+v", shown, away)
	}
}

func TestNeedsYouNotNowRefusesUnknownActions(t *testing.T) {
	rig := newHandoffTestRig(t)
	if err := rig.app.PutOffNeedsYou("waiting", "snooze"); err == nil {
		t.Fatal("unknown action accepted")
	}
}
