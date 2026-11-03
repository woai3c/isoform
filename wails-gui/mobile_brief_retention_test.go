package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/pii"
)

func TestMobileBriefPurposeSurvivesRecordedRetentionPurge(t *testing.T) {
	for _, kind := range []string{taskContextSourceMemoryKind, "activity_session", "activity_run"} {
		t.Run(kind, func(t *testing.T) {
			app := newMobileBriefPurposeTestApp(t)
			now := time.Now().UTC().Truncate(time.Second)
			at := now.Add(-45 * 24 * time.Hour)
			sessionID := seedMobileBriefPurposeRun(t, app, "expired-run", 1, at, 1000, []string{"Editor"})
			detail := mobileBriefPurposeFixture("still-active", now)
			detail.OpenLoop.Kind = PurposeKindCommitment
			detail.Current.DueKind, detail.Current.DueValue = PurposeDueDate, now.Format("2006-01-02")
			detail.Current.Counterparty = "Ana Lee"
			sourceID := sessionID
			switch kind {
			case taskContextSourceMemoryKind:
				memory := "# Release plan\n\n- **Source**: activity\n- **Activity Session**: " + sessionID + "\n- **Apps**: Editor\n\nPRIVATE_MEMORY_BODY\n"
				if _, err := app.SaveMemoryEntry("retained-purpose", memory); err != nil {
					t.Fatal(err)
				}
				sourceID = "retained-purpose:live"
			case "activity_run":
				sourceID = "expired-run"
			}
			detail.Evidence = []PurposeEvidenceRecord{{SourceKind: kind, SourceID: sourceID,
				SourceObservedAt: at.UnixMilli(), PrivacyDecision: TaskContextPrivacyRedact, EvidenceClass: PurposeEvidenceEvaluatedProjection}}
			before := mobileBriefInput{AsOf: now, Location: time.UTC}
			collectMobileBriefPurposes(app, &before, []PurposeOpenLoopDetail{detail}, nil)
			if len(before.WorkingToward) != 1 || !strings.Contains(before.WorkingToward[0].Detail, "Compared the release options") {
				t.Fatalf("missing pre-purge observed progress: %+v", before.WorkingToward)
			}
			rotator := NewStorageRotator(app.activity.store, DefaultRotatorConfig())
			rotator.purgeExpiredSessions(rotator.snapshotConfig())
			if sessionExists(t, app.activity.store, sessionID) {
				t.Fatal("test did not purge the session")
			}
			if run, err := app.activity.store.GetTimelineRunByID("expired-run"); err != nil || run != nil {
				t.Fatalf("test did not purge the timeline run: %+v, %v", run, err)
			}
			after := mobileBriefInput{AsOf: now, Location: time.UTC}
			collectMobileBriefPurposes(app, &after, []PurposeOpenLoopDetail{detail}, nil)
			brief := buildMobileBriefV1(after)
			if len(brief.Sections.WorkingToward) != 1 || len(brief.Sections.Commitments) != 1 || len(brief.Sections.WaitingOnYou) != 0 || len(brief.Sections.OpenLoops) != 1 {
				t.Fatalf("active purpose vanished after retention: %+v", brief.Sections)
			}
			item := brief.Sections.WorkingToward[0]
			if item.LastTouch != "" || item.Detail != "Linked activity is no longer retained." {
				t.Fatalf("retention must not invent touch and must explain missing progress: %+v", item)
			}
			for _, evidence := range brief.Evidence {
				if evidence.SourceKind != "purpose" {
					t.Fatalf("deleted source offered as evidence: %+v", evidence)
				}
			}
			raw, _ := json.Marshal(brief)
			if strings.Contains(string(raw), "PRIVATE_MEMORY_BODY") || strings.Contains(string(raw), "Compared the release options") {
				t.Fatal("purged content leaked into the brief")
			}
		})
	}
}

