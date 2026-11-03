package main

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"modernc.org/sqlite"
)

func seedRetentionBacklog(t *testing.T, app *App, count int) {
	t.Helper()
	at := time.Now().Add(-45 * 24 * time.Hour)
	for i := 0; i < count; i++ {
		seedMobileBriefPurposeRun(t, app, fmt.Sprintf("backlog-%03d", i), int64(i+1), at.Add(time.Duration(i)*time.Second), 1000, []string{"Editor"})
	}
}

func TestActivityRetentionPurgeReleasesConnectionBetweenBatches(t *testing.T) {
	assertRetentionBatchYields(t, false)
}

func TestActivityRetentionPrivacyPreparationReleasesConnectionBetweenBatches(t *testing.T) {
	assertRetentionBatchYields(t, true)
}

func assertRetentionBatchYields(t *testing.T, prepare bool) {
	t.Helper()
	entered, release, readDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	var processed atomic.Int32
	function := "retention_batch_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := sqlite.RegisterScalarFunction(function, 0, func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) {
		switch processed.Add(1) {
		case 1:
			close(entered)
			<-release
		case 33:
			select {
			case <-readDone:
			default:
				return nil, fmt.Errorf("activity read is still blocked after a full purge batch")
			}
		}
		return int64(1), nil
	}); err != nil {
		t.Fatal(err)
	}
	readFunction := function + "_read"
	if err := sqlite.RegisterScalarFunction(readFunction, 0, func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) {
		close(readDone)
		return int64(1), nil
	}); err != nil {
		t.Fatal(err)
	}
	app := newMobileBriefPurposeTestApp(t)
	store := app.activity.store
	seedRetentionBacklog(t, app, 33)
	trigger := "BEFORE DELETE ON activity_timeline_groups"
	if prepare {
		if _, err := store.db.Exec(`UPDATE activity_timeline_group_summaries SET title='Private research (Incognito)'`); err != nil {
			t.Fatal(err)
		}
		trigger = "BEFORE UPDATE OF triage_status ON activity_sessions"
	}
	if _, err := store.db.Exec("CREATE TRIGGER retention_yield " + trigger + " BEGIN SELECT " + function + "(); END"); err != nil {
		t.Fatal(err)
	}
	purgeDone := make(chan error, 1)
	go func() {
		purgeDone <- store.purgeTimelineRunsOlderThan(time.Now().UnixMilli())
	}()
	defer func() { releaseOnce.Do(func() { close(release) }); <-purgeDone }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("purge did not acquire the database connection")
	}
	waiting := store.db.Stats().WaitCount
	readResult := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go func() {
		var observed, remaining int
		err := store.db.QueryRowContext(ctx, "SELECT "+readFunction+"(), count(*) FROM activity_timeline_groups").Scan(&observed, &remaining)
		readResult <- err
	}()
	deadline := time.Now().Add(10 * time.Second)
	for store.db.Stats().WaitCount == waiting && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if store.db.Stats().WaitCount == waiting {
		t.Fatal("activity read did not queue behind purge")
	}
	releaseOnce.Do(func() { close(release) })
	err := <-purgeDone
	purgeDone <- err
	if err != nil {
		t.Fatalf("purge failed to yield between bounded batches: %v", err)
	}
	if err := <-readResult; err != nil {
		t.Fatal(err)
	}
	if processed.Load() != 33 {
		t.Fatalf("processed %d rows, want 33", processed.Load())
	}
}

