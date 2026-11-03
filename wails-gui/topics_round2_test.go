package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"wails-gui/storage"

	"wails-gui/internal/pii"
)

func assertTopicBarriersAvailable(t *testing.T, a *App) {
	t.Helper()
	for _, lock := range []struct {
		name   string
		try    func() bool
		unlock func()
	}{
		{"Closed Eyes", a.closedEyes.mu.TryLock, a.closedEyes.mu.Unlock},
		{"memory mutation", memoryMutationMu.TryLock, memoryMutationMu.Unlock},
		{"memory publication", memoryFileLocks.publication.TryLock, memoryFileLocks.publication.Unlock},
		{"sensitive apps", pii.TryLockSensitiveAppsForPublication, pii.UnlockSensitiveAppsForPublication},
	} {
		if !lock.try() {
			t.Fatalf("collection holds %s", lock.name)
		}
		lock.unlock()
	}
}

func TestTopicPageCollectsOutsideBarriersAndRejectsLateWithdrawal(t *testing.T) {
	a, _, _ := registryChatTestApp(t)
	views, err := collectAndReadTopicViewsForTest(a)
	if err != nil || len(views) != 1 {
		t.Fatalf("views: %+v %v", views, err)
	}
	a.topicsSvc().invalidateChatSources() // Exercise collection as well as the publication gate.
	collections := 0
	a.todayChatCollectionObserver = func() { collections++; assertTopicBarriersAvailable(t, a) }
	a.topicPageBeforePublish = func() {
		assertTopicBarriersAvailable(t, a)
		if err := a.DeleteActivity("atlas"); err != nil {
			t.Fatal(err)
		}
	}
	if page, err := a.ListMainChatTopicItems(views[0].ID, "", 50); err == nil || len(page.Items) != 0 {
		t.Fatalf("withdrawn page published: %+v %v", page, err)
	}
	if collections != 1 {
		t.Fatalf("page collection count=%d", collections)
	}
}

func TestTopicRegistrySidebarSharesCollectionPerRevision(t *testing.T) {
	a, _, _ := registryChatTestApp(t)
	collections := 0
	a.todayChatCollectionObserver = func() { collections++ }
	views, err := collectAndReadTopicViewsForTest(a)
	if err != nil || len(views) != 1 {
		t.Fatalf("views: %+v %v", views, err)
	}
	initial := collections
	if err := a.PinTopicToSidebar(views[0].ID, true); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	for i := 0; i < 10; i++ {
		if topics, err := a.ListSidebarTopics(); err != nil || len(topics) != 1 {
			t.Fatalf("sidebar: %+v %v", topics, err)
		}
	}
	t.Logf("initial collection=%d, 10 unchanged sidebar reads=%s", collections, time.Since(started))
	if initial == 0 || collections != initial {
		t.Fatalf("unchanged sidebar recollected: initial=%d final=%d", initial, collections)
	}
	if _, err := a.activity.store.db.Exec(`UPDATE activities SET latest_change='New release ready' WHERE id='atlas'`); err != nil {
		t.Fatal(err)
	}
	stale, err := a.ListSidebarTopics()
	if err != nil || len(stale) != 1 || stale[0].Latest == nil || stale[0].Latest.Text == "New release ready" {
		t.Fatalf("source revision changed the persisted read: %+v %v", stale, err)
	}
	if _, err := collectAndReadTopicViewsForTest(a); err != nil {
		t.Fatal(err)
	}
	if collections != initial+1 {
		t.Fatalf("explicit build collection count: initial=%d final=%d", initial, collections)
	}
}

