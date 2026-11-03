package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/handoff"
)

func TestMorningBriefRankedInputWithdrawalHidesStoredSynthesis(t *testing.T) {
	app, svc, brief := keptTextFixture(t, "safe-recall")
	store, err := handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app.handoffStore = store
	cutoff := mobileBriefAsOfTime(brief)
	entry := handoff.Entry{ID: "uncited-work", Title: "Private work item", Source: handoff.SourceMobile, Status: handoff.StatusBlocked, CreatedAt: cutoff.Add(-time.Hour).UnixMilli(), UpdatedAt: cutoff.Add(-time.Minute).UnixMilli()}
	if _, err := store.Save(entry); err != nil {
		t.Fatal(err)
	}
	in := mobileBriefInput{AsOf: cutoff}
	collectMobileBriefWork(app, &in)
	if len(in.OpenLoops) != 1 {
		t.Fatalf("work input = %+v", in.OpenLoops)
	}
	candidate := morningBriefCandidate{ID: mobileBriefItemKey("candidate", "history", entry.ID), SourceKind: "history", SourceID: entry.ID, SourceDigest: in.OpenLoops[0].rankedSourceDigest, Title: entry.Title}
	bound := app.morningBriefBindRankedInputs(t.Context(), cutoff, []morningBriefCandidate{candidate})
	if len(bound) != 1 {
		t.Fatal("work input was not admitted")
	}
	unsafe := candidate
	unsafe.Title = "Order number ABC123"
	if len(app.morningBriefBindRankedInputs(t.Context(), cutoff, []morningBriefCandidate{unsafe})) != 0 {
		t.Fatal("sensitive candidate text was admitted")
	}
	brief.modelBinding.RankedInputs = bound
	if err := svc.store.save(brief, cutoff); err != nil {
		t.Fatal(err)
	}
	stored := keptTextStored(t, svc, brief.BriefID)
	if len(stored.modelBinding.RankedInputs) != 1 {
		t.Fatal("ranked input binding did not survive storage")
	}
	wire, err := json.Marshal(stored)
	if err != nil || strings.Contains(string(wire), entry.Title) || strings.Contains(string(wire), entry.ID) {
		t.Fatalf("local ranked input escaped into the brief: %s, %v", wire, err)
	}
	if delivered, withheld := app.mobileBriefDelivery(stored); len(delivered.Narrative) != 1 || withheld.Reason != "" {
		t.Fatalf("unchanged synthesis withheld: %+v, %+v", delivered.Narrative, withheld)
	}
	entry.Title = "Changed private work"
	entry.UpdatedAt = cutoff.UnixMilli()
	if _, err := store.Save(entry); err != nil {
		t.Fatal(err)
	}
	if delivered, withheld := app.mobileBriefDelivery(stored); len(delivered.Narrative) != 0 || delivered.Audio != nil || withheld.Reason == "" {
		t.Fatalf("changed uncited input did not withdraw synthesis: %+v, %+v", delivered.Narrative, withheld)
	}
}

func TestMorningBriefCandidateSafetyKeepsGeneratedDates(t *testing.T) {
	item := morningBriefCandidate{ID: mobileBriefItemKey("candidate", "history", "item"), Title: "Prepare the plan", LastTouch: "2026-10-29T16:00:00Z", Due: "2026-10-30T09:00:00Z"}
	if !morningBriefCandidateSafe(item) {
		t.Fatal("generated dates were treated as secret codes")
	}
	item.Due = "not a date"
	if morningBriefCandidateSafe(item) {
		t.Fatal("invalid timestamp was admitted")
	}
}

func TestMorningBriefRankedDeliveryRejectsUnboundProse(t *testing.T) {
	app, _, brief := keptTextFixture(t, "safe-recall")
	brief.rankedEdition = true
	if delivered, withheld := app.mobileBriefDelivery(brief); len(delivered.Narrative) != 0 || withheld.Reason == "" {
		t.Fatal("a legacy source binding certified ranked prose")
	}
	brief.modelBinding = nil
	if delivered, withheld := app.mobileBriefDelivery(brief); len(delivered.Narrative) != 0 || delivered.Audio != nil || withheld.Reason == "" {
		t.Fatalf("unbound ranked prose was delivered: %+v, %+v", delivered.Narrative, withheld)
	}
}

