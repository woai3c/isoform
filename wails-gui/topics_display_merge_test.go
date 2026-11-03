package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTopicMergeCadenceKeepsDisplaySourceAndWithdrawal(t *testing.T) {
	for _, language := range []string{"en", "zh-CN"} {
		for _, worker := range []bool{false, true} {
			t.Run(language+map[bool]string{false: "/sync", true: "/worker"}[worker], func(t *testing.T) {
				a, _, _ := registryChatTestApp(t)
				if err := a.store.SetUIState(t.Context(), uiLanguageStateKey, language); err != nil {
					t.Fatal(err)
				}
				const source = "Review [REDACTED:account_number]sed before release."
				if _, err := a.activity.store.db.Exec(`UPDATE activities SET latest_change=? WHERE id='atlas'`, source); err != nil {
					t.Fatal(err)
				}
				if _, err := collectAndReadTopicViewsForTest(a); err != nil {
					t.Fatal(err)
				}
				seed, err := a.GetTodayTopics()
				if err != nil || len(seed.Topics) != 1 {
					t.Fatalf("seed: %+v %v", seed, err)
				}
				id := seed.Topics[0].ID
				if err := a.PinTopicToSidebar(id, true); err != nil {
					t.Fatal(err)
				}
				svc := a.topicsSvc()
				snapshot := svc.snapshots.Current()
				foundActivityRef := false
				for _, entry := range snapshot.Topics {
					if entry.Topic.ID != id {
						continue
					}
					for _, ref := range entry.Refs {
						foundActivityRef = foundActivityRef || ref.Kind == topicRefSource && ref.Value == "activity:atlas"
					}
				}
				if !foundActivityRef {
					t.Fatal("published topic snapshot omitted source activity:atlas")
				}
				keys, ok := svc.TopicKeys(id)
				if !ok || !reflect.DeepEqual(keys.ThreadKeys, []string{"obj:atlas"}) {
					t.Fatalf("original keys: %+v %t", keys, ok)
				}
				before, err := json.Marshal(svc.loadRegistryCache().result.views)
				if err != nil || !strings.Contains(string(before), "[REDACTED:account_number]sed") {
					t.Fatalf("raw source fixture: %s %v", before, err)
				}
				var builds atomic.Int32
				svc.registryBeforeSnapshot = func() { builds.Add(1) }
				if worker {
					a.ctx = t.Context()
					svc.startRegistryWorker(t.Context())
					t.Cleanup(svc.stopRegistryWorker)
				}
				want := "an account number"
				if language == "zh-CN" {
					want = "一个账号"
				}
				check := func(views []TopicView, err error) {
					t.Helper()
					if err != nil || len(views) != 1 || views[0].ID != id || views[0].Latest == nil {
						t.Fatalf("display identity: %+v %v", views, err)
					}
					text := views[0].Latest.Text
					if !strings.Contains(text, want) || strings.Contains(text, "[REDACTED") || strings.Contains(text, "sed") {
						t.Fatalf("unsafe %s display: %q", language, text)
					}
				}
				for i := 0; i < 3; i++ {
					view, err := a.GetTodayTopics()
					check(view.Topics, err)
					if view.RefreshIntervalMS != svc.store.load().backgroundRefreshInterval().Milliseconds() || view.Clear || view.Refreshing {
						t.Fatalf("cadence/settled state: %+v", view)
					}
					snapshotViews, _, _ := svc.snapshotRead()
					check(displayModelCopy(snapshotViews, language), nil)
					check(a.ListSidebarTopics())
				}
				after, err := json.Marshal(svc.loadRegistryCache().result.views)
				if err != nil || string(before) != string(after) {
					t.Fatal("display reads changed source publication")
				}
				if got, ok := svc.TopicKeys(id); !ok || !reflect.DeepEqual(got, keys) {
					t.Fatalf("display changed source keys: %+v", got)
				}
				if err := a.deleteActivityWithPrivacyWithdrawal(a.activity.store, "atlas"); err != nil {
					t.Fatal(err)
				}
				withdrawn, err := a.GetTodayTopics()
				if err != nil || len(withdrawn.Topics) != 0 || !withdrawn.Clear {
					t.Fatalf("withdrawal waited for cadence: %+v %v", withdrawn, err)
				}
				if views, err := a.ListSidebarTopics(); err != nil || len(views) != 0 {
					t.Fatalf("withdrawn sidebar: %+v %v", views, err)
				}
				if worker && builds.Load() != 0 {
					t.Fatalf("display or withdrawal forced %d full builds", builds.Load())
				}
			})
		}
	}
}
