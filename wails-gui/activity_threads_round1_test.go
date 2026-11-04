package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"modernc.org/sqlite"
)

func TestActivityThreadConfirmationDoesNotSkipModelReview(t *testing.T) {
	app, _, second, dayMs := seedActivityThreadEvidenceFixture(t)
	original := threadOf(t, app, second.ID)
	if err := app.ConfirmActivityThreadLink(original); err != nil {
		t.Fatal(err)
	}
	var reviewed int
	if err := app.activity.store.reader().QueryRow(`SELECT reviewed FROM activity_thread_members WHERE activity_id=?`, second.ID).Scan(&reviewed); err != nil || reviewed != 0 {
		t.Fatalf("confirmation changed model review: %d, %v", reviewed, err)
	}
	runner := &threadTestRunner{reply: threadReply(map[string]threadAnswer{
		"Review isoform activity code": {thread: "new1", kind: "personal", confidence: "high", title: "Personal research"},
	})}
	app.runner = runner
	if _, err := app.runActivityThreadPass(t.Context(), dayMs, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(runner.prompts) != 1 || !strings.Contains(runner.prompts[0], second.Title) {
		t.Fatalf("confirmed rule join skipped model: %v", runner.prompts)
	}
	if got := threadOf(t, app, second.ID); got == original {
		t.Fatalf("confirmation froze a model correction in %s", got)
	}
	view := activityThreadViewForMoment(t, app, dayMs, second.ID)
	if view.Reviewed {
		t.Fatal("model reassignment inherited user confirmation")
	}
}

func TestActivityThreadModelReviewIsNotUserConfirmation(t *testing.T) {
	app, _, second, dayMs := seedActivityThreadEvidenceFixture(t)
	id := threadOf(t, app, second.ID)
	if _, err := app.activity.store.db.Exec(`UPDATE activity_thread_members SET reviewed=1 WHERE thread_id=?`, id); err != nil {
		t.Fatal(err)
	}
	if view := activityThreadViewForMoment(t, app, dayMs, second.ID); view.Reviewed {
		t.Fatal("model-reviewed thread appears user-confirmed")
	}
	if err := app.ConfirmActivityThreadLink(id); err != nil {
		t.Fatal(err)
	}
	if view := activityThreadViewForMoment(t, app, dayMs, second.ID); !view.Reviewed {
		t.Fatal("user confirmation missing")
	}
	if _, err := app.MoveActivityToThread(second.ID, ""); err != nil {
		t.Fatal(err)
	}
	got, err := app.GetDayThreads(dayMs, dayMs+activityThreadDayMs-1)
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range got.Threads {
		if view.Reviewed {
			t.Fatal("changed membership retained user confirmation")
		}
	}
}

func TestActivityThreadRenameExamplesOnlyAffectNamingBlock(t *testing.T) {
	input := threadPromptInput{Moments: []threadMoment{{ID: "one", Title: "Read project plan"}}, Corrections: []threadCorrectionExample{{MomentTitle: "A", FromTitle: "B", ToTitle: "C"}}}
	before, aliasesBefore := buildThreadPrompt(input)
	input.Corrections = append(input.Corrections, threadCorrectionExample{RenamedTitle: "Personal errands"})
	after, aliasesAfter := buildThreadPrompt(input)
	start := strings.Index(after, "\nNaming examples only.")
	if start < 0 || !strings.Contains(after[start:], "Ignore them when deciding membership, kind or merges") {
		t.Fatal("missing naming-only boundary")
	}
	if !strings.Contains(after[start:], `renamed a thread to "Personal errands"`) || strings.Contains(after[:start], "Personal errands") {
		t.Fatal("rename leaked into membership examples")
	}
	boundary := strings.Index(before, "\nReply with JSON only:")
	if before[:boundary] != after[:start] || !reflect.DeepEqual(aliasesBefore, aliasesAfter) {
		t.Fatal("rename changed membership prompt or aliases")
	}
}

// Count real SQLite operations, without substituting query results or stores.
type activityThreadQueryConnector struct {
	path  string
	count *atomic.Int64
}

func (c activityThreadQueryConnector) Connect(context.Context) (driver.Conn, error) {
	d := &sqlite.Driver{}
	conn, err := d.Open(c.path)
	if err != nil {
		return nil, err
	}
	return activityThreadCountedConn{Conn: conn, count: c.count}, nil
}
func (c activityThreadQueryConnector) Driver() driver.Driver { return &sqlite.Driver{} }

type activityThreadCountedConn struct {
	driver.Conn
	count *atomic.Int64
}

func (c activityThreadCountedConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	c.count.Add(1)
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, q, args)
}
func (c activityThreadCountedConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	c.count.Add(1)
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, q, args)
}

