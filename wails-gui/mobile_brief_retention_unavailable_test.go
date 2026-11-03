package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestMobileBriefMissingHistoricalMemberSurvivesRunRetention(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	store := app.activity.store
	now := time.Now().UTC().Truncate(time.Second)
	at := now.Add(-45 * 24 * time.Hour)
	live := seedMobileBriefPurposeRun(t, app, "historical-member-run", 1, at, 1000, []string{"Editor"})
	insertSession(t, store, "historical-member", at, "done")
	members, _ := json.Marshal([]string{live, "historical-member"})
	if _, err := store.db.Exec(`UPDATE activity_timeline_groups SET session_ids=? WHERE id='historical-member-run'`, string(members)); err != nil {
		t.Fatal(err)
	}
	// Actual pre-receipt deletion: the member existed, but no privacy receipt survives.
	if err := store.DeleteSession("historical-member"); err != nil {
		t.Fatal(err)
	}
	assertFallback := func(stage string) {
		t.Helper()
		for kind, id := range map[string]string{"activity_run": "historical-member-run", "activity_session": live} {
			detail := mobileBriefPurposeFixture("historical-goal", now)
			detail.OpenLoop.Kind = PurposeKindCommitment
			detail.Current.Counterparty = "Ana Lee"
			detail.Current.DueKind, detail.Current.DueValue = PurposeDueDate, now.Format("2006-01-02")
			detail.Evidence = []PurposeEvidenceRecord{{SourceKind: kind, SourceID: id, SourceObservedAt: at.UnixMilli(), PrivacyDecision: TaskContextPrivacyRedact}}
			in := mobileBriefInput{AsOf: now, Location: time.UTC}
			collectMobileBriefPurposes(app, &in, []PurposeOpenLoopDetail{detail}, nil)
			brief := buildMobileBriefV1(in)
			if len(brief.Sections.WorkingToward) != 1 || len(brief.Sections.OpenLoops) != 1 || len(brief.Sections.Commitments)+len(brief.Sections.WaitingOnYou) != 0 {
				t.Fatalf("%s/%s: historical goal vanished or gained obligations: %+v", stage, kind, brief.Sections)
			}
			for _, item := range []MobileBriefItem{brief.Sections.WorkingToward[0], brief.Sections.OpenLoops[0]} {
				if item.Title != detail.Current.Title || item.Status != detail.Current.LifecycleState || item.Detail != "Evidence no longer on this computer." || !item.EvidenceUnavailable {
					t.Fatalf("%s/%s: not the content-free fallback: %+v", stage, kind, item)
				}
				if len(item.Refs) != 0 || item.PurposeID != detail.OpenLoop.ID || item.At != "" || item.LastTouch != "" || item.NextStep != "" || item.DueAt != "" || item.ChatMessageID != "" {
					t.Fatalf("%s/%s: fallback exposed activity: %+v", stage, kind, item)
				}
			}
			if len(brief.Evidence) != 0 || in.ExcludedSensitiveItems != 0 || strings.Contains(mobileBriefTestPrompt(brief, "English"), detail.Current.Title) {
				t.Fatalf("%s/%s: missing evidence became a source, model input or privacy exclusion", stage, kind)
			}
		}
	}
	assertFallback("before retention")
	if _, err := store.db.Exec(`CREATE TRIGGER fail_historical_run_retention BEFORE DELETE ON activity_timeline_groups
		BEGIN SELECT RAISE(ABORT, 'test retention failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := store.PurgeTimelineRunsOlderThan(now.UnixMilli()); err == nil {
		t.Fatal("expected deletion failure")
	}
	if run, err := store.GetTimelineRunByID("historical-member-run"); err != nil || run == nil || run.EvidenceExcluded || store.isSessionProtected(live) {
		t.Fatalf("failed deletion turned missing history into exclusion: %+v, %v", run, err)
	}
	if receipt, err := store.getActivityRetentionSource("activity_run", "historical-member-run"); err != nil || receipt != nil {
		t.Fatalf("failed deletion claimed a live run was retained: %+v, %v", receipt, err)
	}
	assertFallback("failed deletion")
	if _, err := store.db.Exec(`DROP TRIGGER fail_historical_run_retention`); err != nil {
		t.Fatal(err)
	}
	if err := store.PurgeTimelineRunsOlderThan(now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if run, err := store.GetTimelineRunByID("historical-member-run"); err != nil || run != nil {
		t.Fatalf("run was not retained: %+v, %v", run, err)
	}
	if store.isSessionProtected(live) {
		t.Fatal("run retention marked its surviving member protected")
	}
	assertFallback("run retained")
	if err := store.deleteSessionForRetention(live); err != nil {
		t.Fatal(err)
	}
	for kind, id := range map[string]string{"activity_run": "historical-member-run", "activity_session": live} {
		if receipt, err := store.getActivityRetentionSource(kind, id); err != nil || receipt == nil || receipt.Excluded {
			t.Fatalf("missing history changed %s receipt privacy: %+v, %v", kind, receipt, err)
		}
	}
	assertFallback("surviving session retained")
}
