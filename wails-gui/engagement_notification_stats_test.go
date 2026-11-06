package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/storage"
)

func TestNotificationStatsReceiptsChannelsSummaryAndSamples(t *testing.T) {
	rig := newEngagementTestRig(t)
	ctx := t.Context()
	start := rig.clock.Add(-engagementStatsWindow)
	makeSent := func(id string, kind engagement.Kind, at time.Time, channels string) string {
		t.Helper()
		row, err := rig.store.CreateEngagementMoment(ctx, storage.EngagementMomentRow{
			ID: id, Kind: string(kind), DedupeKey: id, CreatedAt: at.UnixMilli(), Params: `{"title":"private title","unshown":"private source"}`,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := rig.store.SetEngagementMomentState(ctx, row.ID, storage.EngagementStateSent, "sent", storage.EngagementMomentRow{Channel: channels, SentAt: at.UnixMilli()}); err != nil {
			t.Fatal(err)
		}
		return row.ID
	}
	add := func(id, channel, state, action string, at time.Time) {
		t.Helper()
		if err := rig.store.RecordEngagementReceipt(ctx, storage.EngagementReceiptRow{MomentID: id, Channel: channel, State: state, Action: action, At: at.UnixMilli()}); err != nil {
			t.Fatal(err)
		}
	}
	first := makeSent("first", engagement.KindWorkFinished, start, "desktop+push")
	second := makeSent("second", engagement.KindWorkFinished, rig.clock.Add(-time.Hour), "text+email")
	add(first, "desktop", storage.EngagementReceiptOpened, "open", start.Add(time.Minute))
	add(first, "push", storage.EngagementReceiptOpened, "open", start.Add(2*time.Minute))
	add(first, storage.EngagementCompletionChannel, storage.EngagementReceiptActed, storage.EngagementCompletionAction, start.Add(time.Hour))
	add(second, storage.EngagementCompletionChannel, storage.EngagementReceiptActed, storage.EngagementCompletionAction, rig.clock)
	add(second, "email", storage.EngagementReceiptDismissed, "dismiss", rig.clock)
	add(second, "text", storage.EngagementReceiptMuted, "mute_kind", rig.clock)
	// Failed/unreached transports and bookkeeping never become channel labels.
	add(second, "push", storage.EngagementReceiptOpened, "open", rig.clock.Add(time.Second))
	add(second, "inferred", storage.EngagementReceiptSent, "", rig.clock)
	makeSent("old", engagement.KindWorkFinished, start.Add(-time.Millisecond), "today_quiet")
	makeSent("future", engagement.KindWorkFinished, rig.clock.Add(time.Millisecond), "today_quiet")
	if err := rig.svc.SetKindQuiet(ctx, engagement.KindCapturePaused, true); err != nil {
		t.Fatal(err)
	}
	makeSent("quiet", engagement.KindCapturePaused, rig.clock, "today_quiet")
	if err := rig.svc.SetKindEnabled(ctx, engagement.KindGoalCheckIn, false); err != nil {
		t.Fatal(err)
	}

	view, err := (&App{engagement: rig.svc}).GetEngagementSettings()
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Kinds) != len(engagement.AllKinds())-len(engagementSilentKinds)-1 {
		t.Fatalf("all kinds: %d", len(view.Kinds))
	}
	var sent int
	for _, row := range view.Kinds {
		sent += row.Sent
		switch engagement.Kind(row.Kind) {
		case engagement.KindWorkFinished:
			if row.Sent != 2 || row.Opened != 1 || row.Acted != 2 || row.Dismissed != 1 || row.Muted != 1 {
				t.Fatalf("receipt counts: %+v", row)
			}
			if !reflect.DeepEqual(row.Channels, []string{"computer", "phone", "text", "email"}) {
				t.Fatalf("channels: %v", row.Channels)
			}
			if row.SampleCopyKey != row.Kind || row.Tag != engagement.NotificationTagRare {
				t.Fatalf("sample/tag: %+v", row)
			}
			for _, lang := range engagement.Languages {
				copy, ok := engagement.Copy(engagement.Kind(row.SampleCopyKey), lang)
				if !ok || copy.PushBody == "" {
					t.Fatalf("sample must resolve real copy in %s", lang)
				}
			}
		case engagement.KindCapturePaused:
			if !row.Quiet || !row.CanQuiet || row.Tag != engagement.NotificationTagQuiet || !reflect.DeepEqual(row.Channels, []string{"today_quiet"}) {
				t.Fatalf("quiet: %+v", row)
			}
		default:
			if row.Sent != 0 || row.SampleCopyKey != "" || row.Channels == nil || len(row.Channels) != 0 || row.Tag != engagement.NotificationTagNone {
				t.Fatalf("zero sends: %+v", row)
			}
		}
		if row.CanQuiet != engagement.QuietOnTodayAllowed(engagement.Kind(row.Kind)) {
			t.Fatalf("needs-you: %+v", row)
		}
		if row.Kind == "reminder" && (!row.CanQuiet || row.QuietSuggestionAllowed) {
			t.Fatalf("reminder choice/suggestion: %+v", row)
		}
		if row.Kind == "goal_check_in" && row.Enabled {
			t.Fatalf("zero-send switch: %+v", row)
		}
	}
	if view.Summary.Sent != 2 || view.Summary.QuietSent != 1 || view.Summary.Sent != sent || view.Summary.OpenedOrActed != 2 {
		t.Fatalf("summary: %+v (rows sent=%d)", view.Summary, sent)
	}
	data, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private") {
		t.Fatalf("stats leaked source content: %s", data)
	}
}

