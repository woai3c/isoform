package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/pii"
	"wails-gui/internal/platform/activitymon"
)

func TestActivityPrivacyCacheReusesLargeDaySourceFacts(t *testing.T) {
	isolateActivityPrivacyTest(t)
	app := newPrivacyReadTestApp(t)
	store := app.activity.store
	const date = "2026-04-20"
	day, _ := time.ParseInLocation("2006-01-02", date, time.Local)
	const sessions, eventsPerSession = 200, 100
	for i := 0; i < sessions; i++ {
		start := day.Add(time.Duration(i) * time.Minute).UnixMilli()
		row := ActivitySessionRow{ID: fmt.Sprintf("large-%03d", i), StartTime: start, EndTime: start + 60_000, AppNames: `["Code"]`, EventCount: eventsPerSession}
		events := make([]activitymon.ActivityEvent, eventsPerSession)
		for j := range events {
			events[j] = activitymon.ActivityEvent{Timestamp: start + int64(j)*500, AppName: "Code", WindowTitle: "Public project", TypedText: strings.Repeat("ordinary text ", 100)}
		}
		if err := store.SaveSession(row, events, nil); err != nil {
			t.Fatal(err)
		}
	}
	started := time.Now()
	if _, err := app.GetDailySummary(date); err != nil {
		t.Fatal(err)
	}
	cold := time.Since(started)
	if got := store.readPrivacySources.eventRows.Load(); got != sessions*eventsPerSession {
		t.Fatalf("cold read decoded %d privacy event rows, want %d", got, sessions*eventsPerSession)
	}
	if _, err := store.SaveSummary(date, "daily", "A public work day", "test", false); err != nil {
		t.Fatal(err)
	}
	beforeRows, beforeReads := store.readPrivacySources.eventRows.Load(), store.readPrivacySources.metadataReads.Load()
	started = time.Now()
	if _, err := app.GetDailySummary(date); err != nil {
		t.Fatal(err)
	}
	warm := time.Since(started)
	if got := store.readPrivacySources.eventRows.Load() - beforeRows; got != 0 {
		t.Fatalf("unchanged day decoded %d event rows again", got)
	}
	if got := store.readPrivacySources.metadataReads.Load() - beforeReads; got > 2*sessions {
		t.Fatalf("daily aggregations repeated source checks: %d metadata reads", got)
	}
	if _, err := app.GetWeeklySummary(date); err != nil {
		t.Fatal(err)
	}
	if got := store.readPrivacySources.eventRows.Load() - beforeRows; got != 0 {
		t.Fatalf("weekly read decoded %d unchanged event rows", got)
	}
	t.Logf("200 sessions x 100 events: cold=%s warm=%s privacy event rows cold=%d warm=0", cold, warm, beforeRows)
	if _, err := store.db.Exec(`UPDATE activity_events SET data=json_set(data,'$.privateWindow',json('true')) WHERE session_id='large-000'`); err != nil {
		t.Fatal(err)
	}
	result, err := app.GetDailySummary(date)
	if err != nil {
		t.Fatal(err)
	}
	if result.AISummary != "" || result.SessionCount != sessions-1 {
		t.Fatalf("updated source escaped cache invalidation: sessions=%d summary=%q", result.SessionCount, result.AISummary)
	}
	if got := store.readPrivacySources.eventRows.Load() - beforeRows; got != eventsPerSession {
		t.Fatalf("one changed source decoded %d rows, want %d", got, eventsPerSession)
	}
}

