package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/storage"
)

func TestEngagementRoutineResultRemainsInChatWhenChosenDeliveryIsUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name    string
		channel engagement.Channel
		reason  string
	}{
		{"desktop denied", engagement.ChannelDesktop, "retry"},
		{"phone unregistered", engagement.ChannelPush, "no chosen channel"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rig := newEngagementTestRig(t)
			app := &App{store: rig.store, engagement: rig.svc, eventBroadcaster: NewEventBroadcaster()}
			reports := 0
			rig.svc.deliver.Report = func(m engagement.Moment, text engagementText) bool {
				reports++
				if _, exposed := m.Params["_chat_reported"]; exposed {
					t.Fatal("internal reporting state reached presentation")
				}
				return app.postEngagementReport(m, text)
			}
			desktop := rig.svc.deliver.Desktop
			switch tc.channel {
			case engagement.ChannelDesktop:
				rig.svc.deliver.Desktop = func(context.Context, storage.EngagementMomentRow, engagement.Moment, engagement.Verdict, engagementText) error {
					return errors.New("notifications are off for Isoform in System Settings")
				}
			case engagement.ChannelPush:
				rig.presence.PhoneRegistered = false
			}
			ctx := context.Background()
			row, err := rig.svc.Submit(ctx, engagement.Moment{
				Kind: engagement.KindPlaybookResult, SubjectID: "playbook-run",
				Channels: []engagement.Channel{tc.channel}, ExpiresAt: rig.clock.Add(24 * time.Hour),
				Params: map[string]string{"title": "Weekly review", "summary": "Your review is ready."},
			})
			if err != nil {
				t.Fatal(err)
			}
			rig.svc.Pass(ctx)
			stored, err := rig.store.GetEngagementMoment(ctx, row.ID)
			if err != nil || stored == nil || stored.State != storage.EngagementStateScheduled || stored.Reason != tc.reason || stored.Channel != "" || stored.SentAt != rig.clock.UnixMilli() {
				t.Fatalf("unavailable explicit delivery = %+v, %v", stored, err)
			}
			firstSentAt := stored.SentAt
			stats, err := rig.store.EngagementStatsSince(ctx, rig.clock.Add(-time.Hour))
			if err != nil || stats[string(engagement.KindPlaybookResult)].Sent != 1 {
				t.Fatalf("durable chat must count exactly one send: %+v, %v", stats, err)
			}
			entries := mainChatStatusEntries(t, app)
			if len(entries) != 1 || entries[0].Kind != string(engagement.KindPlaybookResult) {
				t.Fatalf("failed delivery lost the chat result: %+v", entries)
			}
			status := mainChatStructuredRows(t, app, mainChatStatusTool)[0]
			if rig.svc.state.SentByKindToday[engagement.KindPlaybookResult] != 0 || rig.svc.state.InterruptsToday != 0 {
				t.Fatal("chat availability falsely counted an external delivery")
			}

			// Retry after a process restart and a different digest period. It
			// must not create another status row or move the original again.
			rig.clock = rig.clock.Add(4 * time.Hour)
			reopened := newEngagementService(rig.store, rig.svc.presence, rig.svc.deliver)
			reopened.now = rig.svc.now
			if err := reopened.load(ctx); err != nil {
				t.Fatal(err)
			}
			rig.svc, app.engagement = reopened, reopened
			rig.svc.Pass(ctx)
			rows := mainChatStructuredRows(t, app, mainChatStatusTool)
			if reports != 1 || len(rows) != 1 || rows[0].ID != status.ID || rows[0].SortOrder != status.SortOrder || len(mainChatStatusEntries(t, app)) != 1 {
				t.Fatalf("retry repeated the chat result: reports=%d rows=%+v", reports, rows)
			}

			// Once the selected surface returns, only that surface receives
			// the result and the original chat row remains the single record.
			rig.svc.deliver.Desktop = desktop
			rig.presence.PhoneRegistered = true
			rig.clock = rig.clock.Add(engagementRetryDelay)
			rig.svc.Pass(ctx)
			stored, err = rig.store.GetEngagementMoment(ctx, row.ID)
			if err != nil || stored == nil || stored.State != storage.EngagementStateSent || stored.Channel != string(tc.channel) || stored.SentAt != firstSentAt {
				t.Fatalf("recovered explicit delivery = %+v, %v", stored, err)
			}
			stats, err = rig.store.EngagementStatsSince(ctx, time.UnixMilli(firstSentAt).Add(-time.Hour))
			if err != nil || stats[string(engagement.KindPlaybookResult)].Sent != 1 {
				t.Fatalf("external recovery counted the same message twice: %+v, %v", stats, err)
			}
			if reports != 1 || len(rig.pushed)+len(rig.desktop) != 1 || len(mainChatStatusEntries(t, app)) != 1 {
				t.Fatalf("recovery repeated delivery: reports=%d push=%d desktop=%d", reports, len(rig.pushed), len(rig.desktop))
			}
			if rig.svc.state.SentByKindToday[engagement.KindPlaybookResult] != 1 || rig.svc.state.InterruptsToday != 0 {
				t.Fatal("recovery changed the requested-result pacing rules")
			}
		})
	}
}

