package main

import (
	"context"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/storage"
)

func TestEngagementChatThenNextDayNotificationRestoresPacingBudget(t *testing.T) {
	rig := newEngagementTestRig(t)
	ctx := context.Background()
	rig.clock = time.Date(2026, time.October, 12, 23, 0, 0, 0, rig.clock.Location())
	chatAt := rig.clock
	if err := rig.svc.SetRules(ctx, engagement.DefaultPrefs().Quiet, 1); err != nil {
		t.Fatal(err)
	}
	app := &App{store: rig.store, engagement: rig.svc, eventBroadcaster: NewEventBroadcaster()}
	rig.svc.deliver.Report = app.postEngagementReport
	row, err := rig.svc.Submit(ctx, engagement.Moment{
		Kind: engagement.KindWorkBlocked, SubjectID: "overnight-work", ExpiresAt: chatAt.Add(24 * time.Hour),
		Channels: []engagement.Channel{engagement.ChannelDesktop},
	})
	if err != nil {
		t.Fatal(err)
	}
	spec, _ := engagement.Spec(engagement.KindWorkBlocked)
	moment := momentFromRow(row)
	// Work has a dedicated card path; the generic engagement reporter only
	// acknowledges it and deliberately does not create its card.
	item, ok := app.proactiveItem(moment, rig.svc.render(spec, moment))
	if !ok || app.postProactiveItem(item, item.MomentID) == "" {
		t.Fatal("could not store the overnight chat card")
	}
	if cards := mainChatProactiveItems(t, app); len(cards) != 1 {
		t.Fatalf("overnight card was not persisted: %+v", cards)
	}
	first, err := rig.store.GetEngagementMoment(ctx, row.ID)
	if err != nil || first.SentAt != chatAt.UnixMilli() || first.State != storage.EngagementStatePending {
		t.Fatalf("card must record its first send without delivering the notification: %+v, %v", first, err)
	}
	rig.svc.Pass(ctx)
	if len(rig.desktop) != 0 {
		t.Fatal("quiet hours should hold the external notification")
	}
	rig.clock = rig.clock.Add(9 * time.Hour)
	notifiedAt := rig.clock
	rig.svc.Pass(ctx)
	if len(rig.desktop) != 1 || rig.svc.state.InterruptsToday != 1 {
		t.Fatalf("morning notification did not consume its budget: sends=%d interrupts=%d", len(rig.desktop), rig.svc.state.InterruptsToday)
	}
	stored, err := rig.store.GetEngagementMoment(ctx, row.ID)
	if err != nil || stored.SentAt != chatAt.UnixMilli() {
		t.Fatalf("first chat send changed: %+v, %v", stored, err)
	}
	fresh := newEngagementService(rig.store, nil, rig.svc.deliver)
	fresh.now = rig.svc.now
	app.engagement = fresh
	if err := fresh.load(ctx); err != nil {
		t.Fatal(err)
	}
	if fresh.state.InterruptsToday != 1 || fresh.state.SentByKindToday[engagement.KindWorkBlocked] != 1 {
		t.Fatalf("restart lost today's delivery budget: %+v", fresh.state)
	}
	if got := fresh.state.LastSentByKey[row.DedupeKey]; !got.Equal(notifiedAt) {
		t.Fatalf("cooldown restored at %v, want notification at %v", got, notifiedAt)
	}
	next, err := fresh.Submit(ctx, engagement.Moment{
		Kind: engagement.KindWorkBlocked, SubjectID: "second-work", Channels: []engagement.Channel{engagement.ChannelDesktop},
	})
	if err != nil {
		t.Fatal(err)
	}
	fresh.Pass(ctx)
	pending, err := rig.store.GetEngagementMoment(ctx, next.ID)
	if err != nil || pending.State != storage.EngagementStateScheduled || pending.Reason != "budget" || len(rig.desktop) != 1 {
		t.Fatalf("restart allowed an extra interruption: row=%+v sends=%d err=%v", pending, len(rig.desktop), err)
	}
}
