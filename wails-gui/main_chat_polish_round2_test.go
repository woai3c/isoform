package main

import (
	"testing"
	"time"

	"wails-gui/internal/engagement"
)

// Exercise the authorized HTTP route with the registry enabled: enrichment must
// classify the same projected payload as the computer, not the stored copy.
func assertMobileMainChatMatchesDesktop(t *testing.T, bridge *mobileBridge, session string, want map[string]MainChatRowClass) {
	t.Helper()
	desktop, err := bridge.app.LoadStreamItemsPage(session, -1, 200)
	if err != nil {
		t.Fatal(err)
	}
	desktopRows := make(map[string]StreamItemData, len(desktop.Items))
	for _, item := range desktop.Items {
		desktopRows[item.ID] = item
	}
	for _, query := range []string{"", "?after=0"} {
		bridge.app.mainChatWaiting.state = MainChatWaitingState{}
		phone, _ := mobileFieldsPage(t, bridge, "/chats/"+session+query)
		seen := make(map[string]bool)
		waiting := 0
		for id, class := range want {
			if desktopRows[id].RowClass != string(class) {
				t.Fatalf("desktop %s class = %q, want %q", id, desktopRows[id].RowClass, class)
			}
			if class == MainChatRowWaiting {
				waiting++
			}
		}
		for _, item := range phone.Items {
			class, ok := want[item.ID]
			if !ok {
				t.Fatalf("unexpected phone row %s", item.ID)
			}
			seen[item.ID] = true
			if item.RowClass != string(class) || item.LocalDay != desktopRows[item.ID].LocalDay {
				t.Errorf("phone %s%s class/day = %q/%q, computer = %q/%q", item.ID, query, item.RowClass, item.LocalDay, desktopRows[item.ID].RowClass, desktopRows[item.ID].LocalDay)
			}
		}
		if len(seen) != len(want) {
			t.Fatalf("phone lost rows: seen %v, want %v", seen, want)
		}
		if phone.WaitingCount != nil {
			t.Fatalf("phone waiting count = %d before resync, want omitted", *phone.WaitingCount)
		}
		bridge.app.mainChatWaiting.state = MainChatWaitingState{
			Ready: true, SessionID: session, Day: mainChatDayStart(time.Now()).Format("2006-01-02"), Count: waiting,
		}
		phone, _ = mobileFieldsPage(t, bridge, "/chats/"+session+query)
		if phone.WaitingCount == nil || *phone.WaitingCount != waiting {
			t.Fatalf("phone waiting count after resync = %v, want %d", phone.WaitingCount, waiting)
		}
	}
}

func TestMobileMainChatMutedRoutineMatchesDesktop(t *testing.T) {
	bridge, session, _ := mobileFieldsTopicBridge(t)
	app := bridge.app
	app.engagement = newEngagementService(app.store, nil, engagementDelivery{})
	card := mainChatRowTopicCard("meeting")
	card.Kind = "meeting_prep"
	id := app.postProactiveItem(card, "meeting")
	if id == "" {
		t.Fatal("missing meeting fixture")
	}
	assertMobileMainChatMatchesDesktop(t, bridge, session, map[string]MainChatRowClass{id: MainChatRowWaiting})
	if err := app.engagement.SetKindEnabled(t.Context(), engagement.KindMeetingPrep, false); err != nil {
		t.Fatal(err)
	}
	// Keep ordinary needs-you cards visible and the canonical terminal/expiry
	// controls out of Waiting even when display copy omits legacy expiry.
	needs := mainChatRowTopicCard("needs")
	needs.Kind = "decision_due"
	needsID := app.postProactiveItem(needs, "needs")
	if err := app.engagement.SetKindEnabled(t.Context(), engagement.KindDecisionDue, false); err != nil {
		t.Fatal(err)
	}
	expired := mainChatRowTopicCard("expired")
	expired.Kind, expired.ExpiresAt = "decision_due", time.Now().Add(-time.Hour).UnixMilli()
	expiredID := app.postProactiveItem(expired, "expired")
	acted := mainChatRowTopicCard("acted")
	acted.Kind, acted.State = "decision_due", "acted"
	actedID := app.postProactiveItem(acted, "acted")
	assertMobileMainChatMatchesDesktop(t, bridge, session, map[string]MainChatRowClass{
		id: MainChatRowRoutine, needsID: MainChatRowWaiting, expiredID: MainChatRowOther, actedID: MainChatRowOther,
	})
}

func TestMobileMainChatWithdrawnRoutineMatchesDesktop(t *testing.T) {
	bridge, session, _ := mobileFieldsTopicBridge(t)
	card := mainChatRowTopicCard("withdrawn")
	card.Kind, card.ActivityOutcomeSource = "decision_due", "missing-outcome"
	id := bridge.app.postProactiveItem(card, "withdrawn")
	if id == "" {
		t.Fatal("missing withdrawn fixture")
	}
	control := mainChatRowTopicCard("readable")
	control.Kind = "decision_due"
	controlID := bridge.app.postProactiveItem(control, "readable")
	assertMobileMainChatMatchesDesktop(t, bridge, session, map[string]MainChatRowClass{id: MainChatRowRoutine, controlID: MainChatRowWaiting})
	phone, raw := mobileFieldsPage(t, bridge, "/chats/"+session)
	for _, item := range phone.Items {
		if item.ID == id && (item.Proactive == nil || item.Proactive.Kind != "thread_update_unavailable" || len(item.Proactive.ActionIDs) != 0) {
			t.Fatalf("withdrawn card lost its safe projection: %+v, %s", item, raw)
		}
	}
}