func TestEngagementRoutineResultReportingSurvivesPartialDeliveryAndExpiry(t *testing.T) {
	rig := newEngagementTestRig(t)
	app := &App{store: rig.store, engagement: rig.svc, eventBroadcaster: NewEventBroadcaster()}
	reports := 0
	rig.svc.deliver.Report = func(m engagement.Moment, text engagementText) bool {
		reports++
		return app.postEngagementReport(m, text)
	}
	rig.presence.PhoneRegistered = false
	ctx := context.Background()
	row, err := rig.svc.Submit(ctx, engagement.Moment{
		Kind: engagement.KindPlaybookResult, SubjectID: "partial-run", ExpiresAt: rig.clock.Add(24 * time.Hour),
		Channels: []engagement.Channel{engagement.ChannelDesktop, engagement.ChannelPush},
		Params:   map[string]string{"title": "Weekly review", "summary": "Your review is ready.", "_chat_reported": "true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	rig.svc.Pass(ctx)
	stored, err := rig.store.GetEngagementMoment(ctx, row.ID)
	if err != nil || stored == nil || stored.State != storage.EngagementStateScheduled || stored.Channel != "desktop" {
		t.Fatalf("partial delivery = %+v, %v", stored, err)
	}
	if reports != 1 || len(mainChatStatusEntries(t, app)) != 1 {
		t.Fatal("producer parameters suppressed the initial chat report")
	}
	if m := momentFromRow(*stored); len(m.Channels) != 1 || m.Channels[0] != engagement.ChannelPush || m.Params["_chat_reported"] != "" {
		t.Fatalf("internal report marker entered the copy parameters: %+v", m)
	}
	rig.clock = rig.clock.Add(4 * time.Hour)
	reopened := newEngagementService(rig.store, rig.svc.presence, rig.svc.deliver)
	reopened.now = rig.svc.now
	if err := reopened.load(ctx); err != nil {
		t.Fatal(err)
	}
	rig.svc, app.engagement = reopened, reopened
	rig.svc.Pass(ctx)
	rig.clock = rig.clock.Add(20 * time.Hour)
	rig.svc.Pass(ctx)
	stored, err = rig.store.GetEngagementMoment(ctx, row.ID)
	if err != nil || stored == nil || stored.State != storage.EngagementStateExpired || stored.Reason != "expired" {
		t.Fatalf("unavailable channel expiry changed: %+v, %v", stored, err)
	}
	if reports != 1 || len(mainChatStatusEntries(t, app)) != 1 || len(rig.desktop) != 1 || len(rig.pushed) != 0 {
		t.Fatal("partial retry or expiry duplicated the result or lost its chat record")
	}
}