func TestMobileBriefPurposeRetentionPreservesPrivacyAndUnknownSources(t *testing.T) {
	for _, exclusion := range []string{"protected", "sensitive-app", "private-browser", "persisted-pause", "unknown-session", "unknown-run"} {
		t.Run(exclusion, func(t *testing.T) {
			app := newMobileBriefPurposeTestApp(t)
			now := time.Now().UTC().Truncate(time.Second)
			at := now.Add(-45 * 24 * time.Hour)
			sessionID := seedMobileBriefPurposeRun(t, app, "private-run", 1, at, 1000, []string{"Editor"})
			store := app.activity.store
			switch exclusion {
			case "protected", "persisted-pause":
				if _, err := store.db.Exec(`UPDATE activity_sessions SET triage_status='protected' WHERE id=?`, sessionID); err != nil {
					t.Fatal(err)
				}
			case "private-browser":
				if _, err := store.db.Exec(`UPDATE activity_timeline_group_summaries SET title='Private research (Incognito)' WHERE group_id='private-run'`); err != nil {
					t.Fatal(err)
				}
			}
			if exclusion == "sensitive-app" {
				pii.SetUserSensitiveApps(map[string]string{"editor.app": "Editor"})
				t.Cleanup(func() { pii.SetUserSensitiveApps(map[string]string{}) })
			}
			rotator := NewStorageRotator(store, DefaultRotatorConfig())
			rotator.purgeExpiredSessions(rotator.snapshotConfig())
			// Deletion freezes the exclusion; later relaxing settings must not
			// make the lost source readable again.
			pii.SetUserSensitiveApps(map[string]string{})
			detail := mobileBriefPurposeFixture("PRIVATE_PURPOSE", now)
			detail.Current.Counterparty = "PRIVATE_PERSON"
			detail.OpenLoop.Kind = PurposeKindCommitment
			detail.Current.DueKind, detail.Current.DueValue = PurposeDueDate, now.Format("2006-01-02")
			sourceKind, sourceID := "activity_session", sessionID
			switch exclusion {
			case "unknown-session":
				sourceID = "never-recorded"
			case "unknown-run":
				sourceKind, sourceID = "activity_run", "never-recorded"
			}
			refs := []PurposeEvidenceRecord{{SourceKind: sourceKind, SourceID: sourceID}}
			if !strings.HasPrefix(exclusion, "unknown-") {
				refs = append(refs, PurposeEvidenceRecord{SourceKind: "activity_run", SourceID: "private-run"})
			}
			for _, ref := range refs {
				ref.SourceObservedAt, ref.PrivacyDecision, ref.EvidenceClass = at.UnixMilli(), TaskContextPrivacyRedact, PurposeEvidenceEvaluatedProjection
				detail.Evidence = []PurposeEvidenceRecord{ref}
				in := mobileBriefInput{AsOf: now, Location: time.UTC}
				collectMobileBriefPurposes(app, &in, []PurposeOpenLoopDetail{detail}, nil)
				brief := buildMobileBriefV1(in)
				if strings.HasPrefix(exclusion, "unknown-") {
					if len(brief.Sections.WorkingToward) != 1 || len(brief.Sections.OpenLoops) != 1 || len(brief.Sections.Commitments)+len(brief.Sections.WaitingOnYou) != 0 {
						t.Fatalf("missing evidence removed the purpose or inferred activity: %+v", brief.Sections)
					}
					item := brief.Sections.WorkingToward[0]
					if !item.EvidenceUnavailable || item.Detail != "Evidence no longer on this computer." || item.LastTouch != "" {
						t.Fatalf("missing evidence did not use the content-free fallback: %+v", item)
					}
					if len(brief.Evidence) != 0 || len(item.Refs) != 0 || item.PurposeID != detail.OpenLoop.ID {
						t.Fatalf("missing evidence exposed a source or lost purpose identity: item=%+v evidence=%+v", item, brief.Evidence)
					}
					continue
				}
				if len(brief.Sections.WorkingToward)+len(brief.Sections.Commitments)+len(brief.Sections.WaitingOnYou)+len(brief.Sections.OpenLoops) != 0 {
					t.Fatalf("retention bypassed %s privacy via %s: %+v", exclusion, ref.SourceKind, brief.Sections)
				}
			}
		})
	}
}