func TestNotificationStatsQuietWithoutSendsAndExplicitReminderChoice(t *testing.T) {
	rig := newEngagementTestRig(t)
	app := &App{engagement: rig.svc}
	for _, kind := range []engagement.Kind{engagement.KindBriefReady, engagement.KindReminder} {
		view, err := app.SetEngagementKindQuiet(string(kind), true)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, row := range view.Kinds {
			if row.Kind == string(kind) {
				found = true
				if !row.Quiet || !row.CanQuiet || row.Tag != engagement.NotificationTagQuiet || row.Sent != 0 || len(row.Channels) != 0 {
					t.Fatalf("quiet switch without invented receipt: %+v", row)
				}
			}
		}
		if !found {
			t.Fatalf("missing %s", kind)
		}
	}
}

func TestNotificationSampleUsesMostRecentSentCopyVariant(t *testing.T) {
	rig := newEngagementTestRig(t)
	ctx := t.Context()
	// Source text and the marker value must never escape; only marker presence
	// selects the actual generic push-copy variant, without loading params in Go.
	for i, tc := range []struct{ id, params, want string }{
		{"default", `{"title":"private permission"}`, "decision_due"},
		{"thread", `{"_activity_thread":"private-id","title":"private outcome"}`, engagement.NotificationSampleActivityThread},
		{"default-again", `{"title":"private later permission"}`, "decision_due"},
	} {
		at := rig.clock.Add(time.Duration(i-3) * time.Hour)
		row, err := rig.store.CreateEngagementMoment(ctx, storage.EngagementMomentRow{ID: tc.id, Kind: "decision_due", DedupeKey: tc.id, Params: tc.params, CreatedAt: at.UnixMilli()})
		if err != nil {
			t.Fatal(err)
		}
		if err := rig.store.SetEngagementMomentState(ctx, row.ID, storage.EngagementStateSent, "sent", storage.EngagementMomentRow{SentAt: at.UnixMilli(), Channel: "push"}); err != nil {
			t.Fatal(err)
		}
		// A newer unsent notice never replaces the most recent delivered sample.
		if _, err := rig.store.CreateEngagementMoment(ctx, storage.EngagementMomentRow{ID: "unsent-" + tc.id, Kind: "decision_due", DedupeKey: "unsent-" + tc.id, Params: `{"_activity_thread":"unshown-private-id"}`, CreatedAt: rig.clock.UnixMilli()}); err != nil {
			t.Fatal(err)
		}
		view, err := engagementSettingsView(ctx, rig.svc)
		if err != nil {
			t.Fatal(err)
		}
		for _, kind := range view.Kinds {
			if kind.Kind == "decision_due" && kind.SampleCopyKey != tc.want {
				t.Fatalf("latest sent sample = %q, want %q", kind.SampleCopyKey, tc.want)
			}
		}
		data, err := json.Marshal(view)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "private") {
			t.Fatalf("sample leaked params: %s", data)
		}
	}
}
