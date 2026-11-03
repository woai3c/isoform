package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/handoff"
)

// A batch leaves a card whose source needs a full collection for its click.
func TestTodayChatBatchDefersCollectorSources(t *testing.T) {
	a := newTodayContextTestApp(t, false)
	brief := MobileBriefV1{BriefID: "brief-1", AsOf: time.Now().Format(time.RFC3339)}
	for _, kind := range []string{mobileBriefWebSource, mobileBriefFileSource, mobileBriefDayShapeSource} {
		_, err := a.resolveTodayChatBriefEvidence(context.Background(), MobileBriefRef{Ref: "e1", SourceKind: kind, SourceID: "x"}, brief, todayChatSources{deferCollectors: true}, time.Now())
		if !errors.Is(err, errTodayChatDeferred) {
			t.Fatalf("%s in a batch: %v", kind, err)
		}
	}
	if _, err := a.resolveTodayChatBriefEvidence(context.Background(), MobileBriefRef{Ref: "e1", SourceKind: "memory", SourceID: "x"}, brief, todayChatSources{deferCollectors: true}, time.Now()); errors.Is(err, errTodayChatDeferred) {
		t.Fatal("a one-record source was deferred")
	}
}

// A card joining Isoform's own record with captured text keeps the full
// redaction on the joined text; on its own the owned text keeps its dates.
func TestTodayChatJoinedCardTextKeepsCapturedRedaction(t *testing.T) {
	a := newTodayContextTestApp(t, false)
	now := time.Now()
	handoffDate := now.AddDate(0, 0, -1).Format("2006-01-02")
	memoryDate := now.AddDate(0, 0, 1).Format("2006-01-02")
	handoffTitle := "Atlas review on " + handoffDate
	store, err := handoff.NewStore(filepath.Join(os.Getenv("HOME"), "handoffs"))
	if err != nil {
		t.Fatal(err)
	}
	a.handoffStore = store
	if _, err := store.Save(handoff.Entry{ID: "h1", Title: handoffTitle, Status: handoff.StatusActive, Source: handoff.SourceMobile}); err != nil {
		t.Fatal(err)
	}
	writeTodayContextMemory(t, "note", "# Vendor call\n\n- **Source**: user_edit\n\nThe vendor call is on "+memoryDate+".")
	brief := buildMobileBriefV1(mobileBriefInput{Slot: "morning", AsOf: now.Add(-time.Hour), WindowStart: now.Add(-24 * time.Hour)})
	brief.Evidence = []MobileBriefRef{{Ref: "e1", SourceKind: "handoff", SourceID: "h1", Label: "Atlas review"}, {Ref: "e2", SourceKind: "memory", SourceID: "note", Label: "Vendor call"}}
	brief.Sections.OpenLoops = []MobileBriefItem{{ID: "joined", Title: "Atlas review", Kind: "one_time_work", Refs: []string{"e1", "e2"}}, {ID: "alone", Title: "Atlas review", Kind: "one_time_work", Refs: []string{"e1"}}}
	dir, _ := mobileBriefStateDir()
	if err := newMobileBriefStore(dir).save(brief, now); err != nil {
		t.Fatal(err)
	}
	joined, err := a.buildTodayChatContext(context.Background(), TodayChatContextRef{SourceKind: "brief_item", SourceID: "joined", BriefID: brief.BriefID})
	if err != nil {
		t.Fatal(err)
	}
	item := joined.Text[strings.Index(joined.Text, "<item>")+len("<item>") : strings.Index(joined.Text, "</item>")]
	var record todayChatRecord
	if err := json.Unmarshal([]byte(item), &record); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(record.Text, "Atlas review on ") || !strings.Contains(record.Text, "The vendor call is on ") {
		t.Fatalf("joined card text lost a source:\n%s", record.Text)
	}
	if strings.Contains(record.Text, memoryDate) || strings.Contains(record.Text, handoffDate) {
		t.Fatalf("joined card text kept a date:\n%s", record.Text)
	}
	alone, err := a.buildTodayChatContext(context.Background(), TodayChatContextRef{SourceKind: "brief_item", SourceID: "alone", BriefID: brief.BriefID})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(alone.Text, handoffTitle+"; status") {
		t.Fatalf("owned card text lost its date:\n%s", alone.Text)
	}
}

// Fact values pass the owned redaction at the boundary too.
func TestTodayChatRenderedFactsKeepBoundaryRedaction(t *testing.T) {
	record := todayChatRecord{Kind: "purpose", ID: "p", Text: "x", RenderedFacts: todayChatRenderedFacts{{Name: "due", Value: "2026-10-20 Tue (tomorrow)"}, {Name: "note", Value: "code 482913"}}}
	packed, _ := packTodayChatSection([]todayChatRecord{record}, todayChatItemSectionTokens)
	if !strings.Contains(packed, "2026-10-20 Tue (tomorrow)") || strings.Contains(packed, "482913") {
		t.Fatalf("facts at the boundary: %s", packed)
	}
}

// The brief's current items name each record in <evidence> by its title only;
// the record itself is in <brief-items>.
func TestTodayChatBriefCurrentItemsNameEvidenceByTitle(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	brief := boundMemoryConversationFixture(t, app)
	records, refs := app.todayChatBriefCurrentItems(context.Background(), brief, app.collectTodayChatSources(context.Background(), time.Now()), time.Now())
	if len(records) == 0 || len(refs) != len(records) {
		t.Fatalf("records %d, refs %d", len(records), len(refs))
	}
	for i, ref := range refs {
		if ref.Label != records[i].Title || strings.Contains(ref.Label, "Prepare the release") {
			t.Fatalf("evidence %d label = %q, record title %q", i, ref.Label, records[i].Title)
		}
	}
}

// A settled item's past due date is not called overdue.
func TestTodayChatSettledDueIsNotOverdue(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.Local)
	due := time.Date(2026, 10, 17, 23, 59, 59, 0, time.Local).UnixMilli()
	open := renderTodayChatFacts([]todayChatFact{{Name: "due", At: due, DateOnly: true}}, now)
	settled := renderTodayChatFacts([]todayChatFact{{Name: "due", At: due, DateOnly: true, Settled: true}}, now)
	if !strings.Contains(open[0].Value, "overdue") || strings.Contains(settled[0].Value, "overdue") {
		t.Fatalf("open %q, settled %q", open[0].Value, settled[0].Value)
	}
	for state, want := range map[string]bool{PurposeStateResolved: true, PurposeStateDismissed: true, PurposeStateSuperseded: true, PurposeStateContradicted: true, PurposeStateActive: false, PurposeStateWaiting: false} {
		if todayChatPurposeSettled(state) != want {
			t.Errorf("settled(%s) != %t", state, want)
		}
	}
}

// One read proves each brief revision once: a remembered answer is reused.
func TestTodayChatBriefProofIsProvedOncePerRead(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	brief := boundMemoryConversationFixture(t, app)
	bindConversationBriefFixture(t, app, &brief)
	sources := app.collectTodayChatSources(context.Background(), time.Now())
	if !app.todayChatBriefFrozen(context.Background(), brief, sources) {
		t.Fatal("bound brief not proven")
	}
	key := brief.BriefID + "|" + mobileBriefConversationKey(brief)
	if proven, known := sources.briefProofs[key]; !known || !proven {
		t.Fatalf("proof not remembered: %v", sources.briefProofs)
	}
	sources.briefProofs[key] = false
	if app.todayChatBriefFrozen(context.Background(), brief, sources) {
		t.Fatal("the remembered proof was not reused")
	}
}