func instrumentActivityThreadQueries(t *testing.T, s *ActivityStore) *atomic.Int64 {
	t.Helper()
	var seq int
	var name, path string
	if err := s.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	count := &atomic.Int64{}
	writer := sql.OpenDB(activityThreadQueryConnector{path: path, count: count})
	writer.SetMaxOpenConns(1)
	reader := sql.OpenDB(activityThreadQueryConnector{path: path, count: count})
	reader.SetMaxOpenConns(4)
	oldDB, oldRead, oldWriter := s.db, s.readDB, s.readWriter
	s.db, s.readDB, s.readWriter = writer, reader, writer
	t.Cleanup(func() { s.db, s.readDB, s.readWriter = oldDB, oldRead, oldWriter; reader.Close(); writer.Close() })
	return count
}

func TestActivityThreadDayReadQueryBudget(t *testing.T) {
	for _, size := range []int{60, 600} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			app := newThreadTestApp(t)
			day, dayMs := threadTestDay(t)
			ctx := t.Context()
			for i := 0; i < size; i++ {
				start := day.Add(8*time.Hour + time.Duration(i)*30*time.Second)
				row := seedThreadMoment(t, app, threadFixtureMoment{id: fmt.Sprintf("moment-%d", i), title: "Read project plan", app: "Claude", start: start, end: start.Add(20 * time.Second)})
				tx, err := app.activity.store.db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				thread := fmt.Sprintf("work-%d", i%12)
				if err := insertActivityThread(ctx, tx, thread, dayMs, "Work", nil, threadKindWork, row.EndMs); err != nil {
					t.Fatal(err)
				}
				if err := upsertActivityThreadMember(ctx, tx, row.ID, thread, dayMs, threadSourceRule, "obj:plan", false, row.EndMs); err != nil {
					t.Fatal(err)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
			}
			counter := instrumentActivityThreadQueries(t, app.activity.store)
			if _, err := app.buildActivityThreadDay(ctx, dayMs); err != nil {
				t.Fatal(err)
			} // warm existing privacy source caches
			counter.Store(0)
			at := time.Now()
			base, err := app.buildActivityThreadDay(ctx, dayMs)
			if err != nil {
				t.Fatal(err)
			}
			baseQueries, baseTime := counter.Load(), time.Since(at)
			counter.Store(0)
			at = time.Now()
			full, err := app.buildDayThreads(ctx, dayMs)
			if err != nil {
				t.Fatal(err)
			}
			fullQueries, fullTime := counter.Load(), time.Since(at)
			if len(full.Threads) != len(base.Threads) || len(full.Threads[0].Stretches) == 0 {
				t.Fatal("query budget passed without projection")
			}
			if fullQueries-baseQueries > 3 {
				t.Fatalf("day extras grew with rows: base=%d full=%d", baseQueries, fullQueries)
			}
			for i, view := range full.Threads {
				if !reflect.DeepEqual(view.ActivityIDs, base.Threads[i].ActivityIDs) || view.TimeSpentMs != base.Threads[i].TimeSpentMs {
					t.Fatal("projection changed grouping or time")
				}
			}
			activityThreadCacheForget(dayMs)
			if _, err := app.GetDayThreads(dayMs, dayMs+activityThreadDayMs-1); err != nil {
				t.Fatal(err)
			}
			counter.Store(0)
			at = time.Now()
			for i := 0; i < 5; i++ {
				if _, err := app.GetDayThreads(dayMs, dayMs+activityThreadDayMs-1); err != nil {
					t.Fatal(err)
				}
			}
			warmQueries, warmTime := counter.Load(), time.Since(at)/5
			if warmQueries != 10 {
				t.Fatalf("warm reads scanned rows: queries=%d for five reads", warmQueries)
			}
			t.Logf("moments=%d threads=%d base=%d SQL/%s full=%d SQL/%s extras=%d warm=%d SQL/read %s/read", size, len(full.Threads), baseQueries, baseTime, fullQueries, fullTime, fullQueries-baseQueries, warmQueries/5, warmTime)
			if _, err := app.activity.store.db.Exec(`UPDATE activities SET thread_outcome='{"inputHash":"invalid","outcome":{"version":2}}'`); err != nil {
				t.Fatal(err)
			}
			counter.Store(0)
			if _, err := app.buildActivityThreadDay(ctx, dayMs); err != nil {
				t.Fatal(err)
			}
			outcomeBase := counter.Load()
			counter.Store(0)
			if _, err := app.buildDayThreads(ctx, dayMs); err != nil {
				t.Fatal(err)
			}
			outcomeFull := counter.Load()
			if outcomeFull-outcomeBase != 7 {
				t.Fatalf("outcome day batching changed: base=%d full=%d", outcomeBase, outcomeFull)
			}
			t.Logf("moments=%d with recorded outcomes: base=%d full=%d extras=%d", size, outcomeBase, outcomeFull, outcomeFull-outcomeBase)

		})
	}
}