func TestTopicRegistryGoalBudgetKeepsNewestAndOpaqueDeferredIdentity(t *testing.T) {
	bridge, store, now := mobileTestGoalBridge(t)
	a := bridge.app
	t.Setenv(topicsStateDirEnvVar, t.TempDir())
	configureTopicRegistryTest(a, now)
	first := mobileTestPurpose(t, store, "old-goal", PurposeKindGoal, "Old work topic", false, func(in *CreatePurposeOpenLoopInput) {
		in.OccurredAt = now.Add(-time.Hour).UnixMilli()
		for i := range in.Evidence {
			in.Evidence[i].SourceObservedAt = in.OccurredAt
		}
	})
	before, err := collectAndReadTopicViewsForTest(a)
	if err != nil || len(before) != 1 {
		t.Fatalf("before: %+v %v", before, err)
	}
	id := before[0].ID
	for i := 0; i < topicRegistryGoalLimit+1; i++ {
		mobileTestPurpose(t, store, fmt.Sprintf("goal-%02d", i), PurposeKindGoal, fmt.Sprintf("Work %02d", i), false, func(in *CreatePurposeOpenLoopInput) {
			in.OccurredAt = now.Add(-5*time.Minute + time.Duration(i)*time.Second).UnixMilli()
			for i := range in.Evidence {
				in.Evidence[i].SourceObservedAt = in.OccurredAt
			}
		})
	}
	views, err := collectAndReadTopicViewsForTest(a)
	if err != nil || len(views) == 0 || !views[0].Truncated {
		t.Fatalf("heavy goals: %+v %v", views, err)
	}
	goals, deferred, truncated, err := a.topicRegistryGoalsLimited(context.Background(), now, nil)
	if err != nil || len(goals) != topicRegistryGoalLimit || !truncated || !deferred[first.OpenLoop.ID] {
		t.Fatalf("goal cap: %d %+v %v %v", len(goals), deferred, truncated, err)
	}
	for _, p := range a.topicsSvc().store.load().Projects {
		if p.ID == id {
			if !p.Deferred || p.Name != "" || len(p.MemberKeys) != 0 || !topicStringIn(p.GoalIDs, first.OpenLoop.ID) {
				t.Fatalf("deferred identity: %+v", p)
			}
			return
		}
	}
	t.Fatal("truncation discarded durable reservation")
}

func TestTopicDeleteActivityScrubsFileBeforeReturn(t *testing.T) {
	a, _, _ := registryChatTestApp(t)
	if _, err := collectAndReadTopicViewsForTest(a); err != nil {
		t.Fatal(err)
	}
	if err := a.DeleteActivity("atlas"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(a.topicsSvc().store.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"Atlas", "obj:atlas", "Atlas release ready"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("deleted topic content retained: %s", raw)
		}
	}
}

