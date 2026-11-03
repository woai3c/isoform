package main

import (
	"encoding/json"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/storage"
)

func TestMainChatMutedNeedsUserStaysWaiting(t *testing.T) {
	now := time.Now()
	for _, kind := range []string{"decision_due", "work_blocked", "commitment_slipping", "waiting_on_reply", "permission_fixture"} {
		t.Run(kind, func(t *testing.T) {
			card := mainChatProactive{MomentID: kind, Kind: kind, State: "pending", KindMuted: true, ExpiresAt: now.Add(time.Hour).UnixMilli()}
			if kind == "decision_due" || kind == "permission_fixture" {
				card.Permission = &mainChatWorkPermission{RequestID: "request", ApproveOptionID: "approve", DeclineOptionID: "decline"}
			}
			if got := mainChatProactiveRowClass(card, now); got != MainChatRowWaiting {
				t.Fatalf("muted needs-you card left Waiting on you: %s", got)
			}
			app := mainChatTestApp(t)
			id := app.postProactiveItem(card, kind)
			if id == "" {
				t.Fatal("missing fixture")
			}
			if err := app.SetMainChatHideRoutine(true); err != nil {
				t.Fatal(err)
			}
			overview, err := app.GetMainChatOverview()
			if err != nil || !overview.HideRoutine || overview.Waiting != 1 || overview.Routine != 0 {
				t.Fatalf("hidden routine swallowed needs-you card: %+v, %v", overview, err)
			}
			page, err := app.ListMainChatWaitingItems("", 50)
			if err != nil || len(page.Items) != 1 || page.Items[0].ID != id {
				t.Fatalf("needs-you card missing from waiting page: %+v, %v", page, err)
			}
			card.State = "acted"
			if got := mainChatProactiveRowClass(card, now); got != MainChatRowOther {
				t.Fatalf("resolved card is waiting/routine: %s", got)
			}
			card.State, card.ExpiresAt = "pending", now.Add(-time.Second).UnixMilli()
			if got := mainChatProactiveRowClass(card, now); got != MainChatRowOther {
				t.Fatalf("expired card is waiting/routine: %s", got)
			}
		})
	}
}

func TestEngagementMutedDirectReminderKeepsChatDelivery(t *testing.T) {
	for _, retry := range []bool{false, true} {
		name := "fresh"
		if retry {
			name = "retry"
		}
		t.Run(name, func(t *testing.T) {
			rig := newEngagementTestRig(t)
			app := &App{store: rig.store, engagement: rig.svc, eventBroadcaster: NewEventBroadcaster()}
			rig.svc.deliver.Report = app.postEngagementReport
			rig.svc.prefs.MutedUntil[engagement.KindReminder] = rig.clock.Add(time.Hour)
			row, err := rig.svc.Submit(t.Context(), engagement.Moment{Kind: engagement.KindReminder, SubjectID: name, Channels: []engagement.Channel{engagement.ChannelPush}, Params: map[string]string{"title": "Review the plan"}})
			if err != nil {
				t.Fatal(err)
			}
			if retry {
				err = rig.store.SetEngagementMomentState(t.Context(), row.ID, storage.EngagementStateSent, "chat report", storage.EngagementMomentRow{Channel: "push", SentAt: rig.clock.UnixMilli(), HoldUntil: rig.clock.UnixMilli()})
				if err != nil {
					t.Fatal(err)
				}
			}
			rig.svc.Pass(t.Context())
			rig.svc.Pass(t.Context())
			expectedPushes := 1
			if retry {
				expectedPushes = 0
			}
			if len(rig.pushed) != expectedPushes {
				t.Fatalf("external sends = %d, want %d", len(rig.pushed), expectedPushes)
			}
			cards := mainChatProactiveItems(t, app)
			if len(cards) != 1 || cards[0].Kind != string(engagement.KindReminder) {
				t.Fatalf("direct reminder lost its durable chat record: %+v", cards)
			}
			stored, err := rig.store.GetEngagementMoment(t.Context(), row.ID)
			if err != nil || stored == nil || !engagementReportedInChat(*stored) || stored.Channel != "push" || stored.SentAt != rig.clock.UnixMilli() {
				t.Fatalf("delivery changed or chat report was not acknowledged: %+v, %v", stored, err)
			}
			var projected mainChatProactive
			input := mainChatViewPayload(t, cards[0])
			if err := json.Unmarshal([]byte(app.projectMainChatEngagementPrefs(mainChatProactiveTool, input)), &projected); err != nil {
				t.Fatal(err)
			}
			if projected.KindMuted {
				t.Fatal("Direct reminder was marked muted by chat projection")
			}
		})
	}
}

func TestEngagementChatMuteStateMatchesPacing(t *testing.T) {
	now := time.Now()
	cases := []struct {
		state                                               string
		enabled, paused, temporary, wantDirect, wantPassive bool
	}{
		{state: "enabled", enabled: true},
		{state: "off", wantDirect: true, wantPassive: true},
		{state: "paused", enabled: true, paused: true, wantDirect: true, wantPassive: true},
		{state: "temporary", enabled: true, temporary: true, wantPassive: true},
	}
	for _, kind := range []engagement.Kind{engagement.KindReminder, engagement.KindMeetingPrep} {
		spec, _ := engagement.Spec(kind)
		for _, tc := range cases {
			t.Run(string(kind)+"/"+tc.state, func(t *testing.T) {
				prefs := engagement.Prefs{Enabled: map[engagement.Kind]bool{kind: tc.enabled}, MutedUntil: map[engagement.Kind]time.Time{}}
				if tc.temporary {
					prefs.MutedUntil[kind] = now.Add(time.Hour)
				}
				want := tc.wantPassive
				if spec.Direct {
					want = tc.wantDirect
				}
				if got := engagementChatKindMuted(spec, prefs, tc.paused, now); got != want {
					t.Fatalf("chat mute = %v, want %v", got, want)
				}
			})
		}
	}
}
