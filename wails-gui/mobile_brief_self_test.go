package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func mobileBriefSelfPurposeFixture(t *testing.T, app *App, now time.Time, reasoningStatus string, sources ...SelfModelEvidence) PurposeOpenLoopDetail {
	t.Helper()
	decision := selfDreamDecisionEvidence(
		SelfDreamDecisionRecord{ID: "confirmed-decision", ActorKind: "user", Decision: SelfDreamDecisionAccept, CreatedAt: now.Add(-time.Hour).UnixMilli()},
		SelfDreamOperationRecord{Statement: "SELF_PRIVATE_BODY must stay out of the brief"},
	)
	entry, added, err := app.activity.store.AddSelfModelEntry(SelfModelEntry{
		Kind: selfModelKindPrinciple, Status: selfModelStatusActive,
		Statement: "SELF_PRIVATE_BODY must stay out of the brief", ReasoningStatus: reasoningStatus,
		Evidence: append([]SelfModelEvidence{decision}, sources...), CreatedAt: now.Add(-time.Hour).UnixMilli(),
	})
	if err != nil || !added {
		t.Fatalf("persist Self entry: added=%v, err=%v", added, err)
	}
	detail := mobileBriefPurposeFixture("confirmed-self-goal", now)
	detail.Evidence = []PurposeEvidenceRecord{{SourceKind: taskContextSourceSelfKind, SourceID: entry.ID,
		SourceObservedAt: entry.CreatedAt, EvidenceClass: PurposeEvidenceEvaluatedProjection, PrivacyDecision: TaskContextPrivacyRedact}}
	return detail
}

// Exercise the shared collector/builder and both presentation contracts. The
// service's separate refresh projection is not part of this fixture.
func mobileBriefSelfPurposeDocuments(t *testing.T, app *App, now time.Time, detail PurposeOpenLoopDetail) map[string]MobileBriefV1 {
	t.Helper()
	in := mobileBriefInput{AsOf: now, Location: time.UTC}
	collectMobileBriefPurposes(app, &in, []PurposeOpenLoopDetail{detail}, nil)
	brief := buildMobileBriefV1(in)
	today := todayBriefView(now, time.UTC, brief, true, MobileBriefHealth{}, false, nil)
	if today.Brief == nil {
		t.Fatal("today's built brief was not attached to Today")
	}
	wire, err := json.Marshal(brief)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), "SELF_PRIVATE_BODY") {
		t.Fatal("the Self source body leaked into the phone brief")
	}
	var phone MobileBriefV1
	if err := json.Unmarshal(wire, &phone); err != nil {
		t.Fatal(err)
	}
	return map[string]MobileBriefV1{"today": *today.Brief, "phone": phone}
}

func TestMobileBriefConfirmedSelfPurposeReachesTodayAndPhone(t *testing.T) {
	for _, reasoningStatus := range []string{
		"user_confirmed", "explicit_user_correction", "self_dream_user_correction",
		"explicit_user_control", "behavior_and_explicit_approval", "explicit_user", " USER_CONFIRMED ",
	} {
		t.Run(reasoningStatus, func(t *testing.T) {
			app := newMobileBriefPurposeTestApp(t)
			now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
			at := now.Add(-2 * time.Hour)
			sessionID := seedMobileBriefPurposeRun(t, app, "confirmed-self-work", 1, at, 1000, []string{"Editor"})
			detail := mobileBriefSelfPurposeFixture(t, app, now, reasoningStatus,
				SelfModelEvidence{SourceKind: "activity_session", SourceID: sessionID, OccurredAt: at.UnixMilli()})
			for client, document := range mobileBriefSelfPurposeDocuments(t, app, now, detail) {
				if len(document.Sections.WorkingToward) != 1 || len(document.Sections.OpenLoops) != 1 {
					t.Fatalf("%s lost the confirmed Self purpose: %+v", client, document.Sections)
				}
				item := document.Sections.WorkingToward[0]
				if item.Title != detail.Current.Title || item.Status != PurposeStateActive || item.EvidenceUnavailable ||
					item.LastTouch != formatMobileBriefTime(at) || !strings.Contains(item.Detail, "Compared the release options") {
					t.Fatalf("%s lost the readable progress: %+v", client, item)
				}
				if document.Sections.OpenLoops[0].Title != detail.Current.Title || document.Limits.ExcludedSensitiveItems != 0 {
					t.Fatalf("%s lost legacy compatibility or misclassified privacy: %+v", client, document)
				}
			}
		})
	}
}