func TestTopicMarkdownWithdrawalScrubsDerivedGoalBeforeReturn(t *testing.T) {
	for _, operation := range []string{"forget", "delete_memory", "delete_knowledge", "forget_colon", "delete_memory_colon", "delete_knowledge_colon"} {
		t.Run(operation, func(t *testing.T) {
			colon := strings.HasSuffix(operation, "_colon")
			operation := strings.TrimSuffix(operation, "_colon")
			a, dir := newForgetTestApp(t)
			mainStore, err := storage.New(filepath.Join(t.TempDir(), "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { mainStore.Close() })
			a.store = mainStore
			t.Setenv(topicsStateDirEnvVar, t.TempDir())
			activity, err := NewActivityStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { activity.Close() })
			a.activity = &activityMonitorState{store: activity}
			now := time.Now()
			configureTopicRegistryTest(a, now)
			content := "# Private Atlas\n\nAn internal release plan"
			id := "private-atlas"
			if colon {
				id = "private:atlas"
			}
			kind := "memory_markdown"
			if operation == "delete_knowledge" {
				dir, err = knowledgeDir()
				if err != nil {
					t.Fatal(err)
				}
				kind = "knowledge_markdown"
			}
			if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			goal := mobileTestPurpose(t, NewPurposeMapStore(activity.db), "private-atlas", PurposeKindGoal, "Private Atlas", false, func(in *CreatePurposeOpenLoopInput) {
				in.Evidence[0].SourceKind = kind
				in.Evidence[0].SourceID = id
				in.Evidence[0].SourceHash = agencyPayloadHash([]byte(content))
			})
			var views []TopicView
			if colon {
				// Existing source admission conservatively rejects colon-bearing
				// evidence IDs. Seed an older persisted derivative to exercise
				// immediate erasure without requiring a new registry admission.
				_, err = a.topicsSvc().store.update(func(st *topicsStateFile) {
					st.Projects = []topicProject{{ID: "project-private", Name: goal.Current.Title, GoalIDs: []string{goal.OpenLoop.ID}}}
					st.SidebarPins = map[string]bool{"project-private": true}
				})
				views = []TopicView{{ID: "project-private", Name: goal.Current.Title}}
			} else {
				views, err = collectAndReadTopicViewsForTest(a)
			}
			if err != nil || len(views) != 1 || views[0].Name != goal.Current.Title {
				t.Fatalf("before: %+v %v", views, err)
			}
			if !colon {
				if err := a.PinTopicToSidebar(views[0].ID, true); err != nil {
					t.Fatal(err)
				}
			}
			// Withdrawal must work under Closed Eyes and without a subsequent read.
			a.closedEyes.enabled.Store(true)
			switch operation {
			case "forget":
				preview, previewErr := a.GetMemoryForgetPreview(id)
				if previewErr != nil {
					t.Fatal(previewErr)
				}
				err = a.ForgetMemoryEntry(id, preview.ConfirmationToken)
			case "delete_memory":
				err = a.DeleteMemoryEntry(id)
			case "delete_knowledge":
				err = a.DeleteKnowledgeEntry(id)
			}
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(a.topicsSvc().store.path)
			if err != nil {
				t.Fatal(err)
			}
			for _, text := range []string{"Private Atlas", goal.OpenLoop.ID} {
				if strings.Contains(string(raw), text) {
					t.Fatalf("withdrawn derivative retained: %s", raw)
				}
			}
			if !a.topicsSvc().store.load().SidebarPins[views[0].ID] {
				t.Fatal("scrub discarded independent sidebar choice")
			}
		})
	}
}

func TestTopicGoalForgetScrubsWithoutAReadAndKeepsIndependentProject(t *testing.T) {
	a, _, _ := registryChatTestApp(t)
	goal := mobileTestPurpose(t, NewPurposeMapStore(a.activity.store.db), "to-forget", PurposeKindGoal, "Discarded launch", false, nil)
	if _, err := collectAndReadTopicViewsForTest(a); err != nil {
		t.Fatal(err)
	}
	goalTestMove(t, a, goal.OpenLoop.ID, goal.Current.ID, userPurposeForget, "topic-forget")
	raw, err := os.ReadFile(a.topicsSvc().store.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "Discarded launch") || strings.Contains(string(raw), goal.OpenLoop.ID) {
		t.Fatalf("forgotten goal retained: %s", raw)
	}
	if !strings.Contains(string(raw), "Atlas") {
		t.Fatal("forget scrubbed independent project")
	}
}

func TestTopicComposerDeletionScrubsFileBeforeReturn(t *testing.T) {
	for _, operation := range []string{"purge", "merge"} {
		t.Run(operation, func(t *testing.T) {
			a, _, now := registryChatTestApp(t)
			if _, err := collectAndReadTopicViewsForTest(a); err != nil {
				t.Fatal(err)
			}
			epoch := a.topicsSvc().chatCacheEpoch
			triager := &ActivityTriager{app: a}
			switch operation {
			case "purge":
				if _, err := a.activity.store.db.Exec(`UPDATE activity_timeline_groups SET active_ms=1 WHERE id='run-atlas'`); err != nil {
					t.Fatal(err)
				}
				if n := triager.purgeTrivialActivities(a.activity.store, now.UnixMilli()); n != 1 {
					t.Fatalf("purged=%d", n)
				}
			case "merge":
				seedThreadMoment(t, a, threadFixtureMoment{id: "target", title: "Independent work", app: "Code", start: now.Add(-3 * time.Hour), end: now.Add(-2 * time.Hour)})
				if err := triager.mergeBuckets(a.activity.store, "atlas", "target", now.UnixMilli()); err != nil {
					t.Fatal(err)
				}
			}
			raw, err := os.ReadFile(a.topicsSvc().store.path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "Atlas") || strings.Contains(string(raw), "obj:atlas") {
				t.Fatalf("deleted derivative remains: %s", raw)
			}
			if a.topicsSvc().chatCacheEpoch <= epoch {
				t.Fatal("source cache not invalidated")
			}
		})
	}
}

func TestTopicHistoryBudgetSelectsNewestExistingRows(t *testing.T) {
	a, _, now := registryChatTestApp(t)
	base, err := a.activity.store.GetActivityByID("atlas")
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{"missing-1", "missing-2"}
	for i := 0; i < topicRegistryHistoryLimit+2; i++ {
		row := *base
		// Lexicographic IDs run in the opposite direction to timestamps.
		row.ID = fmt.Sprintf("historical-%04d", topicRegistryHistoryLimit+2-i)
		row.EndMs = now.Add(-30 * 24 * time.Hour).Add(time.Duration(i) * time.Minute).UnixMilli()
		row.StartMs = row.EndMs - 1000
		if err := a.activity.store.InsertActivity(row); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, row.ID)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	rows, deferred, truncated, err := a.topicRegistryActivitiesLimited(ctx, now, []topicProject{{ActivityIDs: ids}})
	if err != nil || !truncated || len(rows) != topicRegistryHistoryLimit+1 {
		t.Fatalf("history: rows=%d deferred=%v truncated=%v err=%v", len(rows), deferred, truncated, err)
	}
	if len(deferred) != 2 || !deferred["historical-0258"] || !deferred["historical-0257"] || deferred["missing-1"] {
		t.Fatalf("wrong historical selection: %v", deferred)
	}
}

func TestTopicHistoricalPinnedPanelUsesRegistrySourceClosure(t *testing.T) {
	a, _, now := registryChatTestApp(t)
	goal := mobileTestPurpose(t, NewPurposeMapStore(a.activity.store.db), "older-release", PurposeKindGoal, "Atlas handoff", false, func(in *CreatePurposeOpenLoopInput) {
		in.Evidence[0].SourceKind = "activity"
		in.Evidence[0].SourceID = "atlas"
	})
	views, err := collectAndReadTopicViewsForTest(a)
	if err != nil || len(views) == 0 {
		t.Fatalf("before: %+v %v", views, err)
	}
	var id string
	for _, project := range a.topicsSvc().store.load().Projects {
		if topicStringIn(project.ActivityIDs, "atlas") {
			id = project.ID
		}
	}
	if id == "" {
		t.Fatal("missing project")
	}
	if err := a.PinTopicToSidebar(id, true); err != nil {
		t.Fatal(err)
	}
	base, err := a.activity.store.GetActivityByID("atlas")
	if err != nil {
		t.Fatal(err)
	}
	base.EndMs = now.Add(-20 * 24 * time.Hour).UnixMilli()
	base.StartMs = base.EndMs - 1000
	if err := a.activity.store.UpdateActivity(*base); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < topicRegistryActivityLimit+1; i++ {
		row := *base
		row.ID = fmt.Sprintf("recent-%04d", i)
		row.EndMs = now.Add(-time.Minute).UnixMilli()
		row.StartMs = row.EndMs - 1000
		row.Entities = "[]"
		if err := a.activity.store.InsertActivity(row); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := collectAndReadTopicViewsForTest(a); err != nil {
		t.Fatalf("rebuild historical source closure: %v", err)
	}
	if sidebar, err := a.ListSidebarTopics(); err != nil || len(sidebar) != 1 {
		t.Fatalf("sidebar: %+v %v", sidebar, err)
	}
	page, err := a.ListMainChatTopicItems(id, "", 50)
	if err != nil {
		t.Fatalf("historical panel (%s): %v", goal.OpenLoop.ID, err)
	}
	_ = page // Old events may lie outside the panel window; admission must succeed.
	sources, err := a.topicPageSources(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, purpose := range sources.Purposes {
		found = found || purpose.OpenLoop.ID == goal.OpenLoop.ID
	}
	if !found {
		t.Fatal("page omitted historical activity-backed goal closure")
	}
}

func TestTopicHistoricalMemoryForgetScrubsWithoutRemovingCorrection(t *testing.T) {
	a, _ := newForgetTestApp(t)
	mainStore, err := storage.New(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mainStore.Close() })
	a.store = mainStore
	activity := newTestStore(t)
	a.activity = &activityMonitorState{store: activity}
	t.Setenv(topicsStateDirEnvVar, t.TempDir())
	configureTopicRegistryTest(a, time.Now())
	if _, err := a.saveMemoryEntry("source", "# Source\n\nOriginal private observation.", false); err != nil {
		t.Fatal(err)
	}
	old, err := a.saveMemoryEntry("corrected", "# Former synthesis\n\n- **MergedFrom**: source\n\nOld derived observation.", false)
	if err != nil {
		t.Fatal(err)
	}
	goal := mobileTestPurpose(t, NewPurposeMapStore(activity.db), "former-synthesis", PurposeKindGoal, "Former confidential work", false, func(in *CreatePurposeOpenLoopInput) {
		in.Evidence[0].SourceKind = "memory_markdown"
		in.Evidence[0].SourceID = old.ID
		in.Evidence[0].SourceHash = agencyPayloadHash([]byte(old.Content))
	})
	views, err := collectAndReadTopicViewsForTest(a)
	if err != nil || len(views) != 1 {
		t.Fatalf("before: %+v %v", views, err)
	}
	oldState := a.topicsSvc().store.load()
	corrected, err := a.SaveMemoryEntry(old.ID, "# My correction\n\nAn independent current note.")
	if err != nil {
		t.Fatal(err)
	}
	currentGoal := mobileTestPurpose(t, NewPurposeMapStore(activity.db), "current-correction", PurposeKindGoal, "Independent current work", false, func(in *CreatePurposeOpenLoopInput) {
		in.Evidence[0].SourceKind = "memory_markdown"
		in.Evidence[0].SourceID = corrected.ID
		in.Evidence[0].SourceHash = agencyPayloadHash([]byte(strings.TrimSpace(corrected.Content)))
	})
	if currentViews, err := collectAndReadTopicViewsForTest(a); err != nil || len(currentViews) != 1 || currentViews[0].Name != currentGoal.Current.Title {
		t.Fatalf("current projection: %+v %v", currentViews, err)
	}
	// Retain the previously saved derivative to test erasure and the independent
	// corrected-version control in the same immediate, no-refresh withdrawal.
	if _, err := a.topicsSvc().store.update(func(st *topicsStateFile) { st.Projects = append(st.Projects, oldState.Projects...) }); err != nil {
		t.Fatal(err)
	}
	preview, err := a.GetMemoryForgetPreview("source")
	if err != nil {
		t.Fatal(err)
	}
	if preview.HistoryCount == 0 {
		t.Fatalf("not a historical forget: %+v", preview)
	}
	if err := a.ForgetMemoryEntry("source", preview.ConfirmationToken); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(a.topicsSvc().store.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), goal.Current.Title) || strings.Contains(string(raw), goal.OpenLoop.ID) {
		t.Fatalf("old historical derivative retained: %s", raw)
	}
	if !strings.Contains(string(raw), currentGoal.Current.Title) {
		t.Fatalf("independent corrected goal scrubbed: %s", raw)
	}
	live, err := a.GetMemoryEntry(old.ID)
	if err != nil || live.Content != corrected.Content {
		t.Fatalf("correction lost: %+v %v", live, err)
	}
}

func TestTopicPageRetainsFencesThroughResponseAssembly(t *testing.T) {
	a, _, _ := registryChatTestApp(t)
	views, err := collectAndReadTopicViewsForTest(a)
	if err != nil || len(views) != 1 {
		t.Fatalf("views: %+v %v", views, err)
	}
	called := false
	a.topicPageAfterValidate = func() {
		called = true
		if a.closedEyes.mu.TryLock() {
			a.closedEyes.mu.Unlock()
			t.Fatal("page released Closed Eyes fence before assembly")
		}
		if a.topicsSvc().store.publication.TryLock() {
			a.topicsSvc().store.publication.Unlock()
			t.Fatal("page released topic publication fence before assembly")
		}
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
		defer cancel()
		if _, err := a.activity.store.db.ExecContext(ctx, `UPDATE activities SET title='late edit' WHERE id='atlas'`); err == nil {
			t.Fatal("page released activity database fence before assembly")
		}
	}
	if _, err := a.ListMainChatTopicItems(views[0].ID, "", 50); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("assembly guard not exercised")
	}
	assertTopicBarriersAvailable(t, a)
}