func TestMorningBriefRankedBindingProvesSourcesOutsideSections(t *testing.T) {
	app, _, brief := keptTextFixture(t, "safe-recall")
	store, err := handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app.handoffStore = store
	cutoff := mobileBriefAsOfTime(brief)
	entry := handoff.Entry{ID: "outside-sections", Title: "Review the plan", Source: handoff.SourceMobile, Status: handoff.StatusNeedsReview, CreatedAt: cutoff.Add(-time.Hour).UnixMilli(), UpdatedAt: cutoff.Add(-time.Minute).UnixMilli()}
	if _, err := store.Save(entry); err != nil {
		t.Fatal(err)
	}
	gathered, err := app.readMobileBriefGatherRevision(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer gathered.close()
	in := mobileBriefInput{AsOf: cutoff}
	collectMobileBriefWork(app, &in)
	if len(in.OpenLoops) != 1 {
		t.Fatalf("work input = %+v", in.OpenLoops)
	}
	candidate := morningBriefCandidate{ID: mobileBriefItemKey("candidate", "history", entry.ID), SourceKind: "history", SourceID: entry.ID, SourceDigest: in.OpenLoops[0].rankedSourceDigest, Title: entry.Title}
	bound := app.morningBriefBindRankedInputs(t.Context(), cutoff, []morningBriefCandidate{candidate})
	if len(bound) != 1 {
		t.Fatal("ranked source was not admitted")
	}
	brief.rankedEdition, brief.NarrativeStatus = true, "ok"
	brief.Evidence = []MobileBriefRef{{Ref: "ranked-ref", SourceKind: "history", SourceID: entry.ID, Label: entry.Title}}
	brief.Sections = MobileBriefSections{}
	brief.Narrative = []MobileBriefPara{{Text: "Review the plan.", Refs: []string{"ranked-ref"}}}
	brief.Offers, brief.Cards, brief.Audio = nil, nil, nil
	unsafe := append([]morningBriefBoundInput(nil), bound...)
	unsafe[0].Candidate.Unbound = true
	if app.captureRankedBriefModelSources(t.Context(), brief, gathered, unsafe) != nil {
		t.Fatal("unbound source was admitted while freezing its manifest")
	}
	binding := app.captureRankedBriefModelSources(t.Context(), brief, gathered, bound)
	if binding == nil || len(binding.Sources) != 0 {
		t.Fatal("ranked-only source binding was not captured")
	}
	app.finishMobileBriefModelBinding(t.Context(), &brief, binding)
	if brief.modelBinding == nil || !app.validateMobileBriefModelSources(t.Context(), brief, brief.modelBinding) {
		t.Fatal("ranked evidence outside sections did not validate")
	}
	// A selected paused-brief card must be able to prove the same ranked
	// inputs, without reopening normal paused brief delivery.
	app.closedEyes.enabled.Store(true)
	pausedCtx := context.WithValue(t.Context(), pausedBriefReadKey{}, true)
	sources := app.collectTodayChatSources(pausedCtx, time.Now())
	if !app.validateMobileBriefModelSourcesFrom(pausedCtx, brief, brief.modelBinding, sources) {
		t.Fatal("unchanged ranked sources could not be proved for a paused brief")
	}
	if got, _ := app.mobileBriefDelivery(brief); len(got.Narrative) != 0 {
		t.Fatal("normal paused delivery exposed ranked prose")
	}
	app.closedEyes.enabled.Store(false)
	corrupt := *binding
	corrupt.RankedInputs = append([]morningBriefBoundInput(nil), binding.RankedInputs...)
	corrupt.RankedInputs[0].Candidate.Title = "Changed model input"
	tampered := brief
	tampered.modelBinding = &corrupt
	if app.validateMobileBriefModelSources(t.Context(), tampered, &corrupt) {
		t.Fatal("changed ranked manifest retained its proof")
	}
	if delivered, _ := app.mobileBriefDelivery(tampered); len(delivered.Narrative) != 0 {
		t.Fatal("changed ranked manifest left its synthesis visible")
	}
	history := app.morningBriefReadableHistory(t.Context(), []MobileBriefV1{brief}, cutoff.Add(24*time.Hour))
	if len(history) != 1 || len(history[0].Sections.OpenLoops) != 1 || history[0].Sections.OpenLoops[0].Title != entry.Title {
		t.Fatalf("frozen ranked ledger did not provide diff facts: %+v", history)
	}
	legacy := brief
	legacy.rankedEdition = false
	if app.validateMobileBriefModelSources(t.Context(), legacy, binding) {
		t.Fatal("ranked-only proof changed legacy binding semantics")
	}
	entry.Title = "Changed plan"
	entry.UpdatedAt = cutoff.UnixMilli()
	if _, err := store.Save(entry); err != nil {
		t.Fatal(err)
	}
	if app.validateMobileBriefModelSources(t.Context(), brief, binding) {
		t.Fatal("changed ranked source retained its model proof")
	}
}