func TestActivityRetentionReceiptRollsBackWithFailedDeletion(t *testing.T) {
	store := newTestStore(t)
	insertSession(t, store, "keep", time.Now().Add(-45*24*time.Hour), "done")
	if _, err := store.db.Exec(`CREATE TRIGGER prevent_retention_delete BEFORE DELETE ON activity_sessions
		BEGIN SELECT RAISE(ABORT, 'test deletion failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := store.deleteSessionForRetention("keep"); err == nil {
		t.Fatal("expected deletion failure")
	}
	row, err := store.getActivityRetentionSource("activity_session", "keep")
	if err != nil || row != nil || !sessionExists(t, store, "keep") {
		t.Fatalf("failed deletion wrote a retention receipt: %+v, %v", row, err)
	}
}

func TestMobileBriefPurposeRetentionRetryPreservesPrivateRun(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	store := app.activity.store
	now := time.Now().UTC().Truncate(time.Second)
	at := now.Add(-45 * 24 * time.Hour)
	sessionID := seedMobileBriefPurposeRun(t, app, "private-retry", 1, at, 1000, []string{"Editor"})
	if _, err := store.db.Exec(`UPDATE activity_timeline_group_summaries
		SET title='Private research (Incognito)' WHERE group_id='private-retry'`); err != nil {
		t.Fatal(err)
	}
	detail := mobileBriefPurposeFixture("private-purpose", now)
	detail.OpenLoop.Kind = PurposeKindCommitment
	detail.Current.DueKind, detail.Current.DueValue = PurposeDueDate, now.Format("2006-01-02")
	detail.Current.Counterparty = "Private person"
	detail.Evidence = []PurposeEvidenceRecord{{SourceKind: "activity_session", SourceID: sessionID,
		SourceObservedAt: at.UnixMilli(), PrivacyDecision: TaskContextPrivacyRedact, EvidenceClass: PurposeEvidenceEvaluatedProjection}}
	assertExcluded := func() {
		t.Helper()
		in := mobileBriefInput{AsOf: now, Location: time.UTC}
		collectMobileBriefPurposes(app, &in, []PurposeOpenLoopDetail{detail}, nil)
		brief := buildMobileBriefV1(in)
		if len(brief.Sections.WorkingToward)+len(brief.Sections.Commitments)+len(brief.Sections.WaitingOnYou)+len(brief.Sections.OpenLoops) != 0 {
			t.Fatalf("private purpose survived retention retry: %+v", brief.Sections)
		}
	}
	assertExcluded()
	if _, err := store.db.Exec(`CREATE TRIGGER prevent_retention_delete BEFORE DELETE ON activity_sessions
		BEGIN SELECT RAISE(ABORT, 'test deletion failure'); END`); err != nil {
		t.Fatal(err)
	}
	rotator := NewStorageRotator(store, DefaultRotatorConfig())
	rotator.purgeExpiredSessions(rotator.snapshotConfig())
	if !sessionExists(t, store, sessionID) {
		t.Fatal("failed deletion removed the session")
	}
	if run, err := store.GetTimelineRunByID("private-retry"); err != nil || run != nil {
		t.Fatalf("failed session deletion retained an expired run: %+v, %v", run, err)
	}
	var summaries int
	if err := store.db.QueryRow(`SELECT count(*) FROM activity_timeline_group_summaries WHERE group_id='private-retry'`).Scan(&summaries); err != nil || summaries != 0 {
		t.Fatalf("failed session deletion retained private summary content: %d, %v", summaries, err)
	}
	for _, status := range []string{"pending", "triaging", "dismissed"} {
		if err := store.UpdateTriageStatus(sessionID, status); err != nil {
			t.Fatal(err)
		}
		if !store.isSessionProtected(sessionID) {
			t.Fatalf("late triager status %s removed retention privacy protection", status)
		}
		assertExcluded()
	}
	rotator.purgeExpiredSessions(rotator.snapshotConfig())
	assertExcluded()
	if _, err := store.db.Exec(`DROP TRIGGER prevent_retention_delete`); err != nil {
		t.Fatal(err)
	}
	rotator.purgeExpiredSessions(rotator.snapshotConfig())
	if sessionExists(t, store, sessionID) {
		t.Fatal("retry did not purge the session")
	}
	if run, err := store.GetTimelineRunByID("private-retry"); err != nil || run != nil {
		t.Fatalf("retry did not purge the timeline run: %+v, %v", run, err)
	}
	for kind, id := range map[string]string{"activity_session": sessionID, "activity_run": "private-retry"} {
		row, err := store.getActivityRetentionSource(kind, id)
		if err != nil || row == nil || !row.Excluded {
			t.Fatalf("retry lost %s privacy exclusion: %+v, %v", kind, row, err)
		}
	}
	assertExcluded()
}

func TestMobileBriefPurposeHonorsPersistedRunExclusion(t *testing.T) {
	for _, kind := range []string{"activity_session", "activity_run"} {
		t.Run(kind, func(t *testing.T) {
			app := newMobileBriefPurposeTestApp(t)
			store := app.activity.store
			now := time.Now().UTC()
			at := now.Add(-45 * 24 * time.Hour)
			sessionID := seedMobileBriefPurposeRun(t, app, "excluded-run", 1, at, 1000, []string{"Editor"})
			if _, err := store.db.Exec(`UPDATE activity_timeline_groups SET evidence_excluded=1 WHERE id='excluded-run'`); err != nil {
				t.Fatal(err)
			}
			sourceID := sessionID
			if kind == "activity_run" {
				sourceID = "excluded-run"
			}
			detail := mobileBriefPurposeFixture("private-purpose", now)
			detail.Evidence = []PurposeEvidenceRecord{{SourceKind: kind, SourceID: sourceID, SourceObservedAt: at.UnixMilli(), PrivacyDecision: TaskContextPrivacyRedact}}
			assertExcluded := func() {
				t.Helper()
				in := mobileBriefInput{AsOf: now, Location: time.UTC}
				collectMobileBriefPurposes(app, &in, []PurposeOpenLoopDetail{detail}, nil)
				if len(in.WorkingToward) != 0 || len(in.OpenLoops) != 0 || in.ExcludedSensitiveItems != 1 {
					t.Fatalf("persisted run exclusion bypassed via %s: %+v", kind, in)
				}
			}
			assertExcluded()
			rotator := NewStorageRotator(store, DefaultRotatorConfig())
			rotator.purgeExpiredSessions(rotator.snapshotConfig())
			assertExcluded()
		})
	}
}

func TestMobileBriefRetentionReceiptsFreezeOnlyTheDeletionPrivacyDecision(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	store := app.activity.store
	now := time.Now().UTC()
	at := now.Add(-45 * 24 * time.Hour)
	sessionID := seedMobileBriefPurposeRun(t, app, "policy-at-deletion", 1, at, 1000, []string{"Editor"})
	rotator := NewStorageRotator(store, DefaultRotatorConfig())
	rotator.purgeExpiredSessions(rotator.snapshotConfig())
	// No app names or times survive to re-evaluate later settings. The
	// purpose may stay visible, but deleted activity can never be cited.
	pii.SetUserSensitiveApps(map[string]string{"editor.app": "Editor"})
	t.Cleanup(func() { pii.SetUserSensitiveApps(map[string]string{}) })
	app.saveObservationPauses([]observationPause{{Start: at.Add(-time.Minute).UnixMilli(), End: at.UnixMilli()}})
	detail := mobileBriefPurposeFixture("retained-purpose", now)
	detail.Current.Counterparty = "Ana Lee"
	detail.Evidence = []PurposeEvidenceRecord{{SourceKind: "activity_session", SourceID: sessionID, SourceObservedAt: at.UnixMilli(), PrivacyDecision: TaskContextPrivacyRedact}}
	in := mobileBriefInput{AsOf: now, Location: time.UTC}
	collectMobileBriefPurposes(app, &in, []PurposeOpenLoopDetail{detail}, nil)
	brief := buildMobileBriefV1(in)
	if len(brief.Sections.WorkingToward) != 1 || brief.Sections.WorkingToward[0].LastTouch != "" || len(brief.Sections.WaitingOnYou) != 0 {
		t.Fatalf("receipt invented a touch or policy history: %+v", brief.Sections)
	}
	for _, source := range brief.Evidence {
		if source.SourceKind != "purpose" {
			t.Fatalf("receipt exposed deleted activity: %+v", source)
		}
	}
}
