package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
)

// This connector delegates every operation to the real SQLite driver. It
// counts common-evidence scans without changing query results or fixtures.
type activityEvidenceCountingConnector struct {
	driver driver.Driver
	path   string
	count  *atomic.Int64
}

func (c activityEvidenceCountingConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.driver.Open(c.path)
	if err != nil {
		return nil, err
	}
	return activityEvidenceCountingConn{Conn: conn, count: c.count}, nil
}

func (c activityEvidenceCountingConnector) Driver() driver.Driver { return c.driver }

type activityEvidenceCountingConn struct {
	driver.Conn
	count *atomic.Int64
}

func (c activityEvidenceCountingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if strings.HasPrefix(strings.TrimSpace(query), "SELECT l.activity_id, g.id, g.app_key, g.app_name, g.session_ids, g.evidence_tokens") {
		c.count.Add(1)
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func countActivityEvidenceScans(t *testing.T, store *ActivityStore) *atomic.Int64 {
	t.Helper()
	original := store.db
	var sequence int
	var name, path string
	if err := original.QueryRow(`PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	count := &atomic.Int64{}
	counting := sql.OpenDB(activityEvidenceCountingConnector{driver: original.Driver(), path: path, count: count})
	store.db = counting
	t.Cleanup(func() {
		store.db = original
		_ = counting.Close()
	})
	return count
}

func TestActivityBindingsShareCommonEvidenceAcrossCardsLinksAndReceipts(t *testing.T) {
	store := newActivityEvidenceTestStore(t)
	const sharedPath = "~/.sample-agent/settings.json"
	const taskCount = 6
	const runsPerTask = 3
	var lastEnd int64
	for task := range taskCount {
		activityID := fmt.Sprintf("memo-task-%d", task)
		uniquePath := fmt.Sprintf("/workspace/project-%d/result.json", task)
		for member := range runsPerTask {
			n := int64(task*runsPerTask + member + 1)
			runID := fmt.Sprintf("memo-run-%d", n)
			insertTestRun(t, store, runID, n, n*1000, "done", 1)
			tokens := extractActivityIdentifierTokens(sharedPath+" "+uniquePath, n*1000)
			if _, err := store.db.Exec(`UPDATE activity_timeline_groups SET evidence_tokens=? WHERE id=?`, composeJSON(tokens), runID); err != nil {
				t.Fatal(err)
			}
			run, err := store.GetTimelineRunByID(runID)
			if err != nil || run == nil {
				t.Fatalf("fixture run: %+v, %v", run, err)
			}
			outcomeTestLink(t, store, activityID, *run)
			lastEnd = max(lastEnd, run.EndMs)
			if _, err := store.db.Exec(`UPDATE activity_run_links SET link_reason='shared_path',link_token_kind='path',link_token_value=? WHERE run_id=?`, sharedPath, runID); err != nil {
				t.Fatal(err)
			}
			// The newest receipt uses a common token and must be filtered; the
			// earlier receipt keeps its distinct task identifier and stays visible.
			for receipt, path := range []string{uniquePath, sharedPath} {
				at := run.EndMs + int64(receipt)
				evidence := []ActivityOutcomeEvidence{{RunID: runID, SourceKind: "activity_event", SourceID: fmt.Sprint(receipt), ObservedAt: at, Tokens: extractActivityIdentifierTokens(path, at)}}
				if _, err := store.db.Exec(`INSERT INTO activity_outcomes(id,activity_id,run_id,status,reason,occurred_at,target,evidence,created_at) VALUES(?,?,?,'reported','message_sent',?,'Claude',?,?)`, fmt.Sprintf("memo-receipt-%d-%d", n, receipt), activityID, runID, at, composeJSON(evidence), at); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	count := countActivityEvidenceScans(t, store)
	app := &App{activity: &activityMonitorState{store: store}}
	views, err := app.GetActivitiesInRange(0, lastEnd)
	if err != nil || len(views) != taskCount {
		t.Fatalf("cards = %d, %v", len(views), err)
	}
	if got := count.Load(); got != 1 {
		t.Fatalf("list scanned common evidence %d times for %d cards with %d receipts", got, taskCount, taskCount*runsPerTask*2)
	}
	for _, view := range views {
		if view.LatestLink == nil || view.LatestLink.ReasonKind != "related_context" || view.LatestLink.Token != "" {
			t.Fatalf("common identifier explained a link: %+v", view.LatestLink)
		}
		if view.LatestOutcome == nil || view.LatestOutcome.Evidence[0].Tokens[0].Value == sharedPath {
			t.Fatalf("receipt filtering changed: %+v", view.LatestOutcome)
		}
	}
	count.Store(0)
	runs, err := app.GetActivityRuns("memo-task-5")
	if err != nil || len(runs) != runsPerTask {
		t.Fatalf("runs = %d, %v", len(runs), err)
	}
	if got := count.Load(); got != runsPerTask {
		t.Fatalf("drill-down scanned common evidence %d times for %d distinct run ends", got, runsPerTask)
	}
	for _, run := range runs {
		if run.Link == nil || run.Link.ReasonKind != "related_context" || run.Link.Token != "" {
			t.Fatalf("drill-down revived a common identifier: %+v", run.Link)
		}
	}
	// The cache lives only for this binding invocation. A later call must
	// re-read privacy changes instead of retaining the previous common map.
	if _, err := store.db.Exec(`UPDATE activity_timeline_groups SET evidence_excluded=1 WHERE id IN (SELECT run_id FROM activity_run_links WHERE activity_id!='memo-task-5')`); err != nil {
		t.Fatal(err)
	}
	count.Store(0)
	runs, err = app.GetActivityRuns("memo-task-5")
	if err != nil || len(runs) != runsPerTask {
		t.Fatalf("runs after privacy change = %d, %v", len(runs), err)
	}
	if count.Load() != runsPerTask || runs[0].Link == nil || runs[0].Link.Token != sharedPath {
		t.Fatalf("per-call evidence snapshot was stale: scans=%d link=%+v", count.Load(), runs[0].Link)
	}
}

func TestActivityBindingsSkipCommonEvidenceScanForEmptyHistory(t *testing.T) {
	store := newActivityEvidenceTestStore(t)
	count := countActivityEvidenceScans(t, store)
	app := &App{activity: &activityMonitorState{store: store}}
	views, err := app.GetActivitiesInRange(0, 1000)
	if err != nil || len(views) != 0 || count.Load() != 0 {
		t.Fatalf("empty history = %+v, %v, scans=%d", views, err, count.Load())
	}
}

func TestActivityDrillDownKeepsRunTimeLinkDecision(t *testing.T) {
	store := newActivityEvidenceTestStore(t)
	shared, distinct := "~/.sample-agent/records", "~/.sample-agent/specific.json"
	add := func(id, task string, n, at int64, paths string) ActivityTimelineRunRow {
		insertTestRun(t, store, id, n, at, "done", 1)
		tokens := extractActivityIdentifierTokens(paths, at)
		if _, err := store.db.Exec(`UPDATE activity_timeline_groups SET evidence_tokens=? WHERE id=?`, composeJSON(tokens), id); err != nil {
			t.Fatal(err)
		}
		run, err := store.GetTimelineRunByID(id)
		if err != nil || run == nil {
			t.Fatalf("run=%+v err=%v", run, err)
		}
		outcomeTestLink(t, store, task, *run)
		if err := store.recordActivityLinkReason(task, *run); err != nil {
			t.Fatal(err)
		}
		return *run
	}
	add("start", "task", 1, 1000, shared+" "+distinct)
	early := add("early", "task", 2, 2000, shared)
	// Three other tasks make the identifier common only after this link was
	// decided. A later distinct member extends the owning activity's end.
	for i := range 3 {
		add(fmt.Sprintf("other-%d", i), fmt.Sprintf("other-task-%d", i), int64(i+3), int64(4000+i*1000), shared)
	}
	latest := add("latest", "task", 6, 8000, distinct)
	var reason, token string
	if err := store.db.QueryRow(`SELECT link_reason,link_token_value FROM activity_run_links WHERE run_id=?`, early.ID).Scan(&reason, &token); err != nil || reason != "shared_path" || token != shared {
		t.Fatalf("persisted early link=%s %s err=%v", reason, token, err)
	}
	app := &App{activity: &activityMonitorState{store: store}}
	views, err := app.GetActivitiesInRange(0, latest.EndMs)
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range views {
		if view.ID == "task" && (view.LatestLink == nil || view.LatestLink.ReasonKind != "shared_path" || view.LatestLink.Token != distinct) {
			t.Fatalf("list changed its latest persisted link: %+v", view.LatestLink)
		}
	}
	runs, err := app.GetActivityRuns("task")
	if err != nil || len(runs) != 3 {
		t.Fatalf("drill-down=%+v err=%v", runs, err)
	}
	for _, run := range runs {
		if run.ID == early.ID && (run.Link == nil || run.Link.ReasonKind != reason || run.Link.Token != token) {
			t.Fatalf("later unrelated evidence rewrote early link: %+v; persisted=%s %s", run.Link, reason, token)
		}
	}
}

func TestActivityDrillDownMemoizesEqualRunEndTimes(t *testing.T) {
	store := newActivityEvidenceTestStore(t)
	for i := range 3 {
		id := fmt.Sprintf("same-end-%d", i)
		insertTestRun(t, store, id, int64(i+1), int64(1000+i), "done", 1)
		if _, err := store.db.Exec(`UPDATE activity_timeline_groups SET end_ms=5000,evidence_tokens=? WHERE id=?`, composeJSON(extractActivityIdentifierTokens("~/.sample-agent/records", 5000)), id); err != nil {
			t.Fatal(err)
		}
		run, err := store.GetTimelineRunByID(id)
		if err != nil || run == nil {
			t.Fatalf("run=%+v err=%v", run, err)
		}
		outcomeTestLink(t, store, "task", *run)
		if err := store.recordActivityLinkReason("task", *run); err != nil {
			t.Fatal(err)
		}
	}
	count := countActivityEvidenceScans(t, store)
	app := &App{activity: &activityMonitorState{store: store}}
	runs, err := app.GetActivityRuns("task")
	if err != nil || len(runs) != 3 || count.Load() != 1 {
		t.Fatalf("equal-end maps were not reused: runs=%d scans=%d err=%v", len(runs), count.Load(), err)
	}
}
