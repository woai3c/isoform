package main

import (
	"encoding/json"
	"testing"
	"time"
)

func TestActivityRetentionUnavailableEvidenceCrossesBatches(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	store := app.activity.store
	seedRetentionBacklog(t, app, 65)
	// The missing member occurs on the last page. Availability must propagate
	// backwards through shared live sessions and an already-retained session.
	for runID, members := range map[string][]string{
		"backlog-032": {"s-backlog-000", "s-backlog-032"},
		"backlog-064": {"s-backlog-032", "s-backlog-064", "pre-receipt-member"},
	} {
		raw, _ := json.Marshal(members)
		if _, err := store.db.Exec(`UPDATE activity_timeline_groups SET session_ids=? WHERE id=?`, string(raw), runID); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.deleteSessionForRetention("s-backlog-000"); err != nil {
		t.Fatal(err)
	}
	if err := store.purgeTimelineRunsOlderThan(time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	reader := mobileBriefPurposeReader{store: store}
	for _, id := range []string{"backlog-000", "backlog-032", "backlog-064"} {
		receipt, err := store.getActivityRetentionSource("activity_run", id)
		if err != nil || receipt == nil || receipt.Excluded {
			t.Fatalf("unavailable evidence became exclusion for %s: %+v, %v", id, receipt, err)
		}
		if observed := reader.retainedSource("activity_run", id); !observed.MissingEvidence || observed.Sensitive || observed.Unavailable {
			t.Fatalf("unavailable evidence lost across pages for %s: %+v", id, observed)
		}
		if store.isSessionProtected("s-" + id) {
			t.Fatalf("shared session %s became protected", id)
		}
	}
	if observed := reader.retainedSource("activity_session", "s-backlog-000"); !observed.MissingEvidence || observed.Sensitive || observed.Unavailable {
		t.Fatalf("existing session receipt lost unavailable provenance: %+v", observed)
	}
}

func TestActivityRetentionUnavailableMarkersSurviveMigrationReplay(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	store := app.activity.store
	seedRetentionBacklog(t, app, 1)
	if _, err := store.db.Exec(`UPDATE activity_timeline_groups SET session_ids='["s-backlog-000","pre-receipt-member"]'`); err != nil {
		t.Fatal(err)
	}
	if err := store.purgeTimelineRunsOlderThan(time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DELETE FROM migration_log WHERE name IN
		('activity_retention_unavailable', 'activity_retention_sources_minimal')`); err != nil {
		t.Fatal(err)
	}
	if err := store.migrate(); err != nil {
		t.Fatal(err)
	}
	reader := mobileBriefPurposeReader{store: store}
	if observed := reader.retainedSource("activity_run", "backlog-000"); !observed.MissingEvidence || observed.Sensitive || observed.Unavailable {
		t.Fatalf("migration replay changed availability into privacy: %+v", observed)
	}
	var extra int
	if err := store.db.QueryRow(`SELECT count(*) FROM pragma_table_info('activity_retention_unavailable_v1')
		WHERE name NOT IN ('source_kind', 'source_id')`).Scan(&extra); err != nil || extra != 0 {
		t.Fatalf("availability marker kept content beyond opaque identity: extra=%d error=%v", extra, err)
	}
}

func TestActivityRetentionRunAvailabilityOutlivesMemberReceipts(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	store := app.activity.store
	seedRetentionBacklog(t, app, 1)
	if _, err := store.db.Exec(`INSERT INTO activity_retention_unavailable_v1 VALUES ('activity_run', 'backlog-000')`); err != nil {
		t.Fatal(err)
	}
	for kind, id := range map[string]string{"activity_run": "backlog-000", "activity_session": "s-backlog-000"} {
		reader := mobileBriefPurposeReader{app: app, store: store, asOf: time.Now(), pausesReadable: true, cache: map[string]mobileBriefPurposeObservation{}}
		if observed := reader.source(kind, id, 0, false); !observed.MissingEvidence || observed.Sensitive || observed.Unavailable {
			t.Fatalf("live %s ignored the run's unavailable history: %+v", kind, observed)
		}
	}
	// The run's own availability marker remains authoritative even if every
	// member has since acquired a safe receipt and no content remains to check.
	if err := store.deleteSessionForRetention("s-backlog-000"); err != nil {
		t.Fatal(err)
	}
	if err := store.purgeTimelineRunsOlderThan(time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	reader := mobileBriefPurposeReader{store: store}
	for kind, id := range map[string]string{"activity_run": "backlog-000", "activity_session": "s-backlog-000"} {
		if observed := reader.retainedSource(kind, id); !observed.MissingEvidence || observed.Sensitive || observed.Unavailable {
			t.Fatalf("%s lost the run's unavailable history: %+v", kind, observed)
		}
	}
}