func TestActivityRetentionPurgeCommitsCompletedBatchesBeforeFailure(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	store := app.activity.store
	seedRetentionBacklog(t, app, 33)
	if _, err := store.db.Exec(`CREATE TRIGGER stop_later_retention_batch BEFORE DELETE ON activity_timeline_groups
        WHEN OLD.id='backlog-032' BEGIN SELECT RAISE(ABORT, 'later batch failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := store.purgeTimelineRunsOlderThan(time.Now().UnixMilli()); err == nil {
		t.Fatal("expected second batch failure")
	}
	var remaining, receipts, summaries int
	for query, target := range map[string]*int{
		`SELECT count(*) FROM activity_timeline_groups`:                                       &remaining,
		`SELECT count(*) FROM activity_retention_sources_v1 WHERE source_kind='activity_run'`: &receipts,
		`SELECT count(*) FROM activity_timeline_group_summaries`:                              &summaries,
	} {
		if err := store.db.QueryRow(query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if remaining != 1 || receipts != 32 || summaries != 1 {
		t.Fatalf("completed batch was not committed independently: remaining=%d receipts=%d summaries=%d", remaining, receipts, summaries)
	}
}

func TestActivityRetentionPrivacyCrossesBatchBoundaries(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	store := app.activity.store
	seedRetentionBacklog(t, app, 65)
	// Privacy must travel backwards through more than one batch before any
	// membership is discarded, including a run connected only transitively.
	for runID, members := range map[string][]string{
		"backlog-032": {"s-backlog-000", "s-backlog-032"},
		"backlog-064": {"s-backlog-032", "s-backlog-064"},
	} {
		raw, _ := json.Marshal(members)
		if _, err := store.db.Exec(`UPDATE activity_timeline_groups SET session_ids=? WHERE id=?`, string(raw), runID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.db.Exec(`UPDATE activity_timeline_groups SET evidence_excluded=1 WHERE id='backlog-064'`); err != nil {
		t.Fatal(err)
	}
	if err := store.purgeTimelineRunsOlderThan(time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"backlog-000", "backlog-032", "backlog-064"} {
		receipt, err := store.getActivityRetentionSource("activity_run", id)
		if err != nil || receipt == nil || !receipt.Excluded {
			t.Fatalf("privacy lost across batches for %s: %+v, %v", id, receipt, err)
		}
	}
}

func TestActivityRetentionIgnoresExpiredOrUnreadablePauseHistory(t *testing.T) {
	for _, history := range []string{"missing", "unreadable", "stale", "pruned"} {
		t.Run(history, func(t *testing.T) {
			app := newMobileBriefPurposeTestApp(t)
			store := app.activity.store
			at := time.Now().Add(-45 * 24 * time.Hour)
			safe := seedMobileBriefPurposeRun(t, app, "safe-pause-history", 1, at, 1000, []string{"Editor"})
			protected := seedMobileBriefPurposeRun(t, app, "protected-pause-history", 2, at, 1000, []string{"Editor"})
			if _, err := store.db.Exec(`UPDATE activity_sessions SET triage_status='protected' WHERE id=?`, protected); err != nil {
				t.Fatal(err)
			}
			oldPause := []observationPause{{Start: at.Add(-time.Minute).UnixMilli(), End: at.UnixMilli()}}
			switch history {
			case "unreadable":
				if err := app.store.SetUIState(context.Background(), observationPausesKey, "not-json"); err != nil {
					t.Fatal(err)
				}
			case "stale":
				app.saveObservationPauses(oldPause)
			case "pruned":
				app.saveObservationPauses(nextObservationPauses(oldPause, true, time.Now()))
			}
			rotator := NewStorageRotator(store, DefaultRotatorConfig())
			rotator.purgeExpiredSessions(rotator.snapshotConfig())
			for id, wantExcluded := range map[string]bool{safe: false, protected: true} {
				receipt, err := store.getActivityRetentionSource("activity_session", id)
				if err != nil || receipt == nil || receipt.Excluded != wantExcluded {
					t.Fatalf("%s pause history changed persisted exclusion for %s: %+v, %v", history, id, receipt, err)
				}
			}
		})
	}
}

func TestActivityRetentionPurgeRechecksPrivacyBeforeDeletingBatch(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	store := app.activity.store
	seedRetentionBacklog(t, app, 65)
	for runID, members := range map[string][]string{
		"backlog-032": {"s-backlog-000", "s-backlog-032"},
		"backlog-064": {"s-backlog-032", "s-backlog-064"},
	} {
		raw, _ := json.Marshal(members)
		if _, err := store.db.Exec(`UPDATE activity_timeline_groups SET session_ids=? WHERE id=?`, string(raw), runID); err != nil {
			t.Fatal(err)
		}
	}
	cutoff := time.Now().UnixMilli()
	if err := store.prepareTimelineRetention(cutoff); err != nil {
		t.Fatal(err)
	}
	// A real writer acquires the released connection after preparation.
	if _, err := store.db.Exec(`UPDATE activity_sessions SET triage_status='protected' WHERE id='s-backlog-000'`); err != nil {
		t.Fatal(err)
	}
	deleted, changed, err := store.purgeTimelineRetentionBatch(cutoff)
	if err != nil || deleted != 0 || !changed {
		t.Fatalf("late privacy change discarded membership: deleted=%d changed=%v error=%v", deleted, changed, err)
	}
	var remaining int
	if err := store.db.QueryRow(`SELECT count(*) FROM activity_timeline_groups`).Scan(&remaining); err != nil || remaining != 65 {
		t.Fatalf("privacy restart deleted runs: remaining=%d error=%v", remaining, err)
	}
	if err := store.purgeTimelineRunsOlderThan(cutoff); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"backlog-000", "backlog-032", "backlog-064"} {
		receipt, err := store.getActivityRetentionSource("activity_run", id)
		if err != nil || receipt == nil || !receipt.Excluded {
			t.Fatalf("late privacy change failed to cross pages for %s: %+v, %v", id, receipt, err)
		}
	}
}

func TestActivityRetentionLateSummaryCannotRestorePurgedContent(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	store := app.activity.store
	seedRetentionBacklog(t, app, 1)
	if err := store.purgeTimelineRunsOlderThan(time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	// A model request started before retention can complete after the run
	// has gone. Its delayed result must not recreate the expired content.
	if err := store.UpsertTimelineGroupSummary(
		ActivityTimelineGroup{ID: "backlog-000"},
		ActivityTimelineGroupSummary{Title: "Expired private title", Description: "Expired private summary"},
		"test", "expired private prompt", "expired private response"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT count(*) FROM activity_timeline_group_summaries WHERE group_id='backlog-000'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("late model result restored expired summary content: count=%d error=%v", count, err)
	}
}