func TestActivityPrivacyCacheTracksRawSourceMutations(t *testing.T) {
	isolateActivityPrivacyTest(t)
	app := newPrivacyReadTestApp(t)
	store := app.activity.store
	row := ActivitySessionRow{ID: "source", StartTime: 100_000, EndTime: 160_000, AppNames: `["Code"]`}
	other := row
	other.ID = "other"
	for _, s := range []ActivitySessionRow{row, other} {
		if err := store.SaveSession(s, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	check := func(row ActivitySessionRow, want bool) {
		t.Helper()
		got, err := app.activitySessionAllowsModelReadResult(store, row)
		if err != nil || got != want {
			t.Fatalf("read %s: allowed=%t err=%v, want %t", row.ID, got, err, want)
		}
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := store.db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	check(row, true)
	check(other, true)
	exec(`INSERT INTO activity_events(session_id,timestamp,kind,data,sort_order) VALUES('source',100000,'focus','{"privateWindow":true}',0)`)
	check(row, false)
	exec(`UPDATE activity_events SET data='{"appName":"Code"}' WHERE session_id='source'`)
	check(row, true)
	exec(`UPDATE activity_events SET data='{"appBundleId":"com.example.private"}' WHERE session_id='source'`)
	check(row, false)
	exec(`UPDATE activity_events SET session_id='other' WHERE session_id='source'`)
	check(row, true)
	check(other, false)
	exec(`DELETE FROM activity_events WHERE session_id='other'`)
	check(other, true)
	// The production snapshot writer must invalidate already cached sessions.
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSnapshots(tx, []ActivitySnapshotRow{{SessionID: row.ID, Timestamp: row.StartTime, App: "Private Notes", ParsedJSON: `{}`, StorageTier: "hot"}}); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	check(row, false)
	exec(`UPDATE activity_snapshots SET app='Code' WHERE session_id='source'`)
	check(row, true)
	exec(`UPDATE activity_snapshots SET parsed_json='{"title":"Bank - Incognito"}' WHERE session_id='source'`)
	check(row, false)
	exec(`UPDATE activity_snapshots SET session_id='other' WHERE session_id='source'`)
	check(row, true)
	check(other, false)
	exec(`DELETE FROM activity_snapshots WHERE session_id='other'`)
	check(other, true)
	exec(`INSERT INTO activity_events(session_id,timestamp,kind,data,sort_order) VALUES('source',100000,'focus','malformed',0)`)
	check(row, false)
	exec(`DELETE FROM activity_events WHERE session_id='source'`)
	check(row, true)
	if err := store.DeleteSession(row.ID); err != nil {
		t.Fatal(err)
	}
	if allowed, _ := app.activitySessionAllowsModelReadResult(store, row); allowed {
		t.Fatal("deleted source survived warm cache")
	}
}

func TestActivityPrivacyCacheRechecksMetadataAndLivePolicy(t *testing.T) {
	isolateActivityPrivacyTest(t)
	app := newPrivacyReadTestApp(t)
	store := app.activity.store
	stale := ActivitySessionRow{ID: "source", StartTime: 100_000, EndTime: 160_000, AppNames: `["Code"]`}
	if err := store.SaveSession(stale, []activitymon.ActivityEvent{{AppName: "Code"}}, nil); err != nil {
		t.Fatal(err)
	}
	check := func(want bool) {
		t.Helper()
		got, err := app.activitySessionAllowsModelReadResult(store, stale)
		if err != nil || got != want {
			t.Fatalf("stale caller metadata: allowed=%t err=%v want=%t", got, err, want)
		}
	}
	check(true)
	for _, change := range []string{
		`UPDATE activity_sessions SET app_names='["Private Notes"]' WHERE id='source'`,
		`UPDATE activity_sessions SET triage_status='protected' WHERE id='source'`,
		`UPDATE activity_sessions SET meeting_app='Private Notes' WHERE id='source'`,
		`UPDATE activity_sessions SET app_names='malformed' WHERE id='source'`,
		`UPDATE activity_sessions SET start_time=200000,end_time=260000 WHERE id='source'`,
	} {
		app.saveObservationPauses([]observationPause{{Start: 210_000, End: 220_000}})
		if _, err := store.db.Exec(change); err != nil {
			t.Fatal(err)
		}
		check(false)
		if _, err := store.db.Exec(`UPDATE activity_sessions SET app_names='["Code"]',triage_status='',meeting_app='',start_time=100000,end_time=160000 WHERE id='source'`); err != nil {
			t.Fatal(err)
		}
		check(true)
	}
	scanned := store.readPrivacySources.eventRows.Load()
	pii.AddUserSensitiveApp("com.example.code", "Code")
	check(false)
	pii.SetUserSensitiveApps(map[string]string{"com.example.private": "Private Notes"})
	check(true)
	app.closedEyes.enabled.Store(true)
	check(false)
	app.closedEyes.enabled.Store(false)
	check(true)
	app.saveObservationPauses([]observationPause{{Start: 110_000, End: 120_000}})
	check(false)
	app.saveObservationPauses(nil)
	check(true)
	if got := store.readPrivacySources.eventRows.Load(); got != scanned {
		t.Fatalf("policy changes unnecessarily decoded unchanged event text: %d -> %d", scanned, got)
	}
	if err := app.store.SetUIState(context.Background(), observationPausesKey, "malformed"); err != nil {
		t.Fatal(err)
	}
	if allowed, err := app.activitySessionAllowsModelReadResult(store, stale); allowed || err == nil {
		t.Fatalf("unreadable pause state allowed cached source: %t %v", allowed, err)
	}
}

func TestActivityPrivacyWarmCacheFailsClosedOnSourceStorageErrors(t *testing.T) {
	for _, fault := range []string{"missing trigger", "missing events", "missing snapshots", "missing revision table", "closed database"} {
		t.Run(fault, func(t *testing.T) {
			isolateActivityPrivacyTest(t)
			app := newPrivacyReadTestApp(t)
			store := app.activity.store
			insertTestSession(t, store, "source", "dismissed", 100_000)
			row, err := store.GetSessionByID("source")
			if err != nil {
				t.Fatal(err)
			}
			if !app.activitySessionAllowsModelRead(store, *row) {
				t.Fatal("safe source unavailable before fault")
			}
			var query string
			switch fault {
			case "missing trigger":
				query = `DROP TRIGGER evidence_revision_activity_events_UPDATE`
			case "missing events":
				query = `ALTER TABLE activity_events RENAME TO unavailable_events`
			case "missing snapshots":
				query = `ALTER TABLE activity_snapshots RENAME TO unavailable_snapshots`
			case "missing revision table":
				query = `DROP TABLE activity_evidence_source_revisions`
			case "closed database":
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if query != "" {
				if _, err := store.db.Exec(query); err != nil {
					t.Fatal(err)
				}
			}
			if allowed, err := app.activitySessionAllowsModelReadResult(store, *row); allowed || err == nil {
				t.Fatalf("cached source bypassed %s: allowed=%t err=%v", fault, allowed, err)
			}
		})
	}
}

func TestActivityPrivacyRetentionProofAndDeletionAreAtomic(t *testing.T) {
	isolateActivityPrivacyTest(t)
	app := newPrivacyReadTestApp(t)
	store := app.activity.store
	const date = "2026-04-20"
	seedActivitySummaryI18nSession(t, store, "source", date, 9, []string{"Code"})
	if _, err := store.SaveSummary(date, "daily", "public narrative", "test", false); err != nil {
		t.Fatal(err)
	}
	original, found := store.cachedActivitySummarySnapshot(date, "daily", "")
	if !found {
		t.Fatal("missing original cache")
	}
	// Warm metadata may not hide private provenance added before pruning.
	if !app.activitySummarySnapshotAllowsRead(date, "daily", original) {
		t.Fatal("safe source unavailable")
	}
	if _, err := store.db.Exec(`INSERT INTO activity_events(session_id,timestamp,kind,data,sort_order) VALUES('source',1,'focus','{"privateWindow":true}',0)`); err != nil {
		t.Fatal(err)
	}
	// A failed final deletion must roll back its retained proof too. Otherwise
	// there is an observable gap between publishing proof and deleting raw data.
	if _, err := store.db.Exec(`CREATE TRIGGER reject_test_retention BEFORE DELETE ON activity_sessions BEGIN SELECT RAISE(ABORT,'test retention interruption'); END`); err != nil {
		t.Fatal(err)
	}
	if err := store.deleteRetainedSession("source"); err == nil {
		t.Fatal("expected interrupted retention")
	}
	after, found := store.cachedActivitySummarySnapshot(date, "daily", "")
	if !found || after.Provenance != original.Provenance {
		t.Fatal("failed raw deletion published retained proof outside its transaction")
	}
	if _, err := store.GetSessionByID("source"); err != nil {
		t.Fatal("failed retention removed raw source")
	}
	if _, err := store.db.Exec(`DROP TRIGGER reject_test_retention`); err != nil {
		t.Fatal(err)
	}
	if err := store.deleteRetainedSession("source"); err != nil {
		t.Fatal(err)
	}
	retained, found := store.cachedActivitySummarySnapshot(date, "daily", "")
	if !found || !strings.Contains(retained.Provenance, `"retained":true`) || !strings.Contains(retained.Provenance, `"private":true`) {
		t.Fatalf("retention lost final private source facts: %+v", retained)
	}
	if app.activitySummarySnapshotAllowsRead(date, "daily", retained) {
		t.Fatal("retained private source survived the read gate")
	}
}
