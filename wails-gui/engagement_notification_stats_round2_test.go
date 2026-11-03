package main

import (
	"encoding/json"
	"fmt"
	"testing"

	"wails-gui/internal/engagement"
	"wails-gui/storage"
)

func round2NotificationHistory(t *testing.T, rig *engagementTestRig, kind engagement.Kind, quiet, notifications, quietActed, notificationAttention int) {
	t.Helper()
	for i := 0; i < quiet; i++ {
		id := round1NotificationSent(t, rig, fmt.Sprintf("quiet-%d", i), kind, "today_quiet")
		if i < quietActed {
			if err := rig.store.RecordEngagementReceipt(t.Context(), storage.EngagementReceiptRow{MomentID: id, Channel: storage.EngagementCompletionChannel, State: storage.EngagementReceiptActed, Action: storage.EngagementCompletionAction, At: rig.clock.UnixMilli()}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for i := 0; i < notifications; i++ {
		id := round1NotificationSent(t, rig, fmt.Sprintf("notification-%d", i), kind, "desktop+push")
		if i < notificationAttention {
			round1NotificationOpenedAndActed(t, rig, id)
		}
	}
}

func TestNotificationRound2TagsIgnoreQuietHistoryAfterUnquiet(t *testing.T) {
	for _, tc := range []struct {
		name                                                    string
		kind                                                    engagement.Kind
		quiet, notifications, quietActed, notificationAttention int
		want                                                    engagement.NotificationTag
	}{
		{"quiet only", engagement.KindBriefReady, 20, 0, 0, 0, engagement.NotificationTagNone},
		{"one real notification stays rare", engagement.KindBriefReady, 20, 1, 0, 0, engagement.NotificationTagRare},
		{"quiet completions cannot hide real noise", engagement.KindBriefReady, 20, 20, 20, 0, engagement.NotificationTagNoisy},
		{"ten percent of interruptions", engagement.KindBriefReady, 20, 10, 0, 1, engagement.NotificationTagNone},
		{"under ten percent remains ignored", engagement.KindBriefReady, 20, 11, 0, 1, engagement.NotificationTagIgnored},
		{"needs you uses interruption completions", engagement.KindCommitmentSlipping, 20, 3, 20, 0, engagement.NotificationTagNeedsYou},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rig := newEngagementTestRig(t)
			app := &App{engagement: rig.svc}
			if _, err := app.SetEngagementKindQuiet(string(tc.kind), true); err != nil {
				t.Fatal(err)
			}
			round2NotificationHistory(t, rig, tc.kind, tc.quiet, tc.notifications, tc.quietActed, tc.notificationAttention)
			quietView, err := app.GetEngagementSettings()
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range quietView.Kinds {
				if row.Kind == string(tc.kind) && row.Tag != engagement.NotificationTagQuiet {
					t.Fatalf("current quiet mode must retain Quiet: %+v", row)
				}
			}
			view, err := app.SetEngagementKindQuiet(string(tc.kind), false)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range view.Kinds {
				if row.Kind != string(tc.kind) {
					continue
				}
				if row.Tag != tc.want {
					t.Fatalf("quiet history must not change interruption tag: got=%s want=%s row=%+v", row.Tag, tc.want, row)
				}
				// Read JSON so this unchanged guard compiles against the old API.
				raw, err := json.Marshal(row)
				if err != nil {
					t.Fatal(err)
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(raw, &fields); err != nil {
					t.Fatal(err)
				}
				for key, want := range map[string]int{"notificationSent": tc.notifications, "notificationOpenedOrActed": tc.notificationAttention, "notificationActed": tc.notificationAttention, "quietSent": tc.quiet, "quietOpenedOrActed": tc.quietActed} {
					var got int
					if err := json.Unmarshal(fields[key], &got); err != nil || got != want {
						t.Fatalf("separate %s: got=%s want=%d err=%v", key, fields[key], want, err)
					}
				}
				return
			}
			t.Fatal("missing active kind")
		})
	}
}

func TestNotificationRound2OverviewUsesInterruptionHistoryOnly(t *testing.T) {
	for _, tc := range []struct {
		name                             string
		quiet, notifications, quietActed int
		want                             bool
	}{
		{"quiet only", 30, 0, 0, false},
		{"below real noise threshold", 30, 19, 0, false},
		{"real noise with quiet history", 30, 20, 0, true},
		{"quiet attention cannot hide real noise", 30, 20, 30, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rig := newEngagementTestRig(t)
			app := &App{engagement: rig.svc}
			if _, err := app.SetEngagementKindQuiet("brief_ready", true); err != nil {
				t.Fatal(err)
			}
			round2NotificationHistory(t, rig, engagement.KindBriefReady, tc.quiet, tc.notifications, tc.quietActed, 0)
			view, err := app.SetEngagementKindQuiet("brief_ready", false)
			if err != nil {
				t.Fatal(err)
			}
			issues := noisyKindIssues(&view)
			if !tc.want {
				if len(issues) != 0 {
					t.Fatalf("quiet lines must not suggest quieting again: %+v", issues)
				}
				return
			}
			if len(issues) != 1 || issues[0].Params.Kind != "brief_ready" || issues[0].Params.Sent != tc.notifications || issues[0].Signature != "brief_ready|20" {
				t.Fatalf("hint count and signature must use actual interruptions: %+v", issues)
			}
		})
	}
}
