package main

import (
	"encoding/json"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/storage"
)

func round1NotificationSent(t *testing.T, rig *engagementTestRig, id string, kind engagement.Kind, channel string) string {
	t.Helper()
	at := rig.clock.Add(-time.Hour).UnixMilli()
	row, err := rig.store.CreateEngagementMoment(t.Context(), storage.EngagementMomentRow{ID: id, Kind: string(kind), DedupeKey: id, CreatedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	if err := rig.store.SetEngagementMomentState(t.Context(), row.ID, storage.EngagementStateSent, "sent", storage.EngagementMomentRow{Channel: channel, SentAt: at}); err != nil {
		t.Fatal(err)
	}
	return row.ID
}

func round1NotificationOpenedAndActed(t *testing.T, rig *engagementTestRig, id string) {
	t.Helper()
	for _, r := range []storage.EngagementReceiptRow{
		{MomentID: id, Channel: "push", State: storage.EngagementReceiptOpened, At: rig.clock.UnixMilli()},
		{MomentID: id, Channel: storage.EngagementCompletionChannel, State: storage.EngagementReceiptActed, Action: storage.EngagementCompletionAction, At: rig.clock.UnixMilli()},
	} {
		if err := rig.store.RecordEngagementReceipt(t.Context(), r); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNotificationRound1IgnoredUsesDistinctAttention(t *testing.T) {
	rig := newEngagementTestRig(t)
	seedOverviewEngagementSent(t, rig, engagement.KindWorkFinished, 10, false)
	id := round1NotificationSent(t, rig, "opened-and-acted", engagement.KindWorkFinished, "desktop+push")
	round1NotificationOpenedAndActed(t, rig, id)
	view, err := engagementSettingsView(t.Context(), rig.svc)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range view.Kinds {
		if row.Kind == "work_finished" {
			if row.Sent != 11 || row.Opened != 1 || row.Acted != 1 || row.Tag != engagement.NotificationTagIgnored || view.Summary.OpenedOrActed != 1 {
				t.Fatalf("one opened/completed moment out of eleven must remain Ignored: row=%+v summary=%+v", row, view.Summary)
			}
			return
		}
	}
	t.Fatal("missing work_finished")
}

func TestNotificationRound1QuietChoiceIsSeparateFromSuggestions(t *testing.T) {
	for _, kind := range []engagement.Kind{engagement.KindReminder, engagement.KindCommitmentSlipping, engagement.KindWaitingOnReply, engagement.KindWorkBlocked, engagement.KindDecisionDue, engagement.KindBriefReady} {
		t.Run(string(kind), func(t *testing.T) {
			rig := newEngagementTestRig(t)
			app := &App{engagement: rig.svc}
			seedOverviewEngagementSent(t, rig, kind, 20, false)
			view, err := app.GetEngagementSettings()
			if err != nil {
				t.Fatal(err)
			}
			var found bool
			for _, row := range view.Kinds {
				if row.Kind != string(kind) {
					continue
				}
				found = true
				if row.CanQuiet != (kind != engagement.KindDecisionDue) {
					t.Errorf("explicit quiet choice: %+v", row)
				}
				if row.QuietSuggestionAllowed != (kind == engagement.KindBriefReady) {
					t.Errorf("quiet suggestion policy: %+v", row)
				}
				if kind == engagement.KindDecisionDue && row.Tag != engagement.NotificationTagNeedsYou {
					t.Errorf("decision must be Needs you ahead of Noisy/Ignored: %+v", row)
				}
			}
			if !found {
				t.Fatal("missing kind")
			}
			issues := noisyKindIssues(&view)
			if (len(issues) == 1) != (kind == engagement.KindBriefReady) {
				t.Errorf("only a suggestion-allowed kind may get an Overview quiet fix: %+v", issues)
			}
			if kind != engagement.KindDecisionDue {
				if _, err := app.SetEngagementKindQuiet(string(kind), true); err != nil {
					t.Fatalf("explicit user choice refused: %v", err)
				}
			}
		})
	}
}

func TestNotificationRound1RetiredKindsStayOutAndZeroSendSwitchesRemain(t *testing.T) {
	rig := newEngagementTestRig(t)
	for _, kind := range engagementSilentKinds {
		seedOverviewEngagementSent(t, rig, kind, 20, false)
	}
	if err := rig.svc.SetKindEnabled(t.Context(), engagement.KindGoalCheckIn, false); err != nil {
		t.Fatal(err)
	}
	view, err := engagementSettingsView(t.Context(), rig.svc)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Kinds) != len(engagement.AllKinds())-len(engagementSilentKinds) {
		t.Fatalf("retired kinds must stay absent: %d", len(view.Kinds))
	}
	found := false
	for _, row := range view.Kinds {
		if engagementKindSilent(engagement.Kind(row.Kind)) {
			t.Fatalf("dead switch returned: %+v", row)
		}
		if row.Kind == "goal_check_in" {
			found = true
			if row.Sent != 0 || row.Enabled {
				t.Fatalf("zero-send switch changed: %+v", row)
			}
		}
	}
	if !found {
		t.Fatal("zero-send active kind disappeared")
	}
}

func TestNotificationRound1SummarySeparatesQuietReceipts(t *testing.T) {
	rig := newEngagementTestRig(t)
	notice := round1NotificationSent(t, rig, "notice", engagement.KindBriefReady, "desktop+push")
	quiet := round1NotificationSent(t, rig, "quiet", engagement.KindBriefReady, "today_quiet")
	round1NotificationSent(t, rig, "quiet-unopened", engagement.KindBriefReady, "today_quiet")
	mixed := round1NotificationSent(t, rig, "mixed", engagement.KindBriefReady, "push+today_quiet")
	for _, id := range []string{notice, quiet, mixed} {
		round1NotificationOpenedAndActed(t, rig, id)
	}
	// Historical buckets follow receipts, even after the user changes the mode.
	for _, nowQuiet := range []bool{false, true} {
		view, err := (&App{engagement: rig.svc}).SetEngagementKindQuiet("brief_ready", nowQuiet)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(view.Summary)
		if err != nil {
			t.Fatal(err)
		}
		var summary map[string]int
		if err := json.Unmarshal(raw, &summary); err != nil {
			t.Fatal(err)
		}
		if summary["sent"] != 2 || summary["openedOrActed"] != 2 || summary["quietSent"] != 3 || summary["quietOpenedOrActed"] != 2 {
			t.Fatalf("notifications and quiet deliveries must be separate, once per notice in each receipt bucket (current quiet=%v): %s", nowQuiet, raw)
		}
	}
}
