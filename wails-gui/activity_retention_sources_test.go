package main

import (
	"encoding/json"
	"testing"
	"time"
)

func TestActivityRetentionReceiptsDoNotKeepUsageMetadata(t *testing.T) {
	store := newTestStore(t)
	insertSession(t, store, "expired", time.Now().Add(-45*24*time.Hour), "done")
	if err := store.deleteSessionForRetention("expired"); err != nil {
		t.Fatal(err)
	}
	var columns int
	if err := store.db.QueryRow(`SELECT count(*) FROM pragma_table_info('activity_retention_sources_v1')
		WHERE name NOT IN ('source_kind', 'source_id', 'excluded')`).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if columns != 0 {
		t.Fatalf("retention receipt kept %d columns beyond opaque identity and exclusion", columns)
	}
}

func TestActivityRetentionSessionLookupIsBoundedByTime(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	store := app.activity.store
	now := time.Now().UTC()
	sessionID := seedMobileBriefPurposeRun(t, app, "expired-bounded", 1, now.Add(-45*24*time.Hour), 1000, []string{"Editor"})
	seedMobileBriefPurposeRun(t, app, "unrelated-new", 2, now, 1000, []string{"Editor"})
	// Outside the old session's interval, even malformed membership must not
	// be decoded by that session's privacy lookup.
	if _, err := store.db.Exec(`UPDATE activity_timeline_groups SET session_ids='not-json' WHERE id='unrelated-new'`); err != nil {
		t.Fatal(err)
	}
	if err := store.deleteSessionForRetention(sessionID); err != nil {
		t.Fatalf("session purge inspected an unrelated run: %v", err)
	}
	if sessionExists(t, store, sessionID) {
		t.Fatal("expired session survived bounded lookup")
	}
	row, err := store.getActivityRetentionSource("activity_session", sessionID)
	if err != nil || row == nil || row.Excluded {
		t.Fatalf("unrelated malformed membership changed the receipt: %+v, %v", row, err)
	}
}

func TestActivityRetentionUpgradeStripsLegacyUsageMetadata(t *testing.T) {
	store := newTestStore(t)
	if _, err := store.db.Exec(`DROP TABLE activity_retention_sources_v1;
		CREATE TABLE activity_retention_sources_v1 (
			source_kind TEXT NOT NULL, source_id TEXT NOT NULL, start_ms INTEGER NOT NULL,
			end_ms INTEGER NOT NULL, app_names TEXT NOT NULL, session_ids TEXT NOT NULL,
			excluded INTEGER NOT NULL, PRIMARY KEY(source_kind, source_id));
		INSERT INTO activity_retention_sources_v1 VALUES
			('activity_session', 'legacy', 1, 2, '["Editor"]', '[]', 0);
		DELETE FROM migration_log WHERE name='activity_retention_sources_minimal'`); err != nil {
		t.Fatal(err)
	}
	if err := store.migrate(); err != nil {
		t.Fatal(err)
	}
	row, err := store.getActivityRetentionSource("activity_session", "legacy")
	if err != nil || row == nil || !row.Excluded {
		t.Fatalf("legacy incomplete privacy decision became usable: %+v, %v", row, err)
	}
	var extra int
	if err := store.db.QueryRow(`SELECT count(*) FROM pragma_table_info('activity_retention_sources_v1')
		WHERE name NOT IN ('source_kind', 'source_id', 'excluded')`).Scan(&extra); err != nil || extra != 0 {
		t.Fatalf("legacy usage metadata survived upgrade: %d, %v", extra, err)
	}
	if _, err := store.db.Exec(`INSERT INTO activity_retention_sources_v1 VALUES ('activity_session', 'new-safe', 0);
		DELETE FROM migration_log WHERE name='activity_retention_sources_minimal'`); err != nil {
		t.Fatal(err)
	}
	if err := store.migrate(); err != nil {
		t.Fatal(err)
	}
	row, err = store.getActivityRetentionSource("activity_session", "new-safe")
	if err != nil || row == nil || row.Excluded {
		t.Fatalf("replaying minimal-schema migration changed a complete decision: %+v, %v", row, err)
	}
}

func TestActivityRetentionRunPreservesEveryMemberPrivacy(t *testing.T) {
	for _, state := range []string{"protected", "persisted-pause", "sensitive-app", "purged-excluded", "missing", "missing-and-protected", "malformed-membership", "empty-member"} {
		t.Run(state, func(t *testing.T) {
			app := newMobileBriefPurposeTestApp(t)
			store := app.activity.store
			at := time.Now().Add(-45 * 24 * time.Hour)
			safe := seedMobileBriefPurposeRun(t, app, "mixed", 1, at, 1000, []string{"Editor"})
			member := "member"
			insertSession(t, store, member, at.Add(-time.Minute), "done")
			switch state {
			case "protected", "persisted-pause", "purged-excluded", "missing-and-protected":
				if _, err := store.db.Exec(`UPDATE activity_sessions SET triage_status='protected' WHERE id=?`, member); err != nil {
					t.Fatal(err)
				}
			case "sensitive-app":
				if _, err := store.db.Exec(`UPDATE activity_sessions SET app_names='["1Password"]' WHERE id=?`, member); err != nil {
					t.Fatal(err)
				}
			}
			if state == "purged-excluded" {
				if err := store.deleteSessionForRetention(member); err != nil {
					t.Fatal(err)
				}
			}
			if state == "missing" {
				if _, err := store.db.Exec(`DELETE FROM activity_sessions WHERE id=?`, member); err != nil {
					t.Fatal(err)
				}
			}
			membership, _ := json.Marshal([]string{safe, member})
			if state == "missing-and-protected" {
				membership, _ = json.Marshal([]string{safe, member, "missing-history"})
			}
			if state == "empty-member" {
				membership, _ = json.Marshal([]string{safe, " "})
			}
			if state == "malformed-membership" {
				membership = []byte("not-json")
			}
			if _, err := store.db.Exec(`UPDATE activity_timeline_groups SET session_ids=? WHERE id='mixed'`, string(membership)); err != nil {
				t.Fatal(err)
			}
			if err := store.purgeTimelineRunsOlderThan(time.Now().UnixMilli()); err != nil {
				t.Fatal(err)
			}
			row, err := store.getActivityRetentionSource("activity_run", "mixed")
			if state == "missing" {
				if err != nil || row == nil || row.Excluded || store.isSessionProtected(safe) {
					t.Fatalf("missing history became a privacy exclusion: %+v, %v", row, err)
				}
				reader := mobileBriefPurposeReader{store: store}
				if observed := reader.retainedSource("activity_run", "mixed"); !observed.MissingEvidence || observed.Sensitive || observed.Unavailable {
					t.Fatalf("retained run lost unavailable evidence: %+v", observed)
				}
				return
			}
			if err != nil || row == nil || !row.Excluded {
				t.Fatalf("run lost %s member privacy: %+v, %v", state, row, err)
			}
			if !store.isSessionProtected(safe) {
				t.Fatal("live session lost the purged run's exclusion")
			}
		})
	}
}

