package main

import (
	"context"
	"testing"
	"time"

	"wails-gui/internal/handoff"
	"wails-gui/internal/pii"
)

func TestYourWeekTaskIdentityRequiresOpenedCompletedTask(t *testing.T) {
	rig := newHandoffTestRig(t)
	start := time.Now().Add(-72 * time.Hour)
	cutoff := time.Now()
	entry := handoff.Entry{ID: "finished-task", Title: "Report", Status: handoff.StatusCompleted,
		Thread: &handoff.Thread{OpenedAt: start.Add(2 * time.Hour).UnixMilli(), Outcome: &handoff.Outcome{
			Kind: handoff.OutcomeCompleted, By: handoff.OutcomeByIsoform, At: start.Add(time.Hour).UnixMilli(),
			Params: map[string]string{"summary": "The report is ready."},
		}}}
	if _, err := rig.handoffs.Save(entry); err != nil {
		t.Fatal(err)
	}
	snap := rig.app.gatherYourWeek(context.Background(), start, cutoff, cutoff, 5)
	if len(snap.Tasks) != 1 || snap.Tasks[0].ID != entry.ID {
		t.Fatalf("snapshot did not retain task identity: %+v", snap.Tasks)
	}
	in := rig.app.yourWeekReadable(context.Background(), snap, start, cutoff)
	if len(in.Done) != 1 || in.Done[0].TaskID != entry.ID || in.Done[0].Title != "The report is ready." {
		t.Fatalf("week did not project the task thread: %+v", in.Done)
	}
	entry.Thread.OpenedAt = cutoff.Add(time.Hour).UnixMilli()
	if _, err := rig.handoffs.Save(entry); err != nil {
		t.Fatal(err)
	}
	if got := rig.app.yourWeekReadable(context.Background(), snap, start, cutoff).Done; len(got) != 1 || got[0].TaskID != entry.ID {
		t.Fatalf("a later opening erased the frozen receipt: %+v", got)
	}
	previous := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
	pii.SetUserSensitiveApps(map[string]string{"com.example.secret": "Secret"})
	entry.AppName = "Secret"
	if _, err := rig.handoffs.Save(entry); err != nil {
		t.Fatal(err)
	}
	if got := rig.app.yourWeekReadable(context.Background(), snap, start, cutoff).Done; len(got) != 0 {
		t.Fatalf("withdrawn task remained: %+v", got)
	}
}

func TestYourWeekTaskRequiresRealOpeningBeforeCutoff(t *testing.T) {
	start := time.Now().Add(-24 * time.Hour)
	cutoff := time.Now()
	entry := handoff.Entry{ID: "task", Title: "Report", Status: handoff.StatusCompleted,
		Thread: &handoff.Thread{Outcome: &handoff.Outcome{Kind: handoff.OutcomeCompleted, By: handoff.OutcomeByIsoform,
			At: start.Add(time.Hour).UnixMilli(), Params: map[string]string{"summary": "Ready"}}}}
	for _, opened := range []int64{0, start.UnixMilli(), cutoff.UnixMilli()} {
		entry.Thread.OpenedAt = opened
		if row, ok := yourWeekOpenedTask(entry, nil, start, cutoff); ok {
			t.Fatalf("unseen task at %d = %+v", opened, row)
		}
	}
	entry.Thread.OpenedAt = start.Add(2 * time.Hour).UnixMilli()
	entry.Thread.Outcome.By = handoff.OutcomeByYou
	if row, ok := yourWeekOpenedTask(entry, nil, start, cutoff); ok {
		t.Fatalf("user completion attributed to Isoform: %+v", row)
	}
}
