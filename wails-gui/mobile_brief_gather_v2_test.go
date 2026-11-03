package main

import (
	"testing"
	"time"

	"wails-gui/internal/handoff"
)

func TestMorningBriefGatherDiffAndThresholds(t *testing.T) {
	now := time.Date(2026, 10, 30, 8, 0, 0, 0, time.UTC)
	oldAt := now.AddDate(0, 0, -1)
	lastTouch := now.AddDate(0, 0, -7)
	items := []mobileBriefCandidate{
		{SourceKind: "purpose", SourceID: "unchanged", Title: "Unchanged", Status: "active", Kind: "goal", LastTouch: now.AddDate(0, 0, -2)},
		{SourceKind: "purpose", SourceID: "stuck", Title: "Stuck", Status: "active", Kind: "goal", LastTouch: lastTouch},
		{SourceKind: "purpose", SourceID: "changed", Title: "Changed", Status: "active", Kind: "goal", LastTouch: now.Add(-time.Hour)},
		{SourceKind: "purpose", SourceID: "unspoken", Title: "Unspoken", Status: "active", Kind: "goal", LastTouch: now.AddDate(0, 0, -2)},
		{SourceKind: "purpose", SourceID: "private", Title: "Private", Status: "active", Kind: "goal", Protected: true},
	}
	previous := MobileBriefV1{AsOf: formatMobileBriefTime(oldAt), Timezone: "UTC"}
	for i, item := range items[:3] {
		ref := string(rune('a' + i))
		previous.Evidence = append(previous.Evidence, MobileBriefRef{Ref: ref, SourceKind: item.SourceKind, SourceID: item.SourceID})
		last := item.LastTouch
		if item.SourceID == "changed" {
			last = oldAt.Add(-time.Hour)
		}
		previous.Sections.WorkingToward = append(previous.Sections.WorkingToward, MobileBriefItem{
			Title: item.Title, Status: item.Status, Kind: item.Kind, LastTouch: formatMobileBriefTime(last), Refs: []string{ref},
		})
		previous.Narrative = append(previous.Narrative, MobileBriefPara{Text: "Previously mentioned.", Refs: []string{ref}})
	}
	previous.Evidence = append(previous.Evidence, MobileBriefRef{Ref: "d", SourceKind: "purpose", SourceID: "unspoken"})
	previous.Sections.WorkingToward = append(previous.Sections.WorkingToward, MobileBriefItem{
		Title: "Unspoken", Status: "active", Kind: "goal", LastTouch: formatMobileBriefTime(items[3].LastTouch), Refs: []string{"d"},
	})
	got := morningBriefGatherCandidates(mobileBriefInput{AsOf: now, Location: time.UTC, PreviousBriefs: []MobileBriefV1{previous}, WorkingToward: items})
	byID := map[string]morningBriefCandidate{}
	for _, item := range got {
		byID[item.SourceID] = item
	}
	if len(byID) != 3 || byID["stuck"].Changed || !byID["changed"].Changed || len(byID["stuck"].PriorBriefs) != 1 ||
		len(byID["unspoken"].PriorBriefs) != 1 || byID["unspoken"].PriorBriefs[0] != "none" {
		t.Fatalf("gathered = %+v", got)
	}
}

func TestMorningBriefGatherCalendarIsNotPreparationEvidence(t *testing.T) {
	now := time.Date(2026, 10, 30, 8, 0, 0, 0, time.UTC)
	meeting := mobileBriefCandidate{SourceKind: mobileBriefCalendarSource, SourceID: "tomorrow", Title: "Planning", Detail: "invite not answered", Kind: "meeting", Status: "upcoming", At: now.Add(23 * time.Hour)}
	got := morningBriefGatherCandidates(mobileBriefInput{AsOf: now, Location: time.UTC, TomorrowMeetings: []mobileBriefCandidate{meeting}})
	if len(got) != 1 || !got[0].Within24Hours || got[0].MeetingPrep || got[0].NeedsUser || got[0].OnlyUser || got[0].Confidence != 1 {
		t.Fatalf("gathered = %+v", got)
	}
}

func TestMorningBriefGatherHandledRequiresPreviouslyRaisedLoop(t *testing.T) {
	now := time.Date(2026, 10, 30, 8, 0, 0, 0, time.UTC)
	previous := MobileBriefV1{AsOf: formatMobileBriefTime(now.AddDate(0, 0, -1)), Timezone: "UTC",
		Evidence:  []MobileBriefRef{{Ref: "e1", SourceKind: "history", SourceID: "work"}},
		Narrative: []MobileBriefPara{{Text: "The work is blocked.", Refs: []string{"e1"}}},
		Sections:  MobileBriefSections{OpenLoops: []MobileBriefItem{{Title: "Work", Status: "blocked", Refs: []string{"e1"}}}},
	}
	input := mobileBriefInput{AsOf: now, Location: time.UTC, PreviousBriefs: []MobileBriefV1{previous}, Moved: []mobileBriefCandidate{
		{SourceKind: "history", SourceID: "work", Title: "Work", Kind: "one_time_work", Status: "completed", At: now.Add(-time.Hour)},
		{SourceKind: "history", SourceID: "other", Title: "Other", Kind: "one_time_work", Status: "completed", At: now.Add(-time.Hour)},
	}}
	rows := morningBriefGatherCandidates(input)
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	for _, row := range rows {
		if row.Handled != (row.SourceID == "work") {
			t.Fatalf("handled classification = %+v", row)
		}
	}
}