func TestMobileBriefConfirmedSelfPurposePreservesSourcePrivacy(t *testing.T) {
	for _, sourceState := range []string{"missing_session", "unreadable_memory", "unreadable_self", "protected_session"} {
		t.Run(sourceState, func(t *testing.T) {
			app := newMobileBriefPurposeTestApp(t)
			now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
			source := SelfModelEvidence{SourceID: "unreadable-source", OccurredAt: now.Add(-time.Hour).UnixMilli()}
			switch sourceState {
			case "missing_session", "protected_session":
				source.SourceKind = "activity_session"
				source.SourceID = seedMobileBriefPurposeRun(t, app, "self-source", 1, now.Add(-time.Hour), 1000, []string{"Editor"})
				if sourceState == "missing_session" {
					if err := app.activity.store.DeleteSession(source.SourceID); err != nil {
						t.Fatal(err)
					}
				} else if _, err := app.activity.store.db.Exec(`UPDATE activity_sessions SET triage_status='protected' WHERE id=?`, source.SourceID); err != nil {
					t.Fatal(err)
				}
			case "unreadable_memory":
				source.SourceKind = taskContextSourceMemoryKind
			case "unreadable_self":
				source.SourceKind = taskContextSourceSelfKind
			}
			detail := mobileBriefSelfPurposeFixture(t, app, now, "user_confirmed", source)
			for client, document := range mobileBriefSelfPurposeDocuments(t, app, now, detail) {
				if sourceState != "missing_session" {
					if len(document.Sections.WorkingToward)+len(document.Sections.OpenLoops) != 0 {
						t.Fatalf("%s allowed explicit Self authority to bypass source privacy: %+v", client, document.Sections)
					}
					if sourceState == "protected_session" && document.Limits.ExcludedSensitiveItems != 1 {
						t.Fatalf("%s lost the real privacy exclusion: %+v", client, document.Limits)
					}
					continue
				}
				if len(document.Sections.WorkingToward) != 1 || len(document.Sections.OpenLoops) != 1 || len(document.Evidence) != 0 {
					t.Fatalf("%s lost the content-free historical fallback: %+v", client, document)
				}
				for _, item := range []MobileBriefItem{document.Sections.WorkingToward[0], document.Sections.OpenLoops[0]} {
					if item.Title != detail.Current.Title || item.Status != PurposeStateActive || !item.EvidenceUnavailable ||
						item.Detail != "Evidence no longer on this computer." || len(item.Refs) != 0 || item.LastTouch != "" || item.At != "" {
						t.Fatalf("%s exposed unavailable Self source content: %+v", client, item)
					}
				}
			}
		})
	}
}

func TestMobileBriefConfirmedSelfDecisionAloneReachesTodayAndPhone(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	detail := mobileBriefSelfPurposeFixture(t, app, now, "user_confirmed")
	for client, document := range mobileBriefSelfPurposeDocuments(t, app, now, detail) {
		if len(document.Sections.WorkingToward) != 1 || len(document.Sections.OpenLoops) != 1 {
			t.Fatalf("%s lost the purpose backed by a confirmed Self decision: %+v", client, document.Sections)
		}
		item := document.Sections.WorkingToward[0]
		if item.Title != detail.Current.Title || item.Status != PurposeStateActive || item.EvidenceUnavailable ||
			item.Detail != "No linked activity recorded yet." || item.LastTouch != "" {
			t.Fatalf("%s invented activity from a Self decision: %+v", client, item)
		}
		for _, ref := range document.Evidence {
			if ref.SourceKind != "purpose" || ref.SourceID != detail.OpenLoop.ID {
				t.Fatalf("%s turned the Self decision into activity evidence: %+v", client, ref)
			}
		}
	}
}

func TestMobileBriefEvaluatedSelfDoesNotBecomeExplicitUserEvidence(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	detail := mobileBriefSelfPurposeFixture(t, app, now, "source_backed")
	for client, document := range mobileBriefSelfPurposeDocuments(t, app, now, detail) {
		if len(document.Sections.WorkingToward)+len(document.Sections.OpenLoops) != 0 {
			t.Fatalf("%s treated evaluated projection as explicit user authority: %+v", client, document.Sections)
		}
	}
}