func TestTopicWithdrawalFailuresAreRecoverable(t *testing.T) {
	for _, failure := range []string{"topic_write", "source_delete"} {
		t.Run(failure, func(t *testing.T) {
			a, _, _ := registryChatTestApp(t)
			if _, err := collectAndReadTopicViewsForTest(a); err != nil {
				t.Fatal(err)
			}
			store := a.topicsSvc().store
			before, err := os.ReadFile(store.path)
			if err != nil {
				t.Fatal(err)
			}
			switch failure {
			case "topic_write":
				if err := os.Mkdir(store.path+".tmp", 0700); err != nil {
					t.Fatal(err)
				}
			case "source_delete":
				if _, err := a.activity.store.db.Exec(`CREATE TRIGGER reject_topic_delete BEFORE DELETE ON activities BEGIN SELECT RAISE(ABORT,'fixture refuses deletion'); END`); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.DeleteActivity("atlas"); err == nil {
				t.Fatal("failed deletion reported success")
			}
			row, err := a.activity.store.GetActivityByID("atlas")
			if err != nil || row == nil {
				t.Fatalf("failed sanitization removed source: %+v %v", row, err)
			}
			after, err := os.ReadFile(store.path)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatalf("failed deletion lost prior projection:\nbefore=%s\nafter=%s", before, after)
			}
		})
	}
}

