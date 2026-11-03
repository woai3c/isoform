package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/storage"
)

func seedEngagementControlCohort(t *testing.T, rig *engagementTestRig, dismissed int) []string {
	t.Helper()
	ctx := context.Background()
	ids := make([]string, 0, engagementAutoPauseMinSent)
	for i := 0; i < engagementAutoPauseMinSent; i++ {
		row, err := rig.store.CreateEngagementMoment(ctx, storage.EngagementMomentRow{
			Kind: string(engagement.KindWorkBlocked), DedupeKey: fmt.Sprintf("control:%d", i),
			State: storage.EngagementStateSent, Channel: "push",
			CreatedAt: rig.clock.Add(-time.Hour).UnixMilli(), SentAt: rig.clock.UnixMilli(),
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, row.ID)
	}
	for _, id := range ids[:dismissed] {
		if err := rig.svc.Receipt(ctx, id, "push", storage.EngagementReceiptDismissed, "", rig.clock); err != nil {
			t.Fatal(err)
		}
	}
	return ids
}

func TestEngagementResumeAtSendMillisecondSurvivesLateReceiptsAndRestart(t *testing.T) {
	rig := newEngagementTestRig(t)
	ctx := context.Background()
	ids := seedEngagementControlCohort(t, rig, 12)
	kind := engagement.KindWorkBlocked
	if !rig.svc.Paused(kind) {
		t.Fatal("fixture should pause at the threshold")
	}
	resumedAt := rig.clock
	if err := rig.svc.SetKindEnabled(ctx, kind, true); err != nil {
		t.Fatal(err)
	}
	// The old cohort and Resume have identical millisecond timestamps. A
	// receipt arriving later must not make that old cohort eligible again.
	rig.clock = rig.clock.Add(time.Minute)
	if err := rig.svc.Receipt(ctx, ids[12], "push", storage.EngagementReceiptDismissed, "", rig.clock); err != nil {
		t.Fatal(err)
	}
	if rig.svc.Paused(kind) || !rig.svc.Prefs().Enabled[kind] {
		t.Fatal("late receipt immediately undid Resume")
	}
	fresh := newEngagementService(rig.store, nil, engagementDelivery{})
	fresh.now = rig.svc.now
	if err := fresh.load(ctx); err != nil {
		t.Fatal(err)
	}
	if fresh.Paused(kind) || !fresh.Prefs().Enabled[kind] {
		t.Fatal("restart immediately undid Resume")
	}
	if err := fresh.Receipt(ctx, ids[13], "desktop", storage.EngagementReceiptDismissed, "", rig.clock); err != nil {
		t.Fatal(err)
	}
	if fresh.Paused(kind) {
		t.Fatal("late receipt after restart immediately undid Resume")
	}
	control, err := rig.store.GetEngagementKindControl(ctx, string(kind))
	if err != nil || control.Paused || control.ResumedAt != resumedAt.UnixMilli() {
		t.Fatalf("resume boundary changed: %+v, %v", control, err)
	}
}

func TestEngagementMutedReceiptTriggersExactAutoPauseThreshold(t *testing.T) {
	rig := newEngagementTestRig(t)
	ctx := context.Background()
	ids := seedEngagementControlCohort(t, rig, 11)
	kind := engagement.KindWorkBlocked
	if rig.svc.Paused(kind) {
		t.Fatal("eleven of twenty dismissals must not pause")
	}
	if err := rig.svc.Receipt(ctx, ids[11], "desktop", storage.EngagementReceiptMuted, "mute_kind", rig.clock); err != nil {
		t.Fatal(err)
	}
	if !rig.svc.Paused(kind) || rig.svc.Prefs().Enabled[kind] {
		t.Fatal("the twelfth distinct negative receipt must immediately pause and disable the kind")
	}
	control, err := rig.store.GetEngagementKindControl(ctx, string(kind))
	if err != nil || !control.Paused {
		t.Fatalf("pause reason not persisted: %+v, %v", control, err)
	}
	prefs, err := rig.store.ListEngagementPrefs(ctx)
	if err != nil || len(prefs) != 1 || prefs[0].Kind != string(kind) || prefs[0].Enabled != 0 {
		t.Fatalf("paused switch not persisted: %+v, %v", prefs, err)
	}
}

func TestEngagementLevelOffToNormalClearsDurablePause(t *testing.T) {
	rig := newEngagementTestRig(t)
	ctx := context.Background()
	seedEngagementControlCohort(t, rig, 12)
	kind := engagement.KindWorkBlocked
	if !rig.svc.Paused(kind) {
		t.Fatal("fixture should pause at the threshold")
	}
	for _, level := range []engagement.Level{engagement.LevelOff, engagement.LevelNormal} {
		if err := rig.svc.SetLevel(ctx, level); err != nil {
			t.Fatal(err)
		}
	}
	control, err := rig.store.GetEngagementKindControl(ctx, string(kind))
	if err != nil || control.Paused || control.ResumedAt != rig.clock.UnixMilli() {
		t.Fatalf("normal did not clear durable pause: %+v, %v", control, err)
	}
	fresh := newEngagementService(rig.store, nil, engagementDelivery{})
	fresh.now = rig.svc.now
	if err := fresh.load(ctx); err != nil {
		t.Fatal(err)
	}
	spec, _ := engagement.Spec(kind)
	if fresh.Paused(kind) || !fresh.Prefs().KindEnabled(spec) || fresh.Level() != engagement.LevelNormal {
		t.Fatalf("restart lost resumed normal level: paused=%v level=%s", fresh.Paused(kind), fresh.Level())
	}
}
