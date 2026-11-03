package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"wails-gui/internal/handoff"
)

func TestMorningBriefQuietRequiresCompleteConfidentInputs(t *testing.T) {
	item := morningBriefCandidate{ID: "calendar", Confidence: 1}
	if !morningBriefQuietEligible(mobileBriefInput{}, []morningBriefCandidate{item}, []morningBriefCandidate{item}) {
		t.Fatal("a fully checked, quiet set was rejected")
	}
	for _, tc := range []struct {
		input    mobileBriefInput
		gathered []morningBriefCandidate
		admitted []morningBriefCandidate
	}{
		{},
		{mobileBriefInput{ColdStart: true}, []morningBriefCandidate{item}, []morningBriefCandidate{item}},
		{mobileBriefInput{OpenLoopsTruncated: true}, []morningBriefCandidate{item}, []morningBriefCandidate{item}},
		{mobileBriefInput{}, []morningBriefCandidate{item, item}, []morningBriefCandidate{item}},
		{mobileBriefInput{}, []morningBriefCandidate{item}, []morningBriefCandidate{{ID: "decision", Confidence: 1, NeedsUser: true}}},
		{mobileBriefInput{}, []morningBriefCandidate{item}, []morningBriefCandidate{{ID: "slip", Confidence: 1, Slipping: true}}},
		{mobileBriefInput{}, []morningBriefCandidate{item}, []morningBriefCandidate{{ID: "uncertain", Confidence: .7}}},
	} {
		if morningBriefQuietEligible(tc.input, tc.gathered, tc.admitted) {
			t.Fatalf("unproven quiet input accepted: %+v", tc)
		}
	}
}

func TestScheduledMorningQuietBriefIsShortAndBound(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	svc, _ := newTestMobileBriefService(t, &fakeMobileBriefClock{now: now})
	svc.app.mobileBriefSvc, svc.compose = svc, nil
	work, err := handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc.app.handoffStore = work
	entry := handoff.Entry{ID: "finished-plan", Title: "Finished plan", Source: handoff.SourceMobile, Status: handoff.StatusCompleted, CreatedAt: now.Add(-time.Hour).UnixMilli(), UpdatedAt: now.Add(-time.Minute).UnixMilli()}
	if _, err := work.Save(entry); err != nil {
		t.Fatal(err)
	}
	svc.gather = func(_ context.Context, slot string, start, asOf time.Time) (mobileBriefInput, error) {
		input := mobileBriefInput{Slot: slot, WindowStart: start, AsOf: asOf, Location: time.UTC}
		collectMobileBriefWork(svc.app, &input)
		return input, nil
	}
	svc.modelCall = func(context.Context, string) (string, error) {
		scores := morningBriefScores{Items: []morningBriefScore{{ID: mobileBriefItemKey("candidate", "history", entry.ID), Critique: "Already finished.", Score: 0, Pick: "omit"}}}
		data, err := json.Marshal(scores)
		return string(data), err
	}
	svc.publish = func(MobileBriefV1, bool) error { return nil }
	brief, err := svc.generate(context.Background(), mobileBriefSlotMorning, mobileBriefReasonSchedule)
	if err != nil {
		t.Fatal(err)
	}
	svc.wg.Wait()
	if len(brief.Narrative) != 1 || mobileBriefNarrativeWords(brief.Narrative) > 20 || brief.modelBinding == nil || len(brief.Narrative[0].Refs) == 0 {
		t.Fatalf("quiet brief is not short and bound: %+v", brief.Narrative)
	}
}

func TestScheduledMorningWithoutCandidatesUsesOnlyStaticEmptyText(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	svc, _ := newTestMobileBriefService(t, &fakeMobileBriefClock{now: now})
	svc.app.mobileBriefSvc, svc.compose = svc, nil
	svc.gather = func(_ context.Context, slot string, start, asOf time.Time) (mobileBriefInput, error) {
		return mobileBriefInput{Slot: slot, WindowStart: start, AsOf: asOf, Location: time.UTC}, nil
	}
	svc.modelCall = func(context.Context, string) (string, error) {
		t.Fatal("empty input should not reach the model")
		return "", nil
	}
	svc.publish = func(MobileBriefV1, bool) error { return nil }
	brief, err := svc.generate(context.Background(), mobileBriefSlotMorning, mobileBriefReasonSchedule)
	if err != nil {
		t.Fatal(err)
	}
	svc.wg.Wait()
	stored, found := svc.store.load(brief.BriefID)
	if !found || !mobileBriefStaticEmpty(stored) || len(svc.app.mobileBriefForDelivery(stored).Narrative) != 1 {
		t.Fatalf("empty brief was not stored and delivered: %+v", stored.Narrative)
	}
	stored.Narrative[0].Text = "An unbound claim."
	if len(svc.app.mobileBriefForDelivery(stored).Narrative) != 0 {
		t.Fatal("changed unbound text survived delivery")
	}
}
