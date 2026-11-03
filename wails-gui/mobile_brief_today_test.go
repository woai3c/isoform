package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/handoff"
	"wails-gui/storage"
)

func TestMobileBriefTodayCollectsAndStoresLocalSignals(t *testing.T) {
	rig := newHandoffTestRig(t)
	ctx := context.Background()
	loc := time.FixedZone("PDT", -7*60*60)
	asOf := time.Date(2026, 10, 15, 12, 0, 0, 0, loc)
	start := asOf.Add(-12 * time.Hour)
	for _, job := range []storage.CronJobRow{
		{ID: "due", Name: "Review inbox", Enabled: true, NextRunAt: asOf.Add(time.Hour).UnixMilli()},
		{ID: "overdue", Name: "Send update", Enabled: true, NextRunAt: asOf.Add(-time.Hour).UnixMilli()},
		{ID: "later", Enabled: true, NextRunAt: asOf.Add(49 * time.Hour).UnixMilli()},
		{ID: "off", Enabled: false, NextRunAt: asOf.Add(time.Hour).UnixMilli()},
		{ID: "system", Name: todayNewsJobName, SurfaceKind: todayNewsSurfaceKind, Enabled: true, NextRunAt: asOf.Add(time.Hour).UnixMilli()},
	} {
		job.ScheduleKind, job.ScheduleConfig = "cron", `{"expr":"0 7 * * *"}`
		job.TargetKind, job.TargetConfig = "agent", `{"prompt":"Review"}`
		if err := rig.state.SaveCronJob(ctx, job); err != nil {
			t.Fatal(err)
		}
	}
	for _, run := range []storage.CronRunRow{
		{ID: "failed", JobID: "due", Status: "failed", Error: "Connection closed", StartedAt: start.Add(time.Hour).UnixMilli()},
		{ID: "yesterday", JobID: "due", Status: "failed", Error: "Old error", StartedAt: start.Add(-time.Minute).UnixMilli()},
		{ID: "hidden", JobID: "system", Status: "failed", Error: "Hidden error", StartedAt: start.Add(time.Hour).UnixMilli()},
	} {
		if err := rig.state.InsertCronRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	for _, entry := range []handoff.Entry{
		{ID: "moved", Title: "Review draft", Body: "## Draft is ready", Status: handoff.StatusNeedsReview, UpdatedAt: asOf.Add(-time.Hour).UnixMilli()},
		{ID: "old-open", Title: "Waiting on access", Body: "Need a login", Status: handoff.StatusBlocked, BlockedReason: "interrupted", UpdatedAt: asOf.Add(-48 * time.Hour).UnixMilli()},
	} {
		if _, err := rig.handoffs.Save(entry); err != nil {
			t.Fatal(err)
		}
	}
	memory, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(memory, "preference.md")
	if err := os.WriteFile(path, []byte("# Short updates\nKeep updates short."), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, start.Add(time.Hour), start.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	knowledge, err := knowledgeDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(knowledge, "release.md"), []byte("# Release checklist\n- **Date**: "+start.Add(2*time.Hour).Format(time.RFC3339)+"\n- **Apps**: Figma\nReview the release."), 0600); err != nil {
		t.Fatal(err)
	}
	if err := rig.state.SaveChatDeliverable(ctx, storage.ChatDeliverableRow{
		ID: "deck", Title: "Release deck", Summary: "Ready to review", Files: `["/tmp/deck.pdf","/tmp/notes.md"]`, UpdatedAt: start.Add(time.Hour).UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	// The store stamps writes with wall time; align this fixture with the brief's clock.
	db, err := sql.Open("sqlite", filepath.Join(os.Getenv("HOME"), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE chat_deliverables SET updated_at = ? WHERE id = 'deck'`, start.Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.activity.SaveSummaryWithLanguage("2026-10-15", "daily", "A productive morning.", "test", false, rig.app.currentUILanguage()); err != nil {
		t.Fatal(err)
	}

	in := mobileBriefInput{Slot: "on_request", Location: loc, AsOf: asOf, WindowStart: asOf.Add(-2 * time.Hour)}
	collectMobileBriefWork(rig.app, &in)
	// A source already carried by the brief is enriched, never appended twice.
	in.Learned = []mobileBriefCandidate{{SourceKind: "memory", SourceID: "preference", Title: "Short updates", At: start.Add(time.Hour)}}
	collectMobileBriefToday(rig.app, &in)
	brief := buildMobileBriefV1(in)
	if len(brief.Upcoming) != 2 || brief.Upcoming[0].Title != "Send update" || brief.Upcoming[1].Context.JobID != "due" {
		t.Fatalf("upcoming = %+v", brief.Upcoming)
	}
	if len(brief.Sections.Schedules) != 1 || brief.Sections.Schedules[0].Context.Preview != "Connection closed" || brief.Sections.Schedules[0].Context.JobID != "due" {
		t.Fatalf("failed schedules = %+v", brief.Sections.Schedules)
	}
	if len(brief.Sections.Moved) != 1 || brief.Sections.Moved[0].Context.Preview != "Draft is ready" || len(brief.Sections.OpenLoops) != 1 || brief.Sections.OpenLoops[0].Context.BlockedReason != "interrupted" {
		t.Fatalf("work = %+v / %+v", brief.Sections.Moved, brief.Sections.OpenLoops)
	}
	if len(brief.Sections.Learned) != 2 {
		t.Fatalf("learned = %+v", brief.Sections.Learned)
	}
	for _, item := range brief.Sections.Learned {
		if item.Context == nil || item.Context.Preview == "" {
			t.Fatalf("missing stored preview: %+v", item)
		}
	}
	if len(brief.Sections.Deliverables) != 1 || brief.Sections.Deliverables[0].Context.FileCount != 2 || brief.Sections.Deliverables[0].Context.Preview != "Ready to review" {
		t.Fatalf("deliverables = %+v", brief.Sections.Deliverables)
	}
	store := newMobileBriefStore(t.TempDir())
	if err := store.save(brief, asOf); err != nil {
		t.Fatal(err)
	}
	loaded, ok := store.load(brief.BriefID)
	if !ok || !reflect.DeepEqual(loaded, brief) {
		t.Fatal("the stored brief lost Today content")
	}
}

func TestMobileBriefTodayFiltersAndBoundsContext(t *testing.T) {
	in := mobileBriefGoldenInput()
	in.Learned = append(in.Learned, mobileBriefCandidate{SourceKind: "memory", SourceID: "secret", Title: "Private record", Protected: true})
	in.Today = &mobileBriefTodayInput{
		Context: map[string]MobileBriefItemContext{
			mobileBriefSourceKey("memory", "secret"): {Preview: "Secret context"},
			mobileBriefSourceKey("memory", "mem-1"):  {Preview: strings.Repeat("界", 1000), SourceLabel: "User note"},
		},
	}
	for _, id := range []string{"d", "c", "b", "a"} {
		in.Today.Upcoming = append(in.Today.Upcoming, mobileBriefCandidate{SourceKind: "schedule", SourceID: id, Title: id, At: in.AsOf.Add(time.Hour)})
	}
	brief := buildMobileBriefV1(in)
	raw, err := json.Marshal(brief)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "Secret context") || strings.Contains(string(raw), "Private record") {
		t.Fatal("context bypassed the brief's privacy filter")
	}
	for _, item := range brief.Sections.Learned {
		if item.Context != nil && (len(item.Context.Preview) > mobileBriefTextLimit || item.Context.SourceLabel != "User note") {
			t.Fatalf("unbounded or lost context: %+v", item.Context)
		}
	}
	if len(brief.Upcoming) != 3 || brief.Upcoming[0].Title != "a" || !slices.Contains(brief.Limits.TruncatedSections, "upcoming") {
		t.Fatalf("upcoming cap/order: %+v, %+v", brief.Upcoming, brief.Limits)
	}
	if prompt := mobileBriefTestPrompt(brief, "English"); strings.Contains(prompt, "User note") || strings.Contains(prompt, "界") || strings.Contains(prompt, strings.Repeat("x", 100)) {
		t.Fatal("Today context was sent to the narrative model")
	}
	for _, item := range brief.Upcoming {
		if !slices.ContainsFunc(brief.Evidence, func(ref MobileBriefRef) bool { return ref.Ref == item.Refs[0] && ref.SourceKind == "schedule" }) {
			t.Fatalf("unresolved upcoming evidence: %+v", item)
		}
	}
}

func TestMobileBriefTodaySharesEvidenceBudget(t *testing.T) {
	in := mobileBriefGoldenInput()
	for _, section := range []*[]mobileBriefCandidate{&in.Moved, &in.Doing, &in.Watching, &in.Meetings, &in.People, &in.OpenLoops, &in.Learned, &in.Deliverables, &in.Schedules} {
		*section = nil
		for i := range mobileBriefItemsPerSection {
			*section = append(*section, mobileBriefCandidate{SourceKind: "history", SourceID: fmt.Sprintf("%p-%d", section, i), Title: "Busy day"})
		}
	}
	in.Today = &mobileBriefTodayInput{Context: map[string]MobileBriefItemContext{}}
	for _, id := range []string{"c", "b", "a"} {
		in.Today.Upcoming = append(in.Today.Upcoming, mobileBriefCandidate{SourceKind: "schedule", SourceID: id, Title: id, At: in.AsOf.Add(time.Hour)})
		in.Today.Context[mobileBriefSourceKey("schedule", id)] = MobileBriefItemContext{JobID: id}
	}
	brief := buildMobileBriefV1(in)
	if len(brief.Evidence) != mobileBriefEvidenceLimit || len(brief.Upcoming) != 3 || slices.Contains(brief.Limits.TruncatedSections, "upcoming") {
		t.Fatalf("upcoming work lost to the evidence budget: %d upcoming, %d refs, %+v", len(brief.Upcoming), len(brief.Evidence), brief.Limits)
	}
	for i, id := range []string{"a", "b", "c"} {
		item := brief.Upcoming[i]
		if item.Context == nil || item.Context.JobID != id || !slices.ContainsFunc(brief.Evidence, func(ref MobileBriefRef) bool { return ref.Ref == item.Refs[0] && ref.SourceID == id }) {
			t.Fatalf("upcoming action lost its source: %+v", item)
		}
	}
}

func TestMobileBriefTodayAloneIsNotEmpty(t *testing.T) {
	for _, today := range []*mobileBriefTodayInput{
		{Upcoming: []mobileBriefCandidate{{SourceKind: "schedule", SourceID: "job", Title: "Job"}}},
	} {
		brief := buildMobileBriefV1(mobileBriefInput{AsOf: time.Now(), ColdStart: true, Today: today})
		if mobileBriefIsEmpty(brief) || brief.ColdStart {
			t.Fatal("stored Today content was treated as an empty cold start")
		}
	}
}
