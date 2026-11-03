package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"wails-gui/internal/interests"
)

func TestTopicCaptureBurstsDoNotBuildOrClearSnapshot(t *testing.T) {
	a, _, _ := registryChatTestApp(t)
	svc := a.topicsSvc()
	if _, err := collectAndReadTopicViewsForTest(a); err != nil {
		t.Fatal(err)
	}
	initial, err := a.GetTodayTopics()
	if err != nil || len(initial.Topics) == 0 {
		t.Fatalf("initial snapshot: %+v, %v", initial, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	a.ctx = ctx
	a.eventBroadcaster = NewEventBroadcaster()
	events := a.eventBroadcaster.AddClient("ten-minute-capture")
	var builds atomic.Int32
	svc.registryBeforeSnapshot = func() { builds.Add(1) }
	svc.startRegistryWorker(ctx)
	t.Cleanup(func() { cancel(); svc.stopRegistryWorker() })
	for batch := 0; batch < 120; batch++ {
		at := time.Now().UnixMilli() + int64(batch)
		if err := a.activity.store.SaveSession(ActivitySessionRow{ID: fmt.Sprintf("no-blink-capture-%d", batch), StartTime: at, EndTime: at, AppNames: `["Code"]`, TriageStatus: "pending", TriggerKind: "test"}, nil, nil); err != nil {
			t.Fatal(err)
		}
		for burst := 0; burst < 8; burst++ {
			a.emitBrowserEvent("activity:session-saved")
		}
	}
	got, err := a.GetTodayTopics()
	if err != nil || got.Clear || !got.Stale || !reflect.DeepEqual(got.Topics, initial.Topics) {
		t.Fatalf("capture bursts changed snapshot: %+v, %v", got, err)
	}
	if got := builds.Load(); got != 0 {
		t.Fatalf("120 capture bursts synchronously built %d times", got)
	}
	for len(events) > 0 {
		var event BrowserEvent
		if err := json.Unmarshal(<-events, &event); err != nil {
			t.Fatal(err)
		}
		if event.Event == "topics:updated" {
			t.Fatal("capture burst emitted topics:updated before a scheduled build")
		}
	}
}

func TestTopicFailedFullBuildPreservesSnapshotDuringCaptureChurn(t *testing.T) {
	a, _, _ := registryChatTestApp(t)
	svc := a.topicsSvc()
	if _, err := collectAndReadTopicViewsForTest(a); err != nil {
		t.Fatal(err)
	}
	base := svc.now()
	svc.now = func() time.Time { return base }
	ctx, cancel := context.WithCancel(t.Context())
	a.ctx = ctx
	var builds atomic.Int32
	svc.registryBeforeSnapshot = func() { builds.Add(1) }
	svc.registryTestReadBudget = 100 * time.Millisecond
	failed := make(chan struct{})
	var expire context.CancelFunc
	svc.registryCollectContext = func(parent context.Context) (context.Context, context.CancelFunc) {
		child, stop := context.WithCancel(parent)
		expire = stop
		return child, stop
	}
	svc.registryDuringCollect = func() {
		if builds.Load() == 1 {
			expire()
			close(failed)
		}
	}
	svc.startRegistryWorker(ctx)
	t.Cleanup(func() { cancel(); svc.stopRegistryWorker() })
	a.RefreshTodayTopics()
	select {
	case <-failed:
	case <-time.After(10 * time.Second):
		t.Fatal("first full collection did not fail")
	}
	for !svc.registryMu.TryLock() {
		time.Sleep(time.Millisecond)
	}
	svc.registryMu.Unlock()
	first, err := a.GetTodayTopics()
	if err != nil || len(first.Topics) == 0 || first.Clear {
		t.Fatalf("verified bootstrap missing after full failure: %+v %v", first, err)
	}
	if first.Refreshing {
		t.Fatalf("verified bootstrap stayed refreshing with no full build running: %+v", first)
	}
	for batch := 0; batch < 40; batch++ {
		at := svc.now().UnixMilli()
		if err := a.activity.store.SaveSession(ActivitySessionRow{ID: fmt.Sprintf("failed-full-capture-%d", batch), StartTime: at, EndTime: at, AppNames: `["Code"]`, TriageStatus: "pending", TriggerKind: "test"}, nil, nil); err != nil {
			t.Fatal(err)
		}
		for burst := 0; burst < 8; burst++ {
			a.emitBrowserEvent("activity:session-saved")
		}
		got, err := a.GetTodayTopics()
		if err != nil || got.Refreshing || got.Clear || !reflect.DeepEqual(got.Topics, first.Topics) || !got.Stale {
			t.Fatalf("capture batch %d disturbed bootstrap: %+v %v", batch, got, err)
		}
	}
	if got := builds.Load(); got != 1 {
		t.Fatalf("capture batches forced %d full builds", got-1)
	}
}

func TestTopicColdBootstrapRecoversOnceAfterFullFailure(t *testing.T) {
	a, _, _ := registryChatTestApp(t)
	svc := a.topicsSvc()
	if _, err := svc.store.update(func(state *topicsStateFile) { state.Today = nil }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	a.ctx = ctx
	entered, release, failed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	var builds atomic.Int32
	svc.registryBeforeSnapshot = func() {
		builds.Add(1)
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
		}
	}
	svc.registryCollectContext = func(parent context.Context) (context.Context, context.CancelFunc) {
		return context.WithTimeout(parent, time.Nanosecond)
	}
	svc.registryDuringCollect = func() { close(failed) }
	var stages topicStageLog
	oldLog := log.Writer()
	log.SetOutput(&stages)
	t.Cleanup(func() { log.SetOutput(oldLog) })
	svc.startRegistryWorker(ctx)
	t.Cleanup(func() { unblock(); cancel(); svc.stopRegistryWorker() })
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("initial full build did not start")
	}
	first, err := a.GetTodayTopics()
	if err != nil || len(first.Topics) != 0 || !first.Refreshing || first.Clear {
		t.Fatalf("cold snapshot read during build: %+v %v", first, err)
	}
	unblock()
	select {
	case <-failed:
	case <-time.After(10 * time.Second):
		t.Fatal("initial full build did not fail")
	}
	for svc.registryFullBuildRunning() {
		time.Sleep(time.Millisecond)
	}
	second, err := a.GetTodayTopics()
	if err != nil || len(second.Topics) != 0 || second.Refreshing || second.Clear {
		t.Fatalf("failed cold build published content or cleared the empty view: %+v %v", second, err)
	}
	if got := builds.Load(); got != 1 {
		t.Fatalf("cold startup ran %d builds before the retry interval, want one", got)
	}
	svc.registryBeforeSnapshot = nil
	svc.registryCollectContext = nil
	svc.registryDuringCollect = nil
	a.RefreshTodayTopics()
	deadline := time.Now().Add(10 * time.Second)
	var recovered TodayTopicsView
	for time.Now().Before(deadline) {
		recovered, err = a.GetTodayTopics()
		if svc.snapshots.Current() != nil && !recovered.Refreshing {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil || len(recovered.Topics) == 0 || recovered.Clear || recovered.Refreshing {
		t.Fatalf("manual full refresh did not recover cold start: %+v %v", recovered, err)
	}
}

func TestTopicRealSizeExhaustedBootstrapDoesNotLoop(t *testing.T) {
	a := populatedTodayTopicsFixture(t)
	svc := a.topicsSvc()
	items := a.interestsSvc().store.load().Interests
	for i := 0; i < 17; i++ {
		id := fmt.Sprintf("loop-history-%d", i)
		at := svc.now().Add(-time.Hour)
		name := fmt.Sprintf("Historical research %02d", i)
		seedThreadMoment(t, a, threadFixtureMoment{id: id, title: name, app: "Code", start: at.Add(-time.Minute), end: at})
		item := testInterest(fmt.Sprintf("loop-history-interest-%d", i), name, "Research")
		item.Kind = interests.KindResearch
		item.Evidence = []interests.Ref{{Source: interests.SourceActivity, Key: "activity:" + id}}
		items = append(items, item)
	}
	for i := 15; i < 62; i++ {
		item := testInterest(fmt.Sprintf("withheld-loop-%02d", i), fmt.Sprintf("Withheld research %02d", i), "Research")
		item.Kind = interests.KindResearch
		item.Evidence = nil
		items = append(items, item)
	}
	storeTestInterests(t, a, items...)
	views, err := collectAndReadTopicSnapshotViewsForTest(a)
	if err != nil || len(views) == 0 {
		t.Fatalf("full registry snapshot: topics=%d err=%v", len(views), err)
	}
	present, withheld := 0, 0
	for _, item := range items {
		if strings.HasPrefix(item.ID, "loop-history-interest-") && topicViewsRepresentInterest(a, views, item.ID) {
			present++
		}
		if strings.HasPrefix(item.ID, "withheld-loop-") && topicViewsRepresentInterest(a, views, item.ID) {
			withheld++
		}
	}
	if present != 17 || withheld != 0 {
		t.Fatalf("full-build privacy projection: verified=%d withheld=%d", present, withheld)
	}
	svc.registryCollectContext = func(context.Context) (context.Context, context.CancelFunc) {
		t.Fatal("snapshot reads attempted another full registry build")
		return nil, nil
	}
	first, err := a.GetTodayTopics()
	if err != nil || first.Clear || len(first.Topics) == 0 {
		t.Fatalf("populated snapshot: %+v %v", first, err)
	}
	for i := 0; i < 20; i++ {
		got, err := a.GetTodayTopics()
		if err != nil || got.Clear || !reflect.DeepEqual(got.Topics, first.Topics) {
			t.Fatalf("snapshot read %d changed: %+v %v", i, got, err)
		}
	}
}

func TestTopicVerifiedEmptyBootstrapSettlesAfterFullFailure(t *testing.T) {
	a, _, _ := registryChatTestApp(t)
	if err := a.deleteActivityWithTopicScrub(a.activity.store, "atlas"); err != nil {
		t.Fatal(err)
	}
	svc := a.topicsSvc()
	ctx, cancel := context.WithCancel(t.Context())
	a.ctx = ctx
	entered, failed := make(chan struct{}), make(chan struct{})
	svc.registryBeforeSnapshot = func() { close(entered) }
	var expire context.CancelFunc
	svc.registryCollectContext = func(parent context.Context) (context.Context, context.CancelFunc) {
		child, stop := context.WithCancel(parent)
		expire = stop
		return child, stop
	}
	svc.registryDuringCollect = func() { expire(); close(failed) }
	svc.startRegistryWorker(ctx)
	t.Cleanup(func() { cancel(); svc.stopRegistryWorker() })
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("empty build did not start")
	}
	first, err := a.GetTodayTopics()
	if err != nil || len(first.Topics) != 0 || first.Clear || !first.Refreshing {
		t.Fatalf("verified empty active read: %+v %v", first, err)
	}
	select {
	case <-failed:
	case <-time.After(10 * time.Second):
		t.Fatal("empty full build did not fail")
	}
	deadline := time.Now().Add(10 * time.Second)
	for svc.registryFullBuildRunning() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	settled, err := a.GetTodayTopics()
	if err != nil || settled.Refreshing || settled.Clear || len(settled.Topics) != 0 {
		t.Fatalf("verified empty did not settle: %+v %v", settled, err)
	}
}

func TestTopicBudgetExhaustionDoesNotClearPopulatedPublication(t *testing.T) {
	a, _, _ := registryChatTestApp(t)
	if views, err := collectAndReadTopicViewsForTest(a); err != nil || len(views) == 0 {
		t.Fatalf("seed publication: views=%d err=%v", len(views), err)
	}
	svc := a.topicsSvc()
	a.ctx = t.Context()
	svc.startRegistryWorker(t.Context())
	t.Cleanup(svc.stopRegistryWorker)
	svc.registryTestReadBudget = time.Nanosecond
	got, err := a.GetTodayTopics()
	if err != nil {
		t.Fatal(err)
	}
	if got.Clear || !got.HasTopics {
		t.Fatalf("budget exhaustion cleared populated publication: %+v", got)
	}
}

func TestTopicRevisionFailureWaitsForRefreshInterval(t *testing.T) {
	a, _, _ := registryChatTestApp(t)
	if _, err := collectAndReadTopicViewsForTest(a); err != nil {
		t.Fatal(err)
	}
	svc := a.topicsSvc()
	ctx, cancel := context.WithCancel(t.Context())
	a.ctx = ctx
	timer := make(chan time.Time, 1)
	svc.registryAfter = func(time.Duration) <-chan time.Time { return timer }
	var builds atomic.Int32
	var attempts atomic.Int32
	failed := make(chan struct{})
	svc.registryBeforeSnapshot = func() { builds.Add(1) }
	svc.registryCollectContext = func(parent context.Context) (context.Context, context.CancelFunc) {
		if attempts.Add(1) == 1 {
			child, stop := context.WithCancel(parent)
			stop()
			close(failed)
			return child, stop
		}
		return parent, func() {}
	}
	svc.startRegistryWorker(ctx)
	t.Cleanup(func() { cancel(); svc.stopRegistryWorker() })
	a.RefreshTodayTopics()
	deadline := time.Now().Add(10 * time.Second)
	for builds.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if builds.Load() != 1 {
		t.Fatalf("initial revision build missing: builds=%d", builds.Load())
	}
	select {
	case <-failed:
	case <-time.After(10 * time.Second):
		t.Fatal("first full build did not fail")
	}
	for svc.registryFullBuildRunning() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if builds.Load() != 1 {
		t.Fatalf("failed build bypassed refresh timer: builds=%d", builds.Load())
	}
	timer <- time.Now().Add(topicBackgroundRefreshDefault)
	deadline = time.Now().Add(10 * time.Second)
	for builds.Load() != 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if builds.Load() != 2 {
		t.Fatalf("timer did not retry failed build: builds=%d", builds.Load())
	}
}

func TestTopicPublicationRetainsPositionsAndAppends(t *testing.T) {
	old := []TopicView{{ID: "a", Name: "Atlas"}, {ID: "b", Name: "Beacon"}}
	incoming := []TopicView{{ID: "c", Name: "New"}, {ID: "b", Name: "Updated"}, {ID: "a", Name: "Atlas"}}
	got := stableTopicViewOrder(old, incoming)
	if len(got) != 3 || got[0].ID != "a" || got[1].Name != "Updated" || got[2].ID != "c" {
		t.Fatalf("changed shared publication order: %+v", got)
	}
	got = stableTopicViewOrder(old, incoming[:2])
	if len(got) != 2 || got[0].ID != "b" || got[1].ID != "c" {
		t.Fatalf("removed card retained: %+v", got)
	}
}

func TestTopicStartupRefreshesOnlyOldStoredDisplay(t *testing.T) {
	for _, startHour := range []int{10, 23} {
		t.Run(fmt.Sprintf("start_%02d_30", startHour), func(t *testing.T) {
			start := time.Date(2026, time.October, 26, startHour, 30, 0, 0, time.Local)
			// Keep the seeded activity and every restarted clock on the same local day.
			base := time.Date(start.Year(), start.Month(), start.Day(), 10, 0, 0, 0, start.Location())
			for _, age := range []time.Duration{time.Hour, 2*time.Hour + time.Minute} {
				t.Run(age.String(), func(t *testing.T) {
					a, _, _ := registryChatTestAppAt(t, base)
					original := a.topicsSvc()
					views, err := collectAndReadTopicViewsForTest(a)
					if err != nil || len(views) == 0 {
						t.Fatalf("seed display: %v %v", views, err)
					}
					now := original.now().Add(age)
					restarted := newTopicsService(a)
					restarted.now = func() time.Time { return now }
					a.topics = restarted
					a.ctx = t.Context()
					entered := make(chan struct{}, 1)
					restarted.registryBeforeSnapshot = func() { entered <- struct{}{} }
					restarted.startRegistryWorker(t.Context())
					t.Cleanup(restarted.stopRegistryWorker)
					if age < topicBackgroundRefreshDefault {
						select {
						case <-entered:
							t.Fatal("fresh stored display rebuilt on startup")
						case <-time.After(50 * time.Millisecond):
						}
						got, err := a.GetTodayTopics()
						if err != nil || got.Refreshing || got.Clear || len(got.Topics) != len(views) {
							t.Fatalf("fresh stored display: %+v %v", got, err)
						}
						if _, err := a.ListMainChatTopicItems(views[0].ID, "", 50); err != nil {
							t.Fatalf("fresh persisted chat action: %v", err)
						}
					} else {
						select {
						case <-entered:
						case <-time.After(10 * time.Second):
							t.Fatal("old stored display did not rebuild on startup")
						}
					}
				})
			}
		})
	}
}

func TestTopicWithdrawalNotifiesAndSchedulesRebuild(t *testing.T) {
	for _, change := range []string{"commit", "rollback", "unrelated", "empty", "following"} {
		t.Run(change, func(t *testing.T) {
			a, _, _ := registryChatTestApp(t)
			if change == "following" {
				item := testInterest("partial-research", "Optical modules", "Current research")
				item.Evidence = []interests.Ref{{Source: interests.SourceActivity, Key: "activity:atlas"}}
				storeTestInterests(t, a, item)
			}
			svc := a.topicsSvc()
			if change == "empty" {
				if err := svc.snapshots.Replace(&topicSnapshot{Version: topicSnapshotSchemaVersion, At: time.Now()}); err != nil {
					t.Fatal(err)
				}
			} else if _, err := collectAndReadTopicViewsForTest(a); err != nil {
				t.Fatal(err)
			}
			a.ctx = t.Context()
			a.eventBroadcaster = NewEventBroadcaster()
			events := a.eventBroadcaster.AddClient("withdrawal")
			timer := make(chan time.Time, 1)
			svc.registryAfter = func(time.Duration) <-chan time.Time { return timer }
			var builds atomic.Int32
			buildEntered := make(chan struct{}, 1)
			svc.registryBeforeSnapshot = func() {
				builds.Add(1)
				select {
				case buildEntered <- struct{}{}:
				default:
				}
				<-t.Context().Done()
			}
			svc.startRegistryWorker(t.Context())
			t.Cleanup(svc.stopRegistryWorker)
			id := "atlas"
			if change == "unrelated" {
				id = "absent-unrelated-source"
			}
			withdrawal, err := a.topicWithdrawalForSource("activity", []string{id}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if change == "rollback" {
				finish, err := a.beginTopicWithdrawal(withdrawal)
				if err != nil {
					t.Fatal(err)
				}
				if err := finish(false); err != nil {
					t.Fatal(err)
				}
			} else if err := a.deleteActivityWithPrivacyWithdrawal(a.activity.store, id); err != nil {
				t.Fatal(err)
			}
			notified := false
			for len(events) > 0 {
				var event BrowserEvent
				if err := json.Unmarshal(<-events, &event); err != nil {
					t.Fatal(err)
				}
				if event.Event == "topics:withdrawn" {
					notified = true
				}
			}
			if notified != (change == "commit" || change == "following") {
				t.Fatalf("change=%s notified=%t", change, notified)
			}
			if builds.Load() != 0 {
				t.Fatalf("withdrawal synchronously forced %d full collections", builds.Load())
			}
			if change != "rollback" {
				select {
				case <-buildEntered:
					t.Fatal("withdrawal rebuilt before its delay elapsed")
				default:
				}
			}
			if change == "commit" || change == "following" {
				got, err := a.GetTodayTopics()
				if err != nil || !got.Clear || len(got.Topics) != 0 {
					t.Fatalf("withdrawn display: %+v %v", got, err)
				}
			}
			if change != "rollback" {
				timer <- time.Now().Add(topicWithdrawalRebuildDelay)
				select {
				case <-buildEntered:
				case <-time.After(time.Second):
					t.Fatal("scheduled withdrawal rebuild did not start")
				}
			}
		})
	}
}
