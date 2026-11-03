package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"wails-gui/internal/interests"
	"wails-gui/internal/pii"
	"wails-gui/storage"
)

// Real SQLite provenance over six weeks, with a stored interest to exercise
// the production cold name collector rather than the empty-interest shortcut.
func coldTodayFixture(t *testing.T) *App {
	t.Helper()
	a := largeTopicRegistryFixtureSized(t, topicColdFixtureSessions)
	a.topicsSvc().registryTestReadBudget = time.Duration(topicColdProofBudgetMillis) * time.Millisecond
	db := a.activity.store.db
	for _, query := range []string{
		`UPDATE activity_sessions SET start_time=start_time-(rowid%42)*86400000,end_time=end_time-(rowid%42)*86400000 WHERE id LIKE 'capture-%'`,
		`UPDATE activities SET start_ms=start_ms-(rowid%42)*86400000,end_ms=end_ms-(rowid%42)*86400000 WHERE id LIKE 'load-%'`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<192) INSERT INTO activity_events(session_id,timestamp,kind,data,sort_order) SELECT s.id,s.start_time+n.x,'window_focus','{"AppName":"Code","WindowTitle":"Atlas release planning"}',n.x FROM activity_sessions s CROSS JOIN n WHERE s.id LIKE 'capture-%'`); err != nil {
		t.Fatal(err)
	}
	purpose := NewPurposeMapStore(db)
	for i := 96; i < 320; i++ {
		input, err := userPurposeCreateInput(userPurposeCreate{Key: fmt.Sprintf("cold-%d", i), Origin: "test", Kind: PurposeKindGoal, Text: fmt.Sprintf("Goal %d", i)}, topicRegistryTestNow.Add(-time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = purpose.Create(input); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 320; i++ {
		if _, err := a.store.CreatePrincipal(t.Context(), storage.PrincipalRow{Kind: storage.PrincipalKindContact, DisplayName: fmt.Sprintf("Fixture Person %d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	storeTestInterests(t, a, interests.Interest{ID: "cold-following", Name: "Optical camera modules", Kind: interests.KindResearch, Why: "Current research", Evidence: []interests.Ref{{Key: "activity:work", Source: interests.SourceActivity, At: topicRegistryTestNow.Add(-time.Minute).UnixMilli()}}})
	a.interestsSvc().readFilter = a.interestsReadFilter
	return a
}

func coldReadBestOfThree(t *testing.T, read func()) time.Duration {
	t.Helper()
	best := time.Duration(0)
	for i := 0; i < 3; i++ {
		started := time.Now()
		read()
		elapsed := time.Since(started)
		if best == 0 || elapsed < best {
			best = elapsed
		}
	}
	return best
}

func TestTodayColdReadLargeFixtureAndCaptureChurn(t *testing.T) {
	a := coldTodayFixture(t)
	svc := a.topicsSvc()
	if _, err := collectAndReadTopicViewsForTest(a); err != nil {
		t.Fatal(err)
	}
	initialResponse, err := a.GetTodayTopics()
	if err != nil || len(initialResponse.Topics) == 0 {
		t.Fatalf("initial full snapshot: %+v %v", initialResponse, err)
	}
	initial := initialResponse.Topics
	ctx, cancel := context.WithCancel(t.Context())
	a.ctx = ctx
	a.eventBroadcaster = NewEventBroadcaster()
	events := a.eventBroadcaster.AddClient("cold-fixture")
	entered, release := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	svc.registryDuringCollect = func() {
		once.Do(func() {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
			}
		})
	}
	svc.startRegistryWorker(ctx)
	a.RefreshTodayTopics()
	t.Cleanup(func() { unblock(); cancel(); <-svc.registryWorkerDone })
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("builder did not start")
	}
	for i := 0; i < 5; i++ {
		if i > 0 {
			if err := a.activity.store.SaveSession(ActivitySessionRow{ID: fmt.Sprintf("churn-%d", i), StartTime: topicRegistryTestNow.UnixMilli() + int64(i), EndTime: topicRegistryTestNow.UnixMilli() + int64(i), AppNames: `["Code"]`, TriageStatus: "pending"}, nil, nil); err != nil {
				t.Fatal(err)
			}
			a.emitBrowserEvent("activity:session-saved")
		}
		var capture *sql.Tx
		if i == 1 {
			var err error
			capture, err = a.activity.store.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer capture.Rollback()
			if _, err = capture.Exec(`INSERT INTO activity_sessions(id,start_time,end_time,app_names,triage_status,trigger_kind,event_count,segment_count,created_at) VALUES ('capture-during-ui',?,?, '[]','pending','test',0,0,?)`, topicRegistryTestNow.UnixMilli()+1, topicRegistryTestNow.UnixMilli()+1, topicRegistryTestNow.UnixMilli()); err != nil {
				t.Fatal(err)
			}
		}
		var response TodayTopicsView
		var views []TopicView
		elapsed := coldReadBestOfThree(t, func() {
			var err error
			response, err = a.GetTodayTopics()
			views = response.Topics
			if err != nil {
				t.Fatalf("cold/churn read: views=%d err=%v", len(views), err)
			}
			if !reflect.DeepEqual(views, initial) || !response.Refreshing || response.Clear {
				t.Fatalf("capture churn changed the stored snapshot: %+v", response)
			}
			if response.Stale != (i > 0) {
				t.Fatalf("capture staleness at read %d: %+v", i, response)
			}
		})
		if capture != nil {
			if err := capture.Commit(); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("cold/churn read %d: best=%s topics=%d", i, elapsed, len(views))
		if elapsed >= 3*time.Duration(topicColdProofBudgetMillis)*time.Millisecond {
			t.Fatalf("cold/churn read: best=%s views=%d", elapsed, len(views))
		}
	}
	unblock()
	timer := time.NewTimer(45 * time.Second)
	defer timer.Stop()
	updates := 0
	for {
		select {
		case raw := <-events:
			var event BrowserEvent
			if err := json.Unmarshal(raw, &event); err != nil {
				t.Fatal(err)
			}
			if event.Event != "topics:updated" {
				continue
			}
			updates++
			response, err := a.GetTodayTopics()
			views := response.Topics
			if err != nil {
				t.Fatal(err)
			}
			if len(views) > 0 && !response.Refreshing {
				fullGoal := false
				for _, view := range views {
					fullGoal = fullGoal || view.SourceCounts["goal"] > 0 && view.Latest != nil && strings.HasPrefix(view.Latest.Text, "Goal ")
				}
				if len(views) != topicRegistryLimit || !fullGoal {
					t.Fatalf("completed read omitted goal content from the full registry: %+v", views)
				}
				if !strings.Contains(fmt.Sprint(views), "Atlas") {
					t.Fatal("full build lost work topic")
				}
				t.Logf("background completed: updates=%d full topics=%d", updates, len(views))
				goto complete
			}
		case <-timer.C:
			t.Fatal("background build never published a full result")
		}
	}
complete:
	cancel()
	<-svc.registryWorkerDone
	saved := svc.store.load()
	if saved.Today == nil || len(saved.Today.Views) == 0 {
		t.Fatal("last good views were not persisted")
	}
	restarted := newTopicsService(a)
	restarted.now = svc.now
	ctx2, cancel2 := context.WithCancel(t.Context())
	defer cancel2()
	a.ctx = ctx2
	a.topics = restarted
	blocked := make(chan struct{})
	restarted.registryBeforeSnapshot = func() {
		select {
		case <-blocked:
		case <-ctx2.Done():
		}
	}
	restarted.startRegistryWorker(ctx2)
	defer func() { cancel2(); <-restarted.registryWorkerDone }()
	var response TodayTopicsView
	var views []TopicView
	elapsed := coldReadBestOfThree(t, func() {
		var err error
		response, err = a.GetTodayTopics()
		views = response.Topics
		if err != nil || len(views) != topicRegistryLimit || response.Refreshing || !strings.Contains(fmt.Sprint(views), "Atlas") {
			t.Fatalf("persisted restart: %+v %v", views, err)
		}
	})
	t.Logf("persisted service restart: best=%s topics=%d", elapsed, len(views))
	if elapsed >= 300*time.Millisecond {
		t.Fatalf("persisted restart exceeded budget: best=%s", elapsed)
	}
	if restarted.loadRegistryCache() != nil {
		t.Fatal("restart unexpectedly used an in-memory cache")
	}
	w, err := a.topicWithdrawalForSource("activity", []string{"work"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.withTopicWithdrawal(w, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if restarted.store.load().Today != nil {
		t.Fatal("withdrawal retained display derivative")
	}
}

func TestTodayPersistedSnapshotSurvivesFailedRefresh(t *testing.T) {
	a, _, _ := registryChatTestApp(t)
	if _, err := collectAndReadTopicViewsForTest(a); err != nil {
		t.Fatal(err)
	}
	original := a.topicsSvc()
	saved := original.store.load().Today
	if saved == nil || len(saved.Views) == 0 {
		t.Fatal("fixture did not persist a verified snapshot")
	}
	svc := newTopicsService(a)
	svc.now = original.now
	a.topics = svc
	ctx, cancel := context.WithCancel(t.Context())
	a.ctx = ctx
	svc.startRegistryWorker(ctx)
	t.Cleanup(func() { cancel(); svc.stopRegistryWorker() })
	first, err := a.GetTodayTopics()
	if err != nil || len(first.Topics) == 0 || first.Refreshing || first.Clear || !reflect.DeepEqual(first.Topics, saved.Views) {
		t.Fatalf("cold persisted read: %+v %v", first, err)
	}
	failed := make(chan struct{})
	svc.registryCollectContext = func(parent context.Context) (context.Context, context.CancelFunc) {
		return context.WithTimeout(parent, time.Nanosecond)
	}
	svc.registryDuringCollect = func() { close(failed) }
	a.RefreshTodayTopics()
	select {
	case <-failed:
	case <-time.After(10 * time.Second):
		t.Fatal("refresh did not fail")
	}
	for svc.registryFullBuildRunning() {
		time.Sleep(time.Millisecond)
	}
	for i := 0; i < 20; i++ {
		got, err := a.GetTodayTopics()
		if err != nil || got.Refreshing || got.Clear || !reflect.DeepEqual(got.Topics, first.Topics) {
			t.Fatalf("failed refresh read %d: %+v %v", i, got, err)
		}
	}
	svc.registryCollectContext = nil
	svc.registryDuringCollect = nil
	svc.now = func() time.Time { return original.now().Add(time.Second) }
	a.RefreshTodayTopics()
	deadline := time.Now().Add(10 * time.Second)
	unchanged := func() bool {
		current := svc.store.load().Today
		return current == nil || current.At.Equal(saved.At)
	}
	for unchanged() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	replaced := svc.store.load().Today
	if replaced == nil || replaced.At.Equal(saved.At) {
		t.Fatal("newer verified view did not replace persisted snapshot")
	}
	settled := time.Now().Add(10 * time.Second)
	for svc.registryFullBuildRunning() && time.Now().Before(settled) {
		time.Sleep(time.Millisecond)
	}
	got, err := a.GetTodayTopics()
	if err != nil || got.Refreshing || got.Clear || !reflect.DeepEqual(got.Topics, replaced.Views) {
		t.Fatalf("replacement read: %+v %v", got, err)
	}
}

func TestTodaySnapshotReadsDoNotRevalidateSourcesButHonorPrivacy(t *testing.T) {
	for _, change := range []string{"database", "file", "policy", "choice", "withdrawal"} {
		t.Run(change, func(t *testing.T) {
			a, _, _ := registryChatTestApp(t)
			svc := a.topicsSvc()
			previousSensitiveApps := pii.GetUserSensitiveApps()
			t.Cleanup(func() { pii.SetUserSensitiveApps(previousSensitiveApps) })
			if _, err := collectAndReadTopicViewsForTest(a); err != nil {
				t.Fatal(err)
			}
			before, err := a.GetTodayTopics()
			if err != nil || len(before.Topics) == 0 {
				t.Fatalf("snapshot control: %+v %v", before, err)
			}
			var hasAtlasSource bool
			if snapshot := svc.snapshots.Current(); snapshot != nil {
				for _, entry := range snapshot.Topics {
					for _, ref := range entry.Refs {
						hasAtlasSource = hasAtlasSource || ref.Kind == topicRefSource && ref.Value == "activity:atlas"
					}
				}
			}
			if !hasAtlasSource {
				t.Fatal("snapshot omitted source activity:atlas")
			}
			switch change {
			case "database":
				if _, err := a.activity.store.db.Exec(`UPDATE activities SET latest_change='Updated source' WHERE id='atlas'`); err != nil {
					t.Fatal(err)
				}
			case "file":
				dir, err := memoryDir()
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "changed.md"), []byte("source changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "withdrawal":
				if err := a.deleteActivityWithPrivacyWithdrawal(a.activity.store, "atlas"); err != nil {
					t.Fatal(err)
				}
			case "policy":
				pii.SetUserSensitiveApps(map[string]string{"Code": "Code"})
			case "choice":
				if _, err := svc.store.update(func(state *topicsStateFile) {
					state.SidebarPins = map[string]bool{before.Topics[0].ID: true}
				}); err != nil {
					t.Fatal(err)
				}
			}
			response, err := a.GetTodayTopics()
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "policy", "withdrawal":
				if len(response.Topics) != 0 {
					t.Fatalf("privacy-filtered source remained visible: %+v", response)
				}
			case "choice":
				if len(response.Topics) != len(before.Topics) || !response.Topics[0].PinnedToSidebar {
					t.Fatalf("live sidebar choice was not applied: %+v", response)
				}
			default:
				if !reflect.DeepEqual(response.Topics, before.Topics) || response.Clear {
					t.Fatalf("source revision caused a read-time rebuild or clear: %+v", response)
				}
			}
		})
	}
}

func TestTodaySnapshotPersistenceFailureRetainsLastGood(t *testing.T) {
	a, _, _ := registryChatTestApp(t)
	svc := a.topicsSvc()
	if _, err := collectAndReadTopicViewsForTest(a); err != nil {
		t.Fatal(err)
	}
	saved := svc.store.load()
	cached := svc.loadRegistryCache()
	if err := os.Mkdir(svc.store.path+".tmp", 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := a.activity.store.db.Exec(`UPDATE activities SET latest_change='Newer source' WHERE id='atlas'`); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.collectRegistryViewsLocked(t.Context(), true); err == nil {
		t.Fatal("persistence failure was not exercised")
	}
	if svc.loadRegistryCache() != cached || proactiveVoiceHash(svc.store.load()) != proactiveVoiceHash(saved) {
		t.Fatal("failed publication replaced last good state")
	}
	if err := os.Remove(svc.store.path + ".tmp"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.collectRegistryViewsLocked(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if svc.store.load().Today == nil || svc.loadRegistryCache() == cached {
		t.Fatal("persistence did not recover")
	}
}

func TestTodayEmptyColdReadReportsRefreshingDuringPrivacyContention(t *testing.T) {
	a, _, _ := registryChatTestApp(t)
	svc := a.topicsSvc()
	ctx, cancel := context.WithCancel(t.Context())
	a.ctx = ctx
	entered := make(chan struct{})
	svc.registryBeforeSnapshot = func() { close(entered); <-ctx.Done() }
	svc.startRegistryWorker(ctx)
	a.RefreshTodayTopics()
	defer func() { cancel(); <-svc.registryWorkerDone }()
	<-entered
	memoryMutationMu.Lock()
	defer memoryMutationMu.Unlock()
	var response TodayTopicsView
	elapsed := coldReadBestOfThree(t, func() {
		var err error
		response, err = a.GetTodayTopics()
		if err != nil || len(response.Topics) != 0 || !response.Refreshing {
			t.Fatalf("busy cold read: %+v err=%v", response, err)
		}
	})
	if elapsed >= 300*time.Millisecond {
		t.Fatalf("busy cold read exceeded budget: best=%s", elapsed)
	}
}

func TestTopicRegistryNameOnlyCheckDoesNotCollectHistory(t *testing.T) {
	a, _, _ := registryChatTestAppAt(t, topicTestNow)
	if _, err := a.store.CreatePrincipal(t.Context(), storage.PrincipalRow{Kind: storage.PrincipalKindContact, DisplayName: "Ada Lovelace"}); err != nil {
		t.Fatal(err)
	}
	storeTestInterests(t, a, testInterest("person", "Ada Lovelace", "Must be excluded"), testInterest("research", "Optical camera modules", "Current research"))
	if _, err := collectAndReadTopicViewsForTest(a); err != nil {
		t.Fatal(err)
	}
	a.interestsSvc().collect = func(time.Time, time.Time) interestInput {
		t.Fatal("name check collected the historical interest input")
		return interestInput{}
	}
	views, err := a.todayTopicViews()
	if err != nil {
		t.Fatal(err)
	}
	hasResearch := false
	for _, view := range views {
		if view.Name == "Ada Lovelace" {
			t.Fatal("person became a followed topic")
		}
		hasResearch = hasResearch || view.Name == "Optical camera modules"
	}
	if !hasResearch {
		t.Fatal("valid research control disappeared")
	}
}

func TestTodayBackgroundServingKeepsEverySidebarPin(t *testing.T) {
	a, _, now := registryChatTestApp(t)
	svc := a.topicsSvc()
	purpose := NewPurposeMapStore(a.activity.store.db)
	for i := 0; i < 16; i++ {
		input, err := userPurposeCreateInput(userPurposeCreate{Key: fmt.Sprintf("sidebar-%d", i), Origin: "test", Kind: PurposeKindGoal, Text: fmt.Sprintf("Pinned project %d", i)}, now.Add(-time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = purpose.Create(input); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.collectRegistryViewsLocked(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	snapshot := svc.snapshots.Current()
	if snapshot == nil {
		t.Fatal("fixture did not persist a verified snapshot")
	}
	all := make([]TopicView, len(snapshot.Topics))
	for i, entry := range snapshot.Topics {
		all[i] = entry.Topic
	}
	if len(all) <= topicRegistryLimit {
		t.Fatal("fixture does not exceed Today limit")
	}
	if _, err := svc.store.update(func(st *topicsStateFile) {
		st.SidebarPins = map[string]bool{}
		for _, v := range all {
			st.SidebarPins[v.ID] = true
		}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.collectRegistryViewsLocked(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	a.ctx = ctx
	entered := make(chan struct{})
	svc.registryBeforeSnapshot = func() { close(entered); <-ctx.Done() }
	svc.startRegistryWorker(ctx)
	a.RefreshTodayTopics()
	defer func() { cancel(); <-svc.registryWorkerDone }()
	<-entered
	response, err := a.GetTodayTopics()
	if err != nil || len(response.Topics) != topicRegistryLimit {
		t.Fatalf("Today limit: %d %v", len(response.Topics), err)
	}
	sidebar, err := a.ListSidebarTopics()
	if err != nil || len(sidebar) != len(all) {
		t.Fatalf("sidebar pins: got=%d want=%d err=%v", len(sidebar), len(all), err)
	}
	if _, err := svc.store.update(func(st *topicsStateFile) { delete(st.SidebarPins, all[0].ID) }); err != nil {
		t.Fatal(err)
	}
	sidebar, err = a.ListSidebarTopics()
	if err != nil || len(sidebar) != len(all)-1 {
		t.Fatalf("pin change hid unrelated rows: got=%d want=%d err=%v", len(sidebar), len(all)-1, err)
	}
}

func TestTopicRegistryWorkerStopCancelsAndJoinsBeforePublication(t *testing.T) {
	a, _, _ := registryChatTestApp(t)
	svc := a.topicsSvc()
	a.eventBroadcaster = NewEventBroadcaster()
	events := a.eventBroadcaster.AddClient("stopped-builder")
	entered := make(chan struct{})
	svc.registryBeforeSnapshot = func() { close(entered); <-svc.registryContext().Done() }
	svc.startRegistryWorker(t.Context())
	<-entered
	started := time.Now()
	svc.stopRegistryWorker()
	svc.stopRegistryWorker()
	if time.Since(started) > time.Second {
		t.Fatal("Stop did not cancel the active builder")
	}
	if svc.loadRegistryCache() != nil || svc.store.load().Today != nil {
		t.Fatal("canceled builder published a display view")
	}
	select {
	case event := <-events:
		t.Fatalf("event after Stop: %s", event)
	default:
	}
}

func TestTopicRegistryWorkerDeliveryDoesNotRevalidateSources(t *testing.T) {
	a, _, _ := registryChatTestApp(t)
	svc := a.topicsSvc()
	if _, err := collectAndReadTopicViewsForTest(a); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	entered := make(chan struct{})
	svc.registryBeforeSnapshot = func() { close(entered); <-ctx.Done() }
	svc.startRegistryWorker(ctx)
	a.RefreshTodayTopics()
	defer func() { cancel(); <-svc.registryWorkerDone }()
	<-entered
	checks := 0
	svc.registryFileStats = func(time.Duration) { checks++ }
	response, err := a.GetTodayTopics()
	if err != nil || len(response.Topics) != 1 || response.Clear || !response.Refreshing || checks != 0 {
		t.Fatalf("checked cached delivery: %+v err=%v checks=%d", response, err, checks)
	}
}
