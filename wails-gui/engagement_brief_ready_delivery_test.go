package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/storage"
)

// A ready brief reaches the Chat card and the desktop toast even when the
// phone is offline: the phone failing must not withhold the brief, and a
// retry must not post the card a second time.
func TestEngagementBriefReadyPostsChatAndDesktopWhenPhoneIsOffline(t *testing.T) {
	rig := newEngagementTestRig(t)
	app := &App{store: rig.store, engagement: rig.svc, eventBroadcaster: NewEventBroadcaster()}
	reports, desktops := 0, 0
	rig.svc.deliver.Report = func(m engagement.Moment, text engagementText) bool {
		reports++
		return app.postEngagementReport(m, text)
	}
	rig.svc.deliver.Desktop = func(context.Context, storage.EngagementMomentRow, engagement.Moment, engagement.Verdict, engagementText) error {
		desktops++
		return nil
	}
	rig.svc.deliver.Push = func(context.Context, storage.EngagementMomentRow, engagement.Moment, engagement.Verdict, engagementText) error {
		return errors.New("the phone connection is offline")
	}
	ctx := context.Background()
	row, err := rig.svc.Submit(ctx, engagement.Moment{
		Kind: engagement.KindBriefReady, SubjectID: "2026-11-04-evening",
		DedupeKey: engagement.DedupeKey(engagement.KindBriefReady, "2026-11-04-evening"),
		ExpiresAt: rig.clock.Add(6 * time.Hour),
		Params:    map[string]string{"slot": "evening"},
	})
	if err != nil {
		t.Fatal(err)
	}
	rig.svc.Pass(ctx)
	stored, err := rig.store.GetEngagementMoment(ctx, row.ID)
	if err != nil || stored == nil || stored.State != storage.EngagementStateSent {
		t.Fatalf("brief ready with an offline phone = %+v, %v; want sent", stored, err)
	}
	if reports != 1 || desktops != 1 {
		t.Fatalf("chat cards = %d, desktop toasts = %d; want one of each", reports, desktops)
	}
	rig.svc.Pass(ctx)
	if reports != 1 || desktops != 1 {
		t.Fatalf("a later pass repeated delivery: chat = %d, desktop = %d", reports, desktops)
	}
}