func TestActivityThreadDayOutcomesMatchOrdinaryReader(t *testing.T) {
	for _, fixture := range threadOutcomeFixtures(t) {
		t.Run(fixture.Name, func(t *testing.T) {
			app := newThreadTestApp(t)
			day, _ := threadTestDay(t)
			row := seedThreadFixture(t, app, "outcome", fixture, at(day, 12, 0))
			row = saveFixtureOutcome(t, app, row, fixture)
			// The ordinary base privacy read populates source facts shared by the batch.
			readable := app.filterComposerModelCandidates(app.activity.store, []ActivityRow{row})
			evidence, err := app.activityThreadDayEvidence(t.Context(), readable)
			if err != nil {
				t.Fatal(err)
			}
			batch := app.activityThreadDayOutcomes(t.Context(), readable, evidence)
			got, want := batch.readable(row), app.readableThreadOutcome(t.Context(), row)
			a, _ := json.Marshal(got)
			b, _ := json.Marshal(want)
			if string(a) != string(b) {
				t.Fatalf("batch outcome differs: got=%s want=%s", a, b)
			}
		})
	}
}

func TestActivityThreadLinkStateMigrationPreservesModelReview(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old-threads.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateActivityThreads(tx); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO activity_threads(id,day_start_ms,user_title,created_at,updated_at) VALUES('old',1,'My title',1,1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO activity_thread_members(activity_id,thread_id,day_start_ms,source,reviewed,updated_at) VALUES('moment','old',1,'ai',1,1)`); err != nil {
		t.Fatal(err)
	}
	// Simulate an interrupted upgrade after the first additive column.
	if _, err := tx.Exec(`ALTER TABLE activity_threads ADD COLUMN user_confirmed INTEGER NOT NULL DEFAULT 0`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		tx, err = db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := migrateActivityThreadLinkState(tx); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	var title, basis string
	var reviewed, confirmed int
	if err := db.QueryRow(`SELECT t.user_title,t.reason_basis,t.user_confirmed,m.reviewed FROM activity_threads t JOIN activity_thread_members m ON m.thread_id=t.id`).Scan(&title, &basis, &confirmed, &reviewed); err != nil {
		t.Fatal(err)
	}
	if title != "My title" || basis != "" || confirmed != 0 || reviewed != 1 {
		t.Fatalf("upgrade changed old state: title=%s basis=%s confirmed=%d reviewed=%d", title, basis, confirmed, reviewed)
	}
	if _, err := db.Exec(`UPDATE activity_threads SET user_confirmed=1`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.QueryRow(`SELECT user_confirmed FROM activity_threads WHERE id='old'`).Scan(&confirmed); err != nil || confirmed != 1 {
		t.Fatalf("confirmation did not survive reopen: %d %v", confirmed, err)
	}
	if _, err := db.Exec(`INSERT INTO activity_thread_members(activity_id,thread_id,day_start_ms,source,reviewed,updated_at) VALUES('new','old',1,'rule',0,2)`); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT user_confirmed FROM activity_threads WHERE id='old'`).Scan(&confirmed); err != nil || confirmed != 0 {
		t.Fatalf("new member did not clear confirmation: %d %v", confirmed, err)
	}
	if err := db.QueryRow(`SELECT reviewed FROM activity_thread_members WHERE activity_id='moment'`).Scan(&reviewed); err != nil || reviewed != 1 {
		t.Fatalf("trigger changed model review: %d %v", reviewed, err)
	}
}

