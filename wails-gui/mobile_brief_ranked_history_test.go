package main

import (
	"fmt"
	"testing"
	"time"
)

func TestMorningBriefHistoryRequiresFrozenReadableInputs(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	brief := boundMemoryConversationFixture(t, app)
	brief.NarrativeStatus = "ok"
	bindConversationBriefFixture(t, app, &brief)
	if _, err := app.SaveMemoryEntry("unread", "# Unread topic\nA separate note."); err != nil {
		t.Fatal(err)
	}
	brief.Evidence = append(brief.Evidence, MobileBriefRef{Ref: "extra", SourceKind: "memory", SourceID: "unread", Label: "Unread topic"})
	brief.Sections.OpenLoops = append(brief.Sections.OpenLoops, MobileBriefItem{Title: "Unread topic", Refs: []string{"extra"}})
	cutoff := mobileBriefAsOfTime(brief).Add(24 * time.Hour)
	history := app.morningBriefReadableHistory(t.Context(), []MobileBriefV1{brief, brief}, cutoff)
	if len(history) != 1 || len(history[0].Narrative) == 0 {
		t.Fatalf("proven previous brief was rejected: %+v", history)
	}
	if len(history[0].Sections.OpenLoops) != 0 {
		t.Fatal("unfrozen legacy section rows became diff inputs")
	}
	newer := []MobileBriefV1{}
	for i := 1; i <= 3; i++ {
		invalid := brief
		invalid.BriefID = fmt.Sprintf("unproven-%d", i)
		invalid.AsOf = formatMobileBriefTime(cutoff.Add(-time.Duration(i) * time.Hour))
		invalid.modelBinding = nil
		newer = append(newer, invalid)
	}
	if len(app.morningBriefReadableHistory(t.Context(), append(newer, brief), cutoff)) != 0 {
		t.Fatal("older prose replaced an unprovable recent brief")
	}
	if len(app.morningBriefReadableHistory(t.Context(), []MobileBriefV1{brief}, mobileBriefAsOfTime(brief))) != 0 {
		t.Fatal("a same-time brief was admitted as history")
	}
	if err := app.DeleteMemoryEntry("launch"); err != nil {
		t.Fatal(err)
	}
	if len(app.morningBriefReadableHistory(t.Context(), []MobileBriefV1{brief}, cutoff)) != 0 {
		t.Fatal("withdrawn prior prose entered history")
	}
}

func TestMorningBriefPriorProseProofRechecksOriginalSources(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	brief := boundMemoryConversationFixture(t, app)
	brief.NarrativeStatus = "ok"
	bindConversationBriefFixture(t, app, &brief)
	cutoff := mobileBriefAsOfTime(brief).Add(24 * time.Hour)
	store := newMobileBriefStore(t.TempDir())
	if err := store.save(brief, cutoff); err != nil {
		t.Fatal(err)
	}
	history := app.morningBriefReadableHistory(t.Context(), []MobileBriefV1{brief}, cutoff)
	proofs, ok := app.morningBriefPriorProofs(t.Context(), store, history, cutoff)
	if !ok || len(proofs) != 1 || !app.morningBriefPriorProofsReadable(t.Context(), store, cutoff, proofs) {
		t.Fatalf("readable prior prose did not bind: %+v", proofs)
	}
	changed := brief
	changed.Narrative = append([]MobileBriefPara(nil), brief.Narrative...)
	changed.Narrative[0].Text = "Changed prior wording."
	if err := store.save(changed, cutoff); err != nil {
		t.Fatal(err)
	}
	if app.morningBriefPriorProofsReadable(t.Context(), store, cutoff, proofs) {
		t.Fatal("changed prior wording retained its proof")
	}
	if err := store.save(brief, cutoff); err != nil {
		t.Fatal(err)
	}
	if err := app.DeleteMemoryEntry("launch"); err != nil {
		t.Fatal(err)
	}
	if app.morningBriefPriorProofsReadable(t.Context(), store, cutoff, proofs) {
		t.Fatal("withdrawn prior source retained its proof")
	}
}