func TestMorningBriefScoringMetadataDoesNotChangeLegacySourceDigest(t *testing.T) {
	base := mobileBriefCandidate{SourceKind: "purpose", SourceID: "goal", Title: "Launch"}
	withScoring := base
	withScoring.confidence, withScoring.confidenceKnown, withScoring.rankedSourceDigest = 0.9, true, "private-stamp"
	withScoring.rankedOriginDigest = "private-origin"
	withScoring.rankedAttendees = []string{"Dana"}
	withScoring.rankedInvitePending = true
	if mobileBriefModelDigest(base) != mobileBriefModelDigest(withScoring) {
		t.Fatal("scoring metadata changed the legacy source digest")
	}
}

func TestMorningBriefGatherUnboundProjectionDoesNotHideReadablePurpose(t *testing.T) {
	now := time.Date(2026, 10, 30, 8, 0, 0, 0, time.UTC)
	person := mobileBriefCandidate{SourceKind: "purpose", SourceID: "goal", Title: "Contact", rankedUnbound: true}
	goal := mobileBriefCandidate{SourceKind: "purpose", SourceID: "goal", Title: "Goal", rankedSourceDigest: "source"}
	rows := morningBriefGatherCandidates(mobileBriefInput{AsOf: now, Location: time.UTC, WaitingOnYou: []mobileBriefCandidate{person}, WorkingToward: []mobileBriefCandidate{goal}})
	if len(rows) != 1 || rows[0].Title != "Goal" || rows[0].Unbound || rows[0].SourceDigest != "source" {
		t.Fatalf("unbound projection hid the readable Purpose: %+v", rows)
	}
}

func TestMorningBriefGatherInvitationRequiresUserDecision(t *testing.T) {
	now := time.Date(2026, 10, 30, 8, 0, 0, 0, time.UTC)
	invite := mobileBriefCandidate{SourceKind: mobileBriefCalendarSource, SourceID: "invite", Title: "Planning", Kind: "meeting", Status: "upcoming", At: now.Add(2 * time.Hour), rankedInvitePending: true}
	rows := morningBriefGatherCandidates(mobileBriefInput{AsOf: now, Location: time.UTC, Meetings: []mobileBriefCandidate{invite}})
	if len(rows) != 1 || !rows[0].NeedsUser || !rows[0].OnlyUserKnown || !rows[0].OnlyUser || rows[0].Confidence != 1 || rows[0].DecisionKind != "invitation_response" || rows[0].MeetingPrep {
		t.Fatalf("invitation decision = %+v", rows)
	}
	if selected := morningBriefSelect(rows, nil); selected.Lead == nil || selected.Lead.ID != rows[0].ID {
		t.Fatalf("invitation decision did not qualify as a lead: %+v", selected)
	}
	invite.Status = "done"
	rows = morningBriefGatherCandidates(mobileBriefInput{AsOf: now, Location: time.UTC, Meetings: []mobileBriefCandidate{invite}})
	if len(rows) != 1 || rows[0].NeedsUser || rows[0].OnlyUserKnown {
		t.Fatalf("past invite required a decision: %+v", rows)
	}
}

func TestMorningBriefGatherUsesRecordedWorkAndSlipFacts(t *testing.T) {
	for _, source := range []handoff.Source{handoff.SourceMobile, handoff.SourceDesktop} {
		if confidence, known := morningBriefWorkConfidence(source); !known || confidence != 1 {
			t.Fatalf("user-created source %q lacks confidence", source)
		}
	}
	if _, known := morningBriefWorkConfidence(handoff.SourceTriage); known {
		t.Fatal("inferred work gained invented confidence")
	}
	now := time.Date(2026, 10, 30, 8, 0, 0, 0, time.UTC)
	commitment := mobileBriefCandidate{SourceKind: "purpose", SourceID: "due", Title: "Send the plan", Kind: PurposeKindCommitment, Status: "slipping", DueAt: now.Add(-time.Hour), confidence: 0.9, confidenceKnown: true}
	rows := morningBriefGatherCandidates(mobileBriefInput{AsOf: now, Location: time.UTC, Commitments: []mobileBriefCandidate{commitment}})
	if len(rows) != 1 || !rows[0].Slipping || rows[0].Confidence != 0.9 || rows[0].OnlyUserKnown {
		t.Fatalf("recorded slipping commitment = %+v", rows)
	}
	if selected := morningBriefSelect(rows, nil); selected.Slip == nil || selected.Slip.ID != rows[0].ID {
		t.Fatalf("slipping commitment lost its typed slot: %+v", selected)
	}
}
