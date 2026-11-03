package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/storage"
)

func TestOfferFindMoreCachedCooldownAndScope(t *testing.T) {
	bridge := mobileTestOfferBridge(t)
	svc := bridge.app.topicsSvc()
	now := time.Now()
	svc.now = func() time.Time { return now }
	oldReady, oldDispatch := topicFindsProviderReady, topicFindsDispatch
	t.Cleanup(func() { topicFindsProviderReady, topicFindsDispatch = oldReady, oldDispatch })
	topicFindsProviderReady = func(*App) bool { t.Fatal("cached or cooldown find checked the model"); return false }
	topicFindsDispatch = func(*App, string) error { t.Fatal("cached or cooldown find called the model"); return nil }
	for _, cooldown := range []bool{false, true} {
		if _, err := svc.store.update(func(st *topicsStateFile) { st.Finds.RanOn = now.In(time.Local).Format("2006-01-02") }); err != nil {
			t.Fatal(err)
		}
		if cooldown {
			svc.lastStart = now
		}
		response := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeOffersRead), http.MethodPost, "/offers/find-more", "")
		if response.Code != http.StatusOK {
			t.Fatalf("%d: %s", response.Code, response.Body.String())
		}
		var view mobileOfferList
		if err := json.Unmarshal(mobileTestDecodeReply(t, response).Data, &view); err != nil {
			t.Fatal(err)
		}
		if len(view.Offers) == 0 {
			t.Fatal("cached offers missing")
		}
		if cooldown {
			until, err := time.Parse(time.RFC3339, view.NextAllowedAt)
			if err != nil || !until.After(now) {
				t.Fatalf("nextAllowedAt = %q, %v", view.NextAllowedAt, err)
			}
		} else if view.NextAllowedAt != "" {
			t.Fatal(view.NextAllowedAt)
		}
	}
	response := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeOffersDecide), http.MethodPost, "/offers/find-more", "")
	if response.Code != http.StatusForbidden {
		t.Fatalf("scope: %d", response.Code)
	}
}

func TestOfferFindMoreFreshBatchAndDispatchError(t *testing.T) {
	bridge := mobileTestOfferBridge(t)
	bridge.app.cronScheduler = NewCronScheduler(bridge.app.store, func(string, any) {}, func(string, string) {}, nil)
	// This route fixture starts with an empty scheduler; its separately seeded
	// run is evidence for a find, not a job to load into that scheduler.
	close(bridge.app.cronScheduler.loadedCh)
	svc := bridge.app.topicsSvc()
	seedFinds := svc.store.load().Finds
	svc.collect = func(at time.Time) topicInput {
		return topicInput{Now: at, Sessions: topicChatsOnThreeDays("Fix wails drag regions")}
	}
	if _, err := svc.store.update(func(st *topicsStateFile) { st.Finds.RanOn = "" }); err != nil {
		t.Fatal(err)
	}
	oldReady, oldDispatch := topicFindsProviderReady, topicFindsDispatch
	t.Cleanup(func() { topicFindsProviderReady, topicFindsDispatch = oldReady, oldDispatch })
	topicFindsProviderReady = func(*App) bool { return true }
	calls := 0
	fail := true
	topicFindsDispatch = func(*App, string) error {
		calls++
		if fail {
			return errors.New("dispatch failed")
		}
		_, err := svc.store.update(func(st *topicsStateFile) {
			st.Finds.RunID = seedFinds.RunID
			st.Finds.Sent = seedFinds.Sent
			st.Finds.RanAt = time.Now().Add(time.Second).Format(time.RFC3339)
			st.Finds.RanOn = time.Now().In(time.Local).Format("2006-01-02")
			st.Finds.Items[0].Title = "Fresh project tools"
		})
		return err
	}
	for _, want := range []int{http.StatusServiceUnavailable, http.StatusOK} {
		response := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeOffersRead), http.MethodPost, "/offers/find-more", "")
		if response.Code != want {
			t.Fatalf("want %d, got %d: %s", want, response.Code, response.Body.String())
		}
		if want == http.StatusOK {
			var view mobileOfferList
			if err := json.Unmarshal(mobileTestDecodeReply(t, response).Data, &view); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, offer := range view.Offers {
				found = found || offer.Title == "Fresh project tools"
			}
			if !found {
				t.Fatalf("fresh offer missing: %+v", view)
			}
		}
		fail = false
	}
	if calls != 2 {
		t.Fatalf("dispatch calls = %d", calls)
	}
}

