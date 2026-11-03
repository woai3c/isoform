package main

import (
	"fmt"
	"testing"
	"time"

	"wails-gui/storage"
)

func seedMCPBriefPurposeTree(t *testing.T, a *App, groups, perGroup int) []string {
	t.Helper()
	store, err := a.purposeProductStore()
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour).UnixMilli()
	ids := make([]string, 0, groups)
	for group := 0; group < groups; group++ {
		events := make([]storage.CalendarEventRow, 0, perGroup)
		evidence := make([]PurposeEvidenceInput, 0, perGroup)
		for i := 0; i < perGroup; i++ {
			id := fmt.Sprintf("budget-event-%d-%d", group, i)
			events = append(events, storage.CalendarEventRow{ID: id, Source: "eventkit", Provider: "apple", Title: "Reviewed event", StartAt: at, EndAt: at + 60000})
			ev := purposeTestEvidence(id, PurposeEvidenceExplicitUser, PurposeStanceSupport, at)
			ev.SourceKind, ev.SourceID = mobileBriefCalendarSource, id
			evidence = append(evidence, ev)
		}
		if err := a.store.UpsertCalendarEvents(a.ctx, events, at); err != nil {
			t.Fatal(err)
		}
		key := fmt.Sprintf("budget-purpose-%d", group)
		purpose, _, err := store.Create(purposeTestCreateInput(key, PurposeKindCommitment, key, evidence, at))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, purpose.OpenLoop.ID)
	}
	return ids
}

func storeMCPBriefReferences(t *testing.T, sources ...MobileBriefRef) {
	t.Helper()
	brief := mobileBriefStoredBrief(t, time.Now().Format("2006-01-02"), mobileBriefSlotMorning, 1)
	brief.Sections = MobileBriefSections{}
	brief.Evidence = nil
	brief.Limits.TruncatedSections = nil
	for i, source := range sources {
		source.Ref = fmt.Sprint("budget-ref-", i)
		brief.Evidence = append(brief.Evidence, source)
		brief.Sections.Moved = append(brief.Sections.Moved, MobileBriefItem{ID: source.Ref, Refs: []string{source.Ref}})
	}
	storeMCPBrief(t, brief)
}

func TestMCPReadBriefWorkBudgetIsPerItem(t *testing.T) {
	a := newMCPReadTestApp(t)
	ids := seedMCPBriefPurposeTree(t, a, 4, 80)
	refs := make([]MobileBriefRef, 0, len(ids))
	for _, id := range ids {
		refs = append(refs, MobileBriefRef{SourceKind: "purpose", SourceID: id})
	}
	storeMCPBriefReferences(t, refs...)
	got := callMCPReadTest(t, a, "brief_today", map[string]any{})
	if len(got.Items) != 4 || got.Truncated || !got.Bounded || got.Withheld != 0 || got.Omitted != 0 {
		t.Fatalf("earlier items consumed a later item's work budget: %+v", got)
	}
}

func TestMCPReadBriefWorkLimitsAreOmittedNotPrivate(t *testing.T) {
	for _, mode := range []string{"references", "depth", "evidence"} {
		t.Run(mode, func(t *testing.T) {
			a := newMCPReadTestApp(t)
			store, err := a.purposeProductStore()
			if err != nil {
				t.Fatal(err)
			}
			at := time.Now().Add(-time.Hour).UnixMilli()
			var id string
			switch mode {
			case "references":
				children := seedMCPBriefPurposeTree(t, a, 3, 90)
				evidence := make([]PurposeEvidenceInput, 0, len(children))
				for _, child := range children {
					ev := purposeTestEvidence(child, PurposeEvidenceExplicitUser, PurposeStanceSupport, at)
					ev.SourceKind, ev.SourceID = "purpose", child
					evidence = append(evidence, ev)
				}
				root, _, err := store.Create(purposeTestCreateInput("budget-parent", PurposeKindCommitment, "parent", evidence, at))
				if err != nil {
					t.Fatal(err)
				}
				id = root.OpenLoop.ID
			case "depth":
				id = seedMCPBriefPurposeTree(t, a, 1, 1)[0]
				for i := 0; i < 7; i++ {
					key := fmt.Sprint("budget-depth-", i)
					ev := purposeTestEvidence(key, PurposeEvidenceExplicitUser, PurposeStanceSupport, at)
					ev.SourceKind, ev.SourceID = "purpose", id
					root, _, err := store.Create(purposeTestCreateInput(key, PurposeKindCommitment, key, []PurposeEvidenceInput{ev}, at))
					if err != nil {
						t.Fatal(err)
					}
					id = root.OpenLoop.ID
				}
			case "evidence":
				id = seedMCPBriefPurposeTree(t, a, 1, 101)[0]
			}
			storeMCPBriefReferences(t,
				MobileBriefRef{SourceKind: "purpose", SourceID: id},
				MobileBriefRef{SourceKind: mobileBriefCalendarSource, SourceID: "budget-event-0-0"},
				MobileBriefRef{SourceKind: "unknown", SourceID: "private"},
			)
			got := callMCPReadTest(t, a, "brief_today", map[string]any{})
			if len(got.Items) != 1 || got.Items[0].Source != "calendar_event" || !got.Truncated || got.Bounded || got.Omitted != 1 || got.Withheld != 1 {
				t.Fatalf("%s work limit was mislabeled or poisoned a later item: %+v", mode, got)
			}
		})
	}
}