func TestActivityThreadReasonBasisIsSharedWithoutReauthorizingOldText(t *testing.T) {
	app := newThreadTestApp(t)
	day, dayMs := threadTestDay(t)
	late := seedThreadMoment(t, app, threadFixtureMoment{id: "late", title: "Late moment", app: "Claude", start: at(day, 10, 0), end: at(day, 10, 5)})
	early := seedThreadMoment(t, app, threadFixtureMoment{id: "early", title: "Early moment", app: "Claude", start: at(day, 9, 0), end: at(day, 9, 5)})
	app.runner = &threadTestRunner{reply: threadReply(map[string]threadAnswer{
		"Late moment":  {thread: "new1", kind: "work", confidence: "high", title: "Project"},
		"Early moment": {thread: "new2", kind: "work", confidence: "high", title: "Project"},
	})}
	if _, err := app.runActivityThreadPass(t.Context(), dayMs, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if view := activityThreadViewForMoment(t, app, dayMs, late.ID); view.LinkReason.Text != "test" {
		t.Fatalf("initial model reason missing: %+v", view.LinkReason)
	}
	thread := threadOf(t, app, late.ID)
	if err := app.RenameActivityThread(thread, "New user title"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.activity.store.db.Exec(`UPDATE activity_thread_members SET source='rule',reason='obj:project',reviewed=0 WHERE activity_id=?`, early.ID); err != nil {
		t.Fatal(err)
	}
	app.runner = &threadTestRunner{reply: threadReply(map[string]threadAnswer{
		"Early moment": {thread: "thread:Late moment", kind: "work", confidence: "high", title: "Project"},
	})}
	if _, err := app.runActivityThreadPass(t.Context(), dayMs, true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if view := activityThreadViewForMoment(t, app, dayMs, late.ID); view.LinkReason.Text != "" {
		t.Fatalf("new shared basis reauthorized old text: %+v", view.LinkReason)
	}
	rows, err := app.activity.store.reader().Query(`SELECT reason FROM activity_thread_members WHERE source='ai'`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &fields); err != nil {
			t.Fatal(err)
		}
		if _, ok := fields["titles"]; ok {
			t.Fatal("title map copied into member")
		}
	}
	if err := firstErr(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := app.activity.store.reader().QueryRow(`SELECT reason_basis FROM activity_threads WHERE id=?`, thread).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var basis activityThreadStoredReason
	if err := json.Unmarshal([]byte(raw), &basis); err != nil || len(basis.Titles) == 0 {
		t.Fatalf("thread basis not stored once: %s %v", raw, err)
	}
}

func TestActivityThreadFileWithdrawalBypassesWarmCache(t *testing.T) {
	app, first, _, day := seedActivityThreadEvidenceFixture(t)
	path := filepath.Join(t.TempDir(), "transcript.md")
	session := "session-" + first.ID
	valid := withTranscriptActivitySources("# Call\n- **Session**: "+session+"\n\n## Transcript\nhello", []string{session})
	if err := os.WriteFile(path, []byte(valid), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.activity.store.db.Exec(`UPDATE activity_sessions SET transcript_path=? WHERE id=?`, path, session); err != nil {
		t.Fatal(err)
	}
	before, err := app.GetDayThreads(day, day+activityThreadDayMs-1)
	if err != nil || len(before.Threads) == 0 {
		t.Fatalf("initial read: %+v %v", before, err)
	}
	for i := 0; i < 2; i++ {
		if _, err := app.GetDayThreads(day, day+activityThreadDayMs-1); err != nil {
			t.Fatal(err)
		}
	}
	// No database write: only the recording's dependency header changes.
	if err := os.WriteFile(path, []byte(strings.Replace(valid, `["`+session+`"]`, `["missing-session"]`, 1)), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := app.GetDayThreads(day, day+activityThreadDayMs-1)
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range after.Threads {
		for _, id := range view.ActivityIDs {
			if id == first.ID {
				t.Fatal("withdrawn transcript remained visible on warm read")
			}
		}
	}
	if err := os.WriteFile(path, []byte(valid), 0600); err != nil {
		t.Fatal(err)
	}
	repaired, err := app.GetDayThreads(day, day+activityThreadDayMs-1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, repaired) {
		t.Fatalf("repaired file did not recover unchanged day: before=%+v after=%+v", before, repaired)
	}
}

func TestActivityThreadOutcomeBatchUsesAuthorizedRunEnvelope(t *testing.T) {
	app := newThreadTestApp(t)
	day, _ := threadTestDay(t)
	fixture := threadOutcomeFixtures(t)[0]
	row := saveFixtureOutcome(t, app, seedThreadFixture(t, app, "safe", fixture, at(day, 12, 0)), fixture)
	readable := app.filterComposerModelCandidates(app.activity.store, []ActivityRow{row})
	evidence, err := app.activityThreadDayEvidence(t.Context(), readable)
	if err != nil {
		t.Fatal(err)
	}
	baseline := app.activityThreadDayOutcomes(t.Context(), readable, evidence)
	if baseline.readable(row) == nil {
		t.Fatal("missing baseline outcome")
	}
	private := seedThreadFixture(t, app, "private", fixture, at(day, 12, 0))
	privateSession := "session-" + private.ID
	if _, err := app.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status='protected' WHERE id=?`, privateSession); err != nil {
		t.Fatal(err)
	}
	// Simulate a concurrent regroup after metadata authorization, before tails.
	if _, err := app.activity.store.db.Exec(`UPDATE activity_timeline_groups SET session_ids=? WHERE id=?`, composeJSON([]string{privateSession}), composeParseStrings(row.RunIDs)[0]); err != nil {
		t.Fatal(err)
	}
	got := app.activityThreadDayOutcomes(t.Context(), readable, evidence)
	if got == nil {
		t.Fatal("batch unavailable")
	}
	if !reflect.DeepEqual(got.snapshots, baseline.snapshots) || !reflect.DeepEqual(got.events, baseline.events) {
		t.Fatal("tail reads followed the changed live run instead of the authorized envelope")
	}
}

func TestActivityThreadResumeRequiresMatchingSourceRevision(t *testing.T) {
	app, first, _, _ := seedActivityThreadEvidenceFixture(t)
	setActivityThreadEvidence(t, app, first, false, ActivityEvidenceToken{Kind: "path", Value: "src/main.go"})
	readable := app.filterComposerModelCandidates(app.activity.store, []ActivityRow{first})
	if len(readable) != 1 {
		t.Fatal("missing baseline source")
	}
	if _, err := app.activity.store.db.Exec(`UPDATE activity_events SET data='{"windowTitle":"Private - Incognito"}' WHERE session_id=?`, "session-"+first.ID); err != nil {
		t.Fatal(err)
	}
	evidence, err := app.activityThreadDayEvidence(t.Context(), readable)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.allowed[composeParseStrings(first.RunIDs)[0]] {
		t.Fatal("source changed after authorization but resume evidence remained allowed")
	}
}

func TestActivityThreadPurposeInputsWithholdDerivedExplanation(t *testing.T) {
	app := newThreadTestApp(t)
	day, dayMs := threadTestDay(t)
	now := time.Now()
	store := NewPurposeMapStore(app.activity.store.db)
	input := purposeTestCreateInput("thread-purpose", PurposeKindGoal, "plan", []PurposeEvidenceInput{userPurposeEvidence("thread-purpose", "chat", PurposeStanceSupport, "Prepare planning", now.Add(-time.Hour).UnixMilli())}, now.Add(-time.Hour).UnixMilli())
	input.Title = "Confidential planning"
	created, _, err := store.Create(input)
	if err != nil {
		t.Fatal(err)
	}
	if goals := app.activityThreadGoals(t.Context(), now); len(goals) != 1 {
		t.Fatalf("missing real Purpose input: %+v", goals)
	}
	seedThreadMoment(t, app, threadFixtureMoment{id: "plan", title: "Read project plan", app: "Notes", start: at(day, 9, 0), end: at(day, 9, 5)})
	runner := &threadTestRunner{reply: threadReply(map[string]threadAnswer{"Read project plan": {thread: "new1", kind: "work", confidence: "high", title: "Planning"}})}
	app.runner = runner
	if _, err := app.runActivityThreadPass(t.Context(), dayMs, true, now); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(runner.prompts[0], input.Title) {
		t.Fatal("Purpose grouping input changed")
	}
	view := activityThreadViewForMoment(t, app, dayMs, "plan")
	if view.LinkReason.Kind != "model" || view.LinkReason.Text != "" {
		t.Fatalf("unprovenanced Purpose reason published: %+v", view.LinkReason)
	}
	detail, err := store.GetDetail(created.OpenLoop.ID)
	if err != nil {
		t.Fatal(err)
	}
	update := purposeTestAppendInput(detail, "rename-purpose", PurposeEventRevised, PurposeStanceUpdate, PurposeEvidenceExplicitUser, "revision", now.UnixMilli())
	update.Title = "Revised planning"
	update.Evidence = []PurposeEvidenceInput{userPurposeEvidence("rename-purpose", "chat", PurposeStanceUpdate, "Change planning", now.UnixMilli())}
	if _, _, err := store.AppendEvent(update); err != nil {
		t.Fatal(err)
	}
	if view := activityThreadViewForMoment(t, app, dayMs, "plan"); view.LinkReason.Text != "" {
		t.Fatal("old reason resurfaced after Purpose mutation")
	}
	threads, _, err := app.activity.store.loadActivityThreads(t.Context(), dayMs)
	if err != nil {
		t.Fatal(err)
	}
	for i := range threads {
		threads[i].Members = []string{"plan"}
	}
	if activityThreadReasonTitlesReadable(threads, map[string]ActivityRow{"plan": {ID: "plan"}}) {
		t.Fatal("Purpose-derived generated title could launder the old input into a later explanation")
	}
}

func TestActivityThreadRetitleRetainsUnsafeInputMarker(t *testing.T) {
	app, first, second, day := seedActivityThreadEvidenceFixture(t)
	id := threadOf(t, app, first.ID)
	// A model can keep a rule join and only retitle its thread. That path has
	// no model assignments, but the title still inherits the prompt's inputs.
	in := threadPromptInput{Threads: []threadState{{ID: id, Kind: threadKindWork, Members: []string{first.ID, second.ID}}}, Moments: []threadMoment{threadMomentFromRow(second)}, RuleJoined: map[string]string{second.ID: id}}
	aliases := threadPromptAliases{threadByAlias: map[string]string{"t1": id}, momentByAlias: map[string]string{"m1": second.ID}}
	resp := threadModelResponse{Moments: []threadModelMoment{{ID: "m1", Thread: "t1", Kind: threadKindWork, Confidence: "high"}}, Threads: []threadModelThread{{ID: "t1", Title: "Purpose-derived title", Kind: threadKindWork}}}
	tx, err := app.activity.store.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := applyThreadModel(t.Context(), tx, day, in, aliases, resp, map[string]*activityThreadRow{id: {threadState: threadState{ID: id, Members: []string{first.ID, second.ID}}}}, map[string]ActivityRow{first.ID: first, second.ID: second}, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	threads, _, err := app.activity.store.loadActivityThreads(t.Context(), day)
	if err != nil {
		t.Fatal(err)
	}
	for _, thread := range threads {
		if thread.ID == id && (thread.Title != "Purpose-derived title" || !thread.ReasonBasis.UnsafeInputs) {
			t.Fatalf("retitle lost provenance marker: %+v", thread)
		}
	}
}

func TestActivityThreadBatchRechecksCachedDeniedSource(t *testing.T) {
	for _, reason := range []string{"private", "pause"} {
		t.Run(reason, func(t *testing.T) {
			app, first, _, _ := seedActivityThreadEvidenceFixture(t)
			day, _ := threadTestDay(t)
			other := seedThreadMoment(t, app, threadFixtureMoment{id: "denied", title: "Read other plan", app: "Notes", start: at(day, 11, 0), end: at(day, 11, 5)})
			if reason == "private" {
				if _, err := app.activity.store.db.Exec(`UPDATE activity_events SET data='{"privateWindow":true,"appName":"Notes","appBundleId":"com.apple.Notes"}' WHERE session_id='session-denied'`); err != nil {
					t.Fatal(err)
				}
			} else {
				app.saveObservationPauses([]observationPause{{Start: other.StartMs, End: other.EndMs}})
			}
			if got := app.filterComposerModelCandidates(app.activity.store, []ActivityRow{other}); len(got) != 0 {
				t.Fatal("denied source unexpectedly readable")
			}
			// Ensure denied facts really were cached before we change the run.
			session, err := app.activity.store.GetSessionByID("session-denied")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := app.activity.store.activitySessionProvenance(*session); err != nil {
				t.Fatal(err)
			}
			readable := app.filterComposerModelCandidates(app.activity.store, []ActivityRow{first})
			if len(readable) != 1 {
				t.Fatal("safe baseline denied")
			}
			if _, err := app.activity.store.db.Exec(`UPDATE activity_timeline_groups SET session_ids=? WHERE id=?`, composeJSON([]string{"session-denied"}), composeParseStrings(first.RunIDs)[0]); err != nil {
				t.Fatal(err)
			}
			evidence, err := app.activityThreadDayEvidence(t.Context(), readable)
			if err != nil {
				t.Fatal(err)
			}
			if evidence.allowed[composeParseStrings(first.RunIDs)[0]] {
				t.Fatal("cached denied source authorized after live envelope change")
			}
		})
	}
}

func TestActivityThreadOrphanCorrectionTitleRetainsUnsafeLineage(t *testing.T) {
	app, first, second, day := seedActivityThreadEvidenceFixture(t)
	id := threadOf(t, app, first.ID)
	if _, err := app.activity.store.db.Exec(`UPDATE activity_threads SET title='Purpose-derived orphan',title_basis=?,reason_basis=? WHERE id=?`, composeJSON([]string{first.ID, second.ID}), composeJSON(activityThreadStoredReason{Version: 2, UnsafeInputs: true}), id); err != nil {
		t.Fatal(err)
	}
	if _, err := app.activity.store.db.Exec(`DELETE FROM activity_thread_members WHERE thread_id=?`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := app.activity.store.db.Exec(`INSERT INTO activity_thread_corrections(id,activity_id,kind,from_thread_id,from_members,to_thread_id,created_at) VALUES('orphan',?,'own',?,?,'',1)`, second.ID, id, composeJSON([]string{first.ID, second.ID})); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]ActivityRow{first.ID: first, second.ID: second}
	fixes, err := app.activity.store.activityThreadCorrectionExamples(t.Context(), allowed)
	if err != nil || len(fixes) != 1 || fixes[0].FromTitle != "Purpose-derived orphan" {
		t.Fatalf("missing real orphan correction input: %+v %v", fixes, err)
	}
	threads, _, err := app.activity.store.loadActivityThreads(t.Context(), day)
	if err != nil {
		t.Fatal(err)
	}
	if activityThreadReasonTitlesReadable(threads, allowed) {
		t.Fatal("unsafe empty-thread title accepted as an explanation input")
	}
}

func TestPausedBriefActivityCandidateRechecksProtectedSession(t *testing.T) {
	app := newThreadTestApp(t)
	day, _ := threadTestDay(t)
	fixture := threadOutcomeFixtures(t)[0]
	row := saveFixtureOutcome(t, app, seedThreadFixture(t, app, "safe", fixture, at(day, 12, 0)), fixture)
	app.closedEyes.enabled.Store(true)
	t.Cleanup(func() { app.closedEyes.enabled.Store(false) })
	ctx := context.WithValue(t.Context(), pausedBriefReadKey{}, true)
	if got := app.filterComposerModelCandidatesContext(ctx, app.activity.store, []ActivityRow{row}); len(got) != 1 {
		t.Fatal("selected brief could not recheck its activity")
	}
	if got := app.filterComposerModelCandidates(app.activity.store, []ActivityRow{row}); len(got) != 0 {
		t.Fatal("ordinary paused read exposed activity")
	}
	ids := composeParseStrings(row.RunIDs)
	if len(ids) == 0 {
		t.Fatal("fixture has no runs")
	}
	run, err := app.activity.store.GetTimelineRunByID(ids[0])
	if err != nil || run == nil || len(run.SessionIDs) == 0 {
		t.Fatal("fixture has no session")
	}
	if _, err := app.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status='protected' WHERE id=?`, run.SessionIDs[0]); err != nil {
		t.Fatal(err)
	}
	if got := app.filterComposerModelCandidatesContext(ctx, app.activity.store, []ActivityRow{row}); len(got) != 0 {
		t.Fatal("protected activity survived paused proof")
	}
}

func TestPausedBriefProofGrantDoesNotEscapePolicyCache(t *testing.T) {
	app := newThreadTestApp(t)
	app.closedEyes.enabled.Store(true)
	t.Cleanup(func() { app.closedEyes.enabled.Store(false) })
	cache := newPeopleReadGateCache()
	ctx := context.WithValue(t.Context(), pausedBriefReadKey{}, true)
	scoped, err := app.activityReadPrivacyPolicyContextWithPeopleCache(ctx, cache)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := app.activityReadPrivacyPolicyContextWithPeopleCache(t.Context(), cache)
	if err != nil {
		t.Fatal(err)
	}
	source := activitySourceProvenance{StartTime: 1, EndTime: 2}
	if !scoped.allows(source) || plain.allows(source) {
		t.Fatal("paused brief grant escaped its proof context through the policy cache")
	}
}

func TestComposerBatchMatchesIndividualPrivacyChecks(t *testing.T) {
	app := newThreadTestApp(t)
	day, _ := threadTestDay(t)
	fixture := threadOutcomeFixtures(t)[0]
	row := saveFixtureOutcome(t, app, seedThreadFixture(t, app, "batch", fixture, at(day, 12, 0)), fixture)
	missing := row
	missing.RunIDs = composeJSON([]string{"missing-run"})
	candidates := []ActivityRow{row, row, missing}
	compare := func() {
		t.Helper()
		var expected []ActivityRow
		for _, candidate := range candidates {
			if composerCandidateAllowsModelRead(candidate, func(id string) bool {
				run, err := app.activity.store.GetTimelineRunByIDContext(t.Context(), id)
				return err == nil && run != nil && app.activityTimelineRunAllowsModelReadContext(t.Context(), app.activity.store, *run)
			}) {
				expected = append(expected, candidate)
			}
		}
		got := app.filterComposerModelCandidatesContext(t.Context(), app.activity.store, candidates)
		if len(got) != len(expected) {
			t.Fatalf("batch returned %d candidates, individual checks returned %d", len(got), len(expected))
		}
	}
	compare()
	run, err := app.activity.store.GetTimelineRunByID(composeParseStrings(row.RunIDs)[0])
	if err != nil || run == nil || len(run.SessionIDs) == 0 {
		t.Fatal("fixture has no source session")
	}
	if _, err := app.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status='protected' WHERE id=?`, run.SessionIDs[0]); err != nil {
		t.Fatal(err)
	}
	compare()
}
