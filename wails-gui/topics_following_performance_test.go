//go:build !race

package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"wails-gui/internal/interests"
)

func TestTopicFollowingLargeFixtureSkipsSlowReadFilterAndHonorsContext(t *testing.T) {
	a := newThreadTestApp(t)
	// The read filter re-derives named evidence within a window relative to
	// wall time, so this fixture must stay inside that window.
	now := time.Now()
	configureTopicRegistryTest(a, now)
	seedThreadMoment(t, a, threadFixtureMoment{id: "work", title: "Atlas", app: "Code", start: now.Add(-time.Hour), end: now.Add(-time.Minute)})
	const activityCount = 3000
	if _, err := a.activity.store.db.Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<?)
		INSERT INTO activity_sessions(id,start_time,end_time,app_names,triage_status,trigger_kind,event_count,segment_count,created_at)
		SELECT 'capture-'||x,?,?, '["Code"]','pending','test',1,1,? FROM n`, activityCount, now.Add(-time.Hour).UnixMilli(), now.UnixMilli(), now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`INSERT INTO activity_events(session_id,timestamp,kind,data,sort_order) SELECT id,end_time,'window_focus','{"appName":"Code","appBundleId":"com.example.code","windowTitle":"Atlas"}',0 FROM activity_sessions WHERE id LIKE 'capture-%'`,
		`INSERT INTO activity_timeline_groups(id,first_event_row_id,last_event_row_id,app_key,app_name,start_ms,end_ms,active_ms,event_count,session_ids,status,summary_status,summarized_event_count,created_at,updated_at)
		 SELECT 'load-run-'||s.id,e.id,e.id,'com.example.code','Code',s.start_time,s.end_time,1000,1,json_array(s.id),'closed','done',1,s.created_at,s.created_at FROM activity_sessions s JOIN activity_events e ON e.session_id=s.id WHERE s.id LIKE 'capture-%'`,
		`INSERT INTO activities(id,day_start_ms,start_ms,end_ms,title,status,run_ids,active_ms_by_app,entities,created_at,updated_at)
		 SELECT 'load-'||s.id,seed.day_start_ms,s.start_time,?,seed.title,seed.status,json_array('load-run-'||s.id),seed.active_ms_by_app,seed.entities,s.created_at,s.created_at FROM activity_sessions s CROSS JOIN activities seed WHERE s.id LIKE 'capture-%' AND seed.id='work'`,
		`UPDATE activity_timeline_group_summaries SET activity_type='research',entities='["Atlas"]' WHERE group_id='run-work'`,
	} {
		if _, err := a.activity.store.db.Exec(query, now.Add(-2*time.Minute).UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	list := make([]interests.Interest, 0, 10)
	for i := 1; i <= 10; i++ {
		item := testInterest(fmt.Sprintf("following-%d", i), fmt.Sprintf("Research topic %d", i), "Current research")
		item.Evidence[0].Key = fmt.Sprintf("activity:load-capture-%d", i)
		item.Evidence[0].At = now.Add(-time.Minute).UnixMilli()
		list = append(list, item)
	}
	list[0].Evidence = append(list[0].Evidence, interests.Ref{Key: "named:" + interestHash("atlas"), Source: interests.SourceNamed, At: now.Add(-time.Minute).UnixMilli()})
	storeTestInterests(t, a, list...)
	svc := a.interestsSvc()
	svc.readFilter = a.interestsReadFilter

	started := time.Now()
	if got := svc.list(); len(got) != len(list) {
		t.Fatalf("complete read filter withheld fixture interests: %d", len(got))
	}
	before := time.Since(started)
	svc.rememberFollowing(t.Context(), list)
	revision, err := a.topicsSvc().captureRegistryRevision(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	slowReads := 0
	svc.readFilter = func() func(interests.Interest) bool {
		slowReads++
		time.Sleep(250 * time.Millisecond)
		return func(interests.Interest) bool { return true }
	}
	started = time.Now()
	got, err := svc.following(t.Context(), revision)
	after := time.Since(started)
	if err != nil || len(got) != len(list) {
		t.Fatalf("following=%d err=%v", len(got), err)
	}
	if slowReads != 0 {
		t.Fatalf("following invoked the slow read filter %d times", slowReads)
	}
	t.Logf("activities=%d interests=%d before=%s after=%s", activityCount+1, len(list), before, after)
	if after >= 200*time.Millisecond {
		t.Fatalf("following exceeded 200ms: %s", after)
	}

	svc.followingMu.Lock()
	svc.followingVerified = nil
	svc.followingMu.Unlock()
	entered := make(chan struct{})
	svc.followingFilter = func(ctx context.Context) func(interests.Interest) bool {
		close(entered)
		<-ctx.Done()
		return nil
	}
	deadline, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	started = time.Now()
	_, _, err = a.topicsSvc().registryFollowing(deadline, a.topicsSvc().store.load(), revision, false)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) >= 200*time.Millisecond {
		t.Fatalf("following ignored its deadline: err=%v elapsed=%s", err, time.Since(started))
	}
	select {
	case <-entered:
	default:
		t.Fatal("deadline fixture did not enter the following filter")
	}
	if slowReads != 0 {
		t.Fatal("deadline path invoked the slow read filter")
	}

	svc.followingFilter = a.interestsFollowingReadFilter
	if _, err := a.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status='protected' WHERE id='capture-2'`); err != nil {
		t.Fatal(err)
	}
	revision, err = a.topicsSvc().captureRegistryRevision(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	got, err = svc.following(t.Context(), revision)
	if err != nil || followingFixtureContains(got, "following-2") {
		t.Fatalf("protected evidence remained visible: ids=%v err=%v", followingFixtureIDs(got), err)
	}
	if _, err := a.activity.store.db.Exec(`DELETE FROM activities WHERE id='load-capture-3'`); err != nil {
		t.Fatal(err)
	}
	revision, err = a.topicsSvc().captureRegistryRevision(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	got, err = svc.following(t.Context(), revision)
	if err != nil || followingFixtureContains(got, "following-3") {
		t.Fatalf("deleted evidence remained visible: ids=%v err=%v", followingFixtureIDs(got), err)
	}
	if slowReads != 0 {
		t.Fatal("privacy checks invoked the slow read filter")
	}
}
