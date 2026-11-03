package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestMorningBriefSelectionRefsSurvivesSectionCaps(t *testing.T) {
	brief := MobileBriefV1{AsOf: "2026-10-30T15:00:00Z", Evidence: []MobileBriefRef{{Ref: "e1", SourceKind: "history", SourceID: "other", Label: "Other"}}}
	lead := morningBriefCandidate{ID: mobileBriefItemKey("candidate", "purpose", "goal"), SourceKind: "purpose", SourceID: "goal", Title: "Launch", NextStep: "Review the posts", Dependencies: []morningBriefSourceDependency{{Kind: "purpose", ID: "step", Digest: "stamp"}}}
	need := morningBriefCandidate{ID: mobileBriefItemKey("candidate", "history", "other"), SourceKind: "history", SourceID: "other", Title: "Other"}
	got, selected, err := morningBriefSelectionRefs(brief, morningBriefSelection{Lead: &lead, Needs: []morningBriefCandidate{need}})
	if err != nil || selected.Lead == nil || len(selected.Lead.Refs) != 2 || len(got.Evidence) != 3 || selected.Needs[0].Refs[0] != "e1" {
		t.Fatalf("evidence=%+v selection=%+v err=%v", got.Evidence, selected, err)
	}
	byRef := mobileBriefEvidenceByRef(got)
	if byRef[selected.Lead.Refs[0]].SourceID != "goal" || byRef[selected.Lead.Refs[1]].SourceID != "step" || len(brief.Evidence) != 1 {
		t.Fatal("selected sources were not preserved independently of sections")
	}
	full := brief
	full.Evidence = nil
	for i := 0; i < mobileBriefEvidenceLimit; i++ {
		full.Evidence = append(full.Evidence, MobileBriefRef{Ref: fmt.Sprintf("e%d", i+1), SourceKind: "history", SourceID: fmt.Sprintf("other-%d", i)})
	}
	if _, _, err := morningBriefSelectionRefs(full, morningBriefSelection{Lead: &lead}); err == nil {
		t.Fatal("accepted a selected source with no evidence capacity")
	}
	bad := lead
	bad.SourceID = strings.Repeat("x", mobileBriefSourceIDLimit+1)
	bad.ID = mobileBriefItemKey("candidate", bad.SourceKind, bad.SourceID)
	if _, _, err := morningBriefSelectionRefs(brief, morningBriefSelection{Lead: &bad}); err == nil {
		t.Fatal("accepted a truncated source identity")
	}
	bad.SourceID = "unsafe\x00identity"
	bad.ID = mobileBriefItemKey("candidate", bad.SourceKind, bad.SourceID)
	if _, _, err := morningBriefSelectionRefs(brief, morningBriefSelection{Lead: &bad}); err == nil {
		t.Fatal("accepted an ambiguous source identity")
	}
}

func TestMorningBriefSelectionRefsReservedBeforeCrowdedSections(t *testing.T) {
	lead := morningBriefCandidate{ID: mobileBriefItemKey("candidate", "purpose", "goal"), SourceKind: "purpose", SourceID: "goal", Title: "Launch", NextStep: "Review the posts", Dependencies: []morningBriefSourceDependency{{Kind: "purpose", ID: "step", Digest: "stamp"}}}
	selected := morningBriefSelection{Lead: &lead}
	in := mobileBriefInput{AsOf: mobileBriefParseTime("2026-10-30T15:00:00Z")}
	for i := 0; i < 8; i++ {
		item := mobileBriefCandidate{SourceKind: "history", SourceID: fmt.Sprintf("work-%d", i), Title: fmt.Sprintf("Work %d", i)}
		for j := 0; j < 8; j++ {
			item.Evidence = append(item.Evidence, mobileBriefCandidate{SourceKind: "history", SourceID: fmt.Sprintf("source-%d-%d", i, j), Title: "Source"})
		}
		in.Moved = append(in.Moved, item)
	}
	brief, refs, err := morningBriefBuildSelected(in, selected)
	if err != nil || !brief.rankedEdition || len(refs[lead.ID]) != 2 || len(brief.Evidence) < 50 || len(brief.Evidence) > mobileBriefEvidenceLimit {
		t.Fatalf("crowded brief lost selected evidence: refs=%+v evidence=%d err=%v", refs, len(brief.Evidence), err)
	}
	byRef := mobileBriefEvidenceByRef(brief)
	if byRef[refs[lead.ID][0]].SourceID != "goal" || byRef[refs[lead.ID][1]].SourceID != "step" {
		t.Fatal("selected citations changed under section pressure")
	}
	if len(in.rankedEvidence) != 0 || len(brief.Narrative) != 0 || brief.NarrativeStatus != "unavailable" {
		t.Fatal("ranked rebuild changed the input or invented prose")
	}
}
