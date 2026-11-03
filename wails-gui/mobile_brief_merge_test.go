package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Purpose sections must retain the companion's stable feedback identities,
// including when a new brief changes their display text and ordering.
func TestMobileBriefPurposeKeysSurviveCompanionMerge(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	item := mobileBriefCandidate{SourceKind: "purpose", SourceID: "release", Title: "Ship the beta", At: now}
	input := mobileBriefInput{AsOf: now, Location: time.UTC,
		WorkingToward: []mobileBriefCandidate{item}, Commitments: []mobileBriefCandidate{item},
		WaitingOnYou: []mobileBriefCandidate{item}, OpenLoops: []mobileBriefCandidate{item}}
	first := buildMobileBriefV1(input)
	item.Title, item.At, item.AttentionMs = "Ship the companion", now.Add(time.Hour), 9000
	other := mobileBriefCandidate{SourceKind: "purpose", SourceID: "another", Title: "Another priority", At: now.Add(2 * time.Hour)}
	input.AsOf = now.Add(3 * time.Hour)
	input.WorkingToward = []mobileBriefCandidate{other, item}
	input.Commitments = []mobileBriefCandidate{other, item}
	input.WaitingOnYou = []mobileBriefCandidate{other, item}
	input.OpenLoops = []mobileBriefCandidate{other, item}
	later := buildMobileBriefV1(input)
	seen := map[string]bool{}
	for _, section := range []string{mobileBriefSectionWorkingToward, mobileBriefSectionCommitments, mobileBriefSectionWaitingOnYou, mobileBriefSectionOpenLoops} {
		want := mobileBriefItemKey(section, "purpose", "release")
		if seen[want] {
			t.Fatalf("sections share a feedback key: %q", want)
		}
		seen[want] = true
		for _, brief := range []MobileBriefV1{first, later} {
			target, found := findMobileBriefFeedbackTarget(brief, want)
			if !found || target.Section != section || target.SourceKind != "purpose" || target.SourceID != "release" {
				t.Fatalf("%s lost its stable purpose target: %+v, found=%v", section, target, found)
			}
		}
	}
}

// A pre-receipt purge preserves goal identity without reusing unverifiable content.
func TestMobileBriefHistoricalPurgeShowsContentFreeFallback(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	now := time.Now().UTC().Truncate(time.Second)
	at := now.Add(-45 * 24 * time.Hour)
	session := seedMobileBriefPurposeRun(t, app, "pre-upgrade", 1, at, 1000, []string{"Editor"})
	detail := mobileBriefPurposeFixture("historical", now)
	detail.OpenLoop.Kind = PurposeKindCommitment
	detail.Current.Counterparty = "Ana Lee"
	detail.Current.DueKind, detail.Current.DueValue = PurposeDueDate, now.Format("2006-01-02")
	linkMobileBriefPurposeSession(&detail, session, at)
	before := mobileBriefInput{AsOf: now, Location: time.UTC}
	collectMobileBriefPurposes(app, &before, []PurposeOpenLoopDetail{detail}, nil)
	if len(before.WorkingToward) != 1 {
		t.Fatal("fixture must have readable activity before the historical purge")
	}
	store := app.activity.store
	// Reproduce the pre-receipt rotator's actual deletion, not an invented ID.
	if err := store.DeleteSession(session); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`DELETE FROM activity_timeline_group_summaries WHERE group_id='pre-upgrade'`,
		`DELETE FROM activity_timeline_groups WHERE id='pre-upgrade'`,
	} {
		if _, err := store.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if receipt, err := store.getActivityRetentionSource("activity_session", session); err != nil || receipt != nil {
		t.Fatalf("historical purge unexpectedly has a privacy receipt: %+v, %v", receipt, err)
	}
	after := mobileBriefInput{AsOf: now, Location: time.UTC}
	collectMobileBriefPurposes(app, &after, []PurposeOpenLoopDetail{detail}, nil)
	if len(after.WorkingToward) != 1 || len(after.OpenLoops) != 1 || len(after.Commitments)+len(after.WaitingOnYou) != 0 {
		t.Fatalf("missing evidence must preserve only goal identity and legacy row: %+v", after)
	}
	brief := buildMobileBriefV1(after)
	for _, item := range []MobileBriefItem{brief.Sections.WorkingToward[0], brief.Sections.OpenLoops[0]} {
		if item.Title != detail.Current.Title || item.Status != detail.Current.LifecycleState || item.Detail != "Evidence no longer on this computer." {
			t.Fatalf("unexpected fallback: %+v", item)
		}
		if item.At != "" || item.LastTouch != "" || item.NextStep != "" || item.DueAt != "" || item.MomentID != "" || item.ChatMessageID != "" || len(item.Refs) != 0 || item.PurposeID != detail.OpenLoop.ID {
			t.Fatalf("fallback leaked activity or derived obligations: %+v", item)
		}
		data, _ := json.Marshal(item)
		var wire map[string]any
		_ = json.Unmarshal(data, &wire)
		if wire["evidenceUnavailable"] != true {
			t.Fatal("clients need an explicit content-free fallback marker")
		}
	}
	if len(brief.Evidence) != 0 {
		t.Fatalf("fallback must not offer any sources even on an old phone: %+v", brief.Evidence)
	}
	prior := buildMobileBriefV1(before)
	for _, section := range []string{mobileBriefSectionWorkingToward, mobileBriefSectionOpenLoops} {
		key := mobileBriefItemKey(section, "purpose", detail.OpenLoop.ID)
		for _, document := range []MobileBriefV1{prior, brief} {
			target, ok := findMobileBriefFeedbackTarget(document, key)
			if !ok || target.SourceKind != "purpose" || target.SourceID != detail.OpenLoop.ID {
				t.Fatalf("missing evidence changed goal feedback identity: %+v, %v", target, ok)
			}
		}
	}
	if prompt := mobileBriefTestPrompt(brief, "English"); strings.Contains(prompt, detail.Current.Title) || strings.Contains(prompt, "Evidence no longer") {
		t.Fatal("the model must not invent narrative progress from unavailable evidence")
	}
	if err := app.store.SetUIState(t.Context(), uiLanguageStateKey, "zh-CN"); err != nil {
		t.Fatal(err)
	}
	localized := mobileBriefInput{AsOf: now, Location: time.UTC}
	collectMobileBriefPurposes(app, &localized, []PurposeOpenLoopDetail{detail}, nil)
	if localized.WorkingToward[0].Detail != "相关依据已不在这台电脑上。" || localized.OpenLoops[0].Detail != localized.WorkingToward[0].Detail {
		t.Fatal("fallback must be localized for both current and legacy clients")
	}
}