func TestActivityRetentionPrivacyPropagationRollsBackRunPurge(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	store := app.activity.store
	at := time.Now().Add(-45 * 24 * time.Hour)
	seedMobileBriefPurposeRun(t, app, "private-propagation", 1, at, 1000, []string{"Editor"})
	if _, err := store.db.Exec(`UPDATE activity_timeline_groups SET evidence_excluded=1 WHERE id='private-propagation';
		CREATE TRIGGER prevent_privacy_transfer BEFORE UPDATE OF triage_status ON activity_sessions
		BEGIN SELECT RAISE(ABORT, 'test privacy transfer failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := store.purgeTimelineRunsOlderThan(time.Now().UnixMilli()); err == nil {
		t.Fatal("privacy transfer failure allowed content deletion")
	}
	row, err := store.getActivityRetentionSource("activity_run", "private-propagation")
	if err != nil || row != nil {
		t.Fatalf("failed purge wrote receipt: %+v, %v", row, err)
	}
	var summaries int
	if err := store.db.QueryRow(`SELECT count(*) FROM activity_timeline_group_summaries WHERE group_id='private-propagation'`).Scan(&summaries); err != nil || summaries != 1 {
		t.Fatalf("failed privacy transfer discarded the only exclusion source: %d, %v", summaries, err)
	}
}

func TestActivityRetentionRunPrivacyDoesNotDependOnOrder(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	store := app.activity.store
	at := time.Now().Add(-45 * 24 * time.Hour)
	shared := seedMobileBriefPurposeRun(t, app, "first-safe", 1, at, 1000, []string{"Editor"})
	other := seedMobileBriefPurposeRun(t, app, "last-private", 2, at.Add(time.Second), 1000, []string{"Editor"})
	ids, _ := json.Marshal([]string{shared, other})
	if _, err := store.db.Exec(`UPDATE activity_timeline_groups SET evidence_excluded=1, session_ids=? WHERE id='last-private'`, string(ids)); err != nil {
		t.Fatal(err)
	}
	if err := store.purgeTimelineRunsOlderThan(time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"first-safe", "last-private"} {
		row, err := store.getActivityRetentionSource("activity_run", id)
		if err != nil || row == nil || !row.Excluded {
			t.Fatalf("receipt for %s depended on scan order: %+v, %v", id, row, err)
		}
	}
}

// Two receipts describe a retained session: the retained source privacy receipt
// (summary provenance) and the opaque retention receipt (run and memory reads).
// One deletion path writes both in the deleting transaction, so a failed delete
// leaves neither and a successful one leaves both, with the read-privacy
// revision stepping past the deletion.
func TestActivityRetentionDeletionWritesBothReceiptsAtomically(t *testing.T) {
	store := newTestStore(t)
	insertSession(t, store, "both-receipts", time.Now().Add(-45*24*time.Hour), "done")
	count := func(table, column, id string) int {
		t.Helper()
		var n int
		if err := store.db.QueryRow(`SELECT count(*) FROM `+table+` WHERE `+column+`=?`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	if _, err := store.db.Exec(`CREATE TRIGGER block_session_delete BEFORE DELETE ON activity_sessions BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
		t.Fatal(err)
	}
	if err := store.deleteSessionForRetention("both-receipts"); err == nil {
		t.Fatal("the blocked deletion reported success")
	}
	if count("activity_retained_source_privacy", "session_id", "both-receipts") != 0 || count("activity_retention_sources_v1", "source_id", "both-receipts") != 0 {
		t.Fatal("a failed deletion left a receipt behind")
	}
	if !sessionExists(t, store, "both-receipts") {
		t.Fatal("a failed deletion removed the session")
	}
	if _, err := store.db.Exec(`DROP TRIGGER block_session_delete`); err != nil {
		t.Fatal(err)
	}

	before := store.readPrivacyRevision.Load()
	if err := store.deleteSessionForRetention("both-receipts"); err != nil {
		t.Fatal(err)
	}
	if sessionExists(t, store, "both-receipts") {
		t.Fatal("the session survived retention")
	}
	if count("activity_retained_source_privacy", "session_id", "both-receipts") != 1 || count("activity_retention_sources_v1", "source_id", "both-receipts") != 1 {
		t.Fatal("retention did not write both receipts")
	}
	if after := store.readPrivacyRevision.Load(); after < before+2 {
		t.Fatalf("read-privacy revision moved %d -> %d, want a full odd/even step", before, after)
	}
}