func TestOfferFindMoreEmptyAndUnavailableStore(t *testing.T) {
	bridge := mobileTestOfferBridge(t)
	bridge.app.closedEyes.enabled.Store(true)
	response := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeOffersRead), http.MethodPost, "/offers/find-more", "")
	if response.Code != http.StatusOK {
		t.Fatalf("empty: %d %s", response.Code, response.Body.String())
	}
	var view mobileOfferList
	if err := json.Unmarshal(mobileTestDecodeReply(t, response).Data, &view); err != nil {
		t.Fatal(err)
	}
	if view.Offers == nil || len(view.Offers) != 0 {
		t.Fatalf("empty offers: %+v", view)
	}
	bridge.app.store = nil
	response = mobileTestChatCall(bridge, mobileTestDevice(mobileScopeOffersRead), http.MethodPost, "/offers/find-more", "")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable: %d", response.Code)
	}
}

func TestYourWeekLegacyProjection(t *testing.T) {
	app := &App{}
	for _, brief := range []string{"", "2026-10-23-evening"} {
		card := mainChatProactive{MomentID: "week", Kind: "weekly_recap", BriefID: brief, Target: mainChatTarget{Kind: "today", ID: brief}}
		app.projectProactiveVoiceItem(t.Context(), &card, "en")
		if (card.Kind == "thread_update_unavailable") != (brief == "") {
			t.Fatalf("brief %q: %+v", brief, card)
		}
	}
	if _, ok := app.proactiveItem(engagement.Moment{Kind: engagement.KindWeeklyRecap}, engagementText{}); ok {
		t.Fatal("legacy week admitted")
	}
	digest := mainChatProactive{MomentID: "digest", Kind: "digest", Items: []mainChatProactive{{MomentID: "old", Kind: "weekly_recap"}}}
	app.projectProactiveVoiceItem(t.Context(), &digest, "en")
	raw, err := json.Marshal(digest)
	if err != nil {
		t.Fatal(err)
	}
	if !mainChatWithdrawnThreadUpdate(string(raw)) {
		t.Fatalf("legacy digest visible: %s", raw)
	}
}

func TestNotificationUsefulPerKind(t *testing.T) {
	rig := newEngagementTestRig(t)
	empty, err := (&App{engagement: rig.svc}).GetEngagementSettings()
	if err != nil || empty.Summary.Useful != 0 {
		t.Fatalf("empty summary: %+v, %v", empty.Summary, err)
	}
	for _, kind := range []engagement.Kind{engagement.KindBriefReady, engagement.KindMeetingEnded, engagement.KindWeeklyRecap, engagement.KindReminder} {
		for _, state := range []string{storage.EngagementReceiptOpened, storage.EngagementReceiptActed} {
			id := string(kind) + state
			if _, err := rig.store.CreateEngagementMoment(t.Context(), storage.EngagementMomentRow{ID: id, Kind: string(kind), DedupeKey: id, CreatedAt: rig.clock.UnixMilli()}); err != nil {
				t.Fatal(err)
			}
			if err := rig.store.SetEngagementMomentState(t.Context(), id, storage.EngagementStateSent, "sent", storage.EngagementMomentRow{Channel: "desktop", SentAt: rig.clock.UnixMilli()}); err != nil {
				t.Fatal(err)
			}
			if err := rig.store.RecordEngagementReceipt(t.Context(), storage.EngagementReceiptRow{MomentID: id, Channel: "desktop", State: state, Action: "done", At: rig.clock.UnixMilli()}); err != nil {
				t.Fatal(err)
			}
		}
	}
	view, err := (&App{engagement: rig.svc}).GetEngagementSettings()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range view.Kinds {
		want := 0
		switch engagement.Kind(row.Kind) {
		case engagement.KindBriefReady, engagement.KindMeetingEnded, engagement.KindWeeklyRecap:
			want = 2
		case engagement.KindReminder:
			want = 1
		}
		if row.NotificationUseful != want {
			t.Fatalf("%s useful = %d, want %d", row.Kind, row.NotificationUseful, want)
		}
	}
	if view.Summary.Useful != 5 {
		t.Fatalf("summary: %+v", view.Summary)
	}
	stats, err := rig.store.EngagementStatsBetween(t.Context(), rig.clock.Add(-engagementStatsWindow), rig.clock)
	if err != nil {
		t.Fatal(err)
	}
	if stats[string(engagement.KindMeetingEnded)].NotificationUseful != 2 {
		t.Fatal("meeting notes opens not useful")
	}
}