func TestMobileBriefMissingEvidenceFallbackPreservesPrivacy(t *testing.T) {
	for _, appName := range []string{"Editor", "1Password"} {
		t.Run(appName, func(t *testing.T) {
			app := newMobileBriefPurposeTestApp(t)
			now := time.Now().UTC()
			detail := mobileBriefPurposeFixture("mixed-history", now)
			detail.Current.LifecycleState = PurposeStateWaiting
			linkMobileBriefPurposeSession(&detail, "missing-historical-session", now.Add(-45*24*time.Hour))
			live := seedMobileBriefPurposeRun(t, app, "live-sibling", 1, now.Add(-time.Hour), 1000, []string{appName})
			linkMobileBriefPurposeSession(&detail, live, now.Add(-time.Hour))
			in := mobileBriefInput{AsOf: now, Location: time.UTC}
			collectMobileBriefPurposes(app, &in, []PurposeOpenLoopDetail{detail}, nil)
			if appName == "1Password" {
				if len(in.WorkingToward)+len(in.OpenLoops) != 0 || in.ExcludedSensitiveItems != 1 {
					t.Fatal("missing history must not bypass a known sensitive source")
				}
				return
			}
			if len(in.WorkingToward) != 1 || in.WorkingToward[0].Status != PurposeStateWaiting || len(in.WorkingToward[0].Evidence) != 0 || !in.WorkingToward[0].LastTouch.IsZero() || in.WorkingToward[0].Detail != "Evidence no longer on this computer." {
				t.Fatalf("partial evidence must not leak into identity-only fallback: %+v", in)
			}
		})
	}
}

func TestMobileBriefMissingRunMemberUsesContentFreeFallback(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	now := time.Now().UTC()
	live := seedMobileBriefPurposeRun(t, app, "mixed-run", 1, now.Add(-time.Hour), 1000, []string{"Editor"})
	members, _ := json.Marshal([]string{live, "purged-before-receipts"})
	if _, err := app.activity.store.db.Exec(`UPDATE activity_timeline_groups SET session_ids=? WHERE id='mixed-run'`, string(members)); err != nil {
		t.Fatal(err)
	}
	detail := mobileBriefPurposeFixture("missing-run-member", now)
	detail.Current.Counterparty = "Ana Lee"
	linkMobileBriefPurposeSession(&detail, live, now.Add(-time.Hour))
	in := mobileBriefInput{AsOf: now, Location: time.UTC}
	collectMobileBriefPurposes(app, &in, []PurposeOpenLoopDetail{detail}, nil)
	brief := buildMobileBriefV1(in)
	if len(brief.Sections.WorkingToward) != 1 || !brief.Sections.WorkingToward[0].EvidenceUnavailable || brief.Sections.WorkingToward[0].LastTouch != "" || len(brief.Evidence) != 0 || len(brief.Sections.WaitingOnYou) != 0 {
		t.Fatalf("a live sibling exposed a summary with a missing member: %+v", brief)
	}
	if strings.Contains(brief.Sections.WorkingToward[0].Detail, "Compared") {
		t.Fatal("mixed run summary leaked into fallback")
	}
}

func TestMobileBriefMissingEvidenceReadErrorDoesNotBecomeFallback(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	now := time.Now().UTC()
	detail := mobileBriefPurposeFixture("read-failure", now)
	linkMobileBriefPurposeSession(&detail, "missing-session", now.Add(-45*24*time.Hour))
	if _, err := app.activity.store.db.Exec(`DROP TABLE activity_retention_sources_v1`); err != nil {
		t.Fatal(err)
	}
	in := mobileBriefInput{AsOf: now, Location: time.UTC}
	collectMobileBriefPurposes(app, &in, []PurposeOpenLoopDetail{detail}, nil)
	if len(in.WorkingToward)+len(in.OpenLoops) != 0 {
		t.Fatal("a database read error is not evidence of deleted history")
	}
}