func TestTopicWithdrawalInterruptedFinalizationRecoversOpaqueIdentity(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(fmt.Sprintf("source_committed_%t", committed), func(t *testing.T) {
			a, _, _ := registryChatTestApp(t)
			views, err := collectAndReadTopicViewsForTest(a)
			if err != nil || len(views) != 1 {
				t.Fatalf("views: %+v %v", views, err)
			}
			w, err := a.topicWithdrawalForSource("activity", []string{"atlas"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			finish, err := a.beginTopicWithdrawal(w)
			if err != nil {
				t.Fatal(err)
			}
			if committed {
				if err := a.activity.store.DeleteActivity("atlas"); err != nil {
					t.Fatal(err)
				}
			}
			store := a.topicsSvc().store
			if err := os.Mkdir(store.path+".tmp", 0700); err != nil {
				t.Fatal(err)
			}
			if err := finish(committed); err == nil {
				t.Fatal("injected final-write failure reported success")
			}
			raw, err := os.ReadFile(store.path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "Atlas") || strings.Contains(string(raw), "obj:atlas") {
				t.Fatalf("interrupted cleanup retained content: %s", raw)
			}
			if err := os.Remove(store.path + ".tmp"); err != nil {
				t.Fatal(err)
			}
			a.topics = newTopicsService(a) // Reload from the sanitized on-disk reservation.
			configureTopicRegistryTest(a, time.Now())
			after, err := collectAndReadTopicViewsForTest(a)
			if err != nil {
				t.Fatal(err)
			}
			if committed && len(after) != 0 {
				t.Fatalf("deleted source revived: %+v", after)
			}
			if !committed && (len(after) != 1 || after[0].ID != views[0].ID) {
				t.Fatalf("readable source lost durable identity: %+v", after)
			}
		})
	}
}
