package main

import "testing"

func TestActivityRangeReadPrivacyChecksOverlappingSessions(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	store := app.activity.store
	const startMs, endMs = 100_000, 200_000
	if app.activityRangeAllowsModelRead(startMs, endMs) {
		t.Fatal("a cached derivative with no remaining source provenance was allowed")
	}
	insertTestSession(t, store, "overlap-start", "dismissed", 90_000)
	if !app.activityRangeAllowsModelRead(startMs, endMs) {
		t.Fatal("safe session crossing the range start should remain readable")
	}
	insertTestSession(t, store, "overlap-end", "protected", 180_000)
	if !app.activityRangeAllowsModelRead(startMs, endMs) {
		t.Fatal("capture-protected session that never entered the summary vetoed safe sources")
	}
	if _, err := store.db.Exec(`UPDATE activity_sessions SET triage_status = 'dismissed' WHERE id = 'overlap-end'`); err != nil {
		t.Fatal(err)
	}
	if !app.activityRangeAllowsModelRead(startMs, endMs) {
		t.Fatal("all safe overlapping sources should be readable")
	}
	app.closedEyes.enabled.Store(true)
	if app.activityRangeAllowsModelRead(startMs, endMs) {
		t.Fatal("closed eyes allowed a cached derivative")
	}
	app.closedEyes.enabled.Store(false)
	if app.activityRangeAllowsModelRead(endMs, startMs) || app.activityDayAllowsModelRead("invalid") {
		t.Fatal("invalid source bounds must fail closed")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if app.activityRangeAllowsModelRead(startMs, endMs) {
		t.Fatal("unreadable source provenance must fail closed")
	}
}
