package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/handoff"
	"wails-gui/storage"
)

func TestTodayRefreshesStoredSignalsBetweenSlots(t *testing.T) {
	rig := newHandoffTestRig(t)
	clock := &fakeMobileBriefClock{now: mobileBriefTestDay(7, 0)}
	svc, gathers := newTestMobileBriefService(t, clock)
	svc.app, rig.app.mobileBriefSvc = rig.app, svc
	seedMobileBriefReadableHistory(t, rig.app, "h-morning")
	brief, err := svc.generate(context.Background(), mobileBriefSlotMorning, mobileBriefReasonSchedule)
	if err != nil {
		t.Fatal(err)
	}
	if len(brief.Evidence) == 0 {
		t.Fatal("readable morning fixture lost its evidence")
	}
	brief.Narrative = []MobileBriefPara{{Text: "The morning recap.", Refs: []string{brief.Evidence[0].Ref}}}
	brief.NarrativeStatus = "ok"
	brief.Counts.OpenLoopsByKind["commitment"] = 37
	brief.Counts.LearningItems = 29
	brief.Audio = &MobileBriefAudio{}
	if err := svc.store.save(brief, clock.Now()); err != nil {
		t.Fatal(err)
	}
	originalWindow := svc.windowStart(mobileBriefTestDay(18, 0))
	clock.Set(mobileBriefTestDay(11, 0))
	addTodayRefreshSources(t, rig, clock.Now())
	view, err := rig.app.GetLatestBrief()
	if err != nil || view.Brief == nil {
		t.Fatalf("view = %+v, %v", view, err)
	}
	assertTodayRefreshSources(t, *view.Brief)
	if view.Brief.Counts.OpenLoopsByKind["commitment"] != 37 || view.Brief.Counts.LearningItems != 29 {
		t.Fatal("refresh lost counts for records beyond the displayed section cap")
	}
	if view.Brief.BriefID != brief.BriefID || view.Brief.AsOf != brief.AsOf || view.Brief.Audio == nil || len(view.Brief.Narrative) != 1 || view.Brief.Narrative[0].Text != brief.Narrative[0].Text {
		t.Fatalf("refresh replaced the generated recap: %+v", view.Brief)
	}
	assertTodayRefsResolve(t, *view.Brief)
	// Today shows the stored brief through the read path, which checks its
	// cards against the cards as delivered now (mobileBriefWithCards).
	stored, ok := svc.store.load(brief.BriefID)
	if !ok || !reflect.DeepEqual(rig.app.mobileBriefWithCards(rig.app.mobileBriefForDelivery(stored)), *view.Brief) {
		t.Fatal("Today was not persisted in the shared brief")
	}
	if gathers.Load() != 1 || !svc.windowStart(mobileBriefTestDay(18, 0)).Equal(originalWindow) || len(svc.lastOnDemand) != 0 {
		t.Fatal("refresh generated a brief or consumed its window/cooldown")
	}

	// A completed task, disabled schedule and deleted insight must disappear;
	// summary edits and new failures must not accumulate duplicate rows or refs.
	if _, err := rig.handoffs.UpdateStatus("new-task", handoff.StatusInProgress); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.handoffs.UpdateStatus("new-task", handoff.StatusCompleted); err != nil {
		t.Fatal(err)
	}
	if err := rig.state.SaveCronJob(context.Background(), storage.CronJobRow{ID: "new-job", Name: "New automation", Enabled: false, ScheduleKind: "cron", ScheduleConfig: `{"expr":"0 12 * * *"}`, TargetKind: "agent", TargetConfig: `{"prompt":"review"}`}); err != nil {
		t.Fatal(err)
	}
	dir, _ := memoryDir()
	if err := os.Remove(filepath.Join(dir, "live-note.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.activity.SaveSummaryWithLanguage("2026-10-09", "daily", "Revised day summary", "test", false, rig.app.currentUILanguage()); err != nil {
		t.Fatal(err)
	}
	next, err := rig.app.GetLatestBrief()
	if err != nil || next.Brief == nil || next.DailySummary == nil || len(next.Brief.Upcoming) != 0 || len(next.Brief.Sections.OpenLoops) != 0 || len(next.Brief.Sections.Learned) != 0 || next.DailySummary.Text != "Revised day summary" {
		t.Fatalf("stale Today content: %+v, %v", next.Brief, err)
	}
	assertTodayRefsResolve(t, *next.Brief)
	again, _ := rig.app.GetLatestBrief()
	if !reflect.DeepEqual(again.Brief, next.Brief) {
		t.Fatal("unchanged reads changed the stored content")
	}
}

func TestTodayWithoutScheduledBriefStillStoresCurrentWork(t *testing.T) {
	for _, mode := range []string{"before-first-slot", "schedule-off", "before-service-start"} {
		t.Run(mode, func(t *testing.T) {
			rig := newHandoffTestRig(t)
			now := mobileBriefTestDay(6, 0)
			if mode == "before-service-start" {
				now = time.Now()
			}
			clock := &fakeMobileBriefClock{now: now}
			svc, gathers := newTestMobileBriefService(t, clock)
			svc.app = rig.app
			if mode != "before-service-start" {
				rig.app.mobileBriefSvc = svc
			} else {
				svc.location = time.Local
			}
			if mode == "schedule-off" {
				settings := svc.config.load()
				settings.Enabled = false
				if err := svc.config.save(settings); err != nil {
					t.Fatal(err)
				}
			}
			addTodayRefreshSources(t, rig, now)
			view, err := rig.app.GetLatestBrief()
			if mode == "before-service-start" && view.PendingSlot != "" && (!view.FirstBrief || view.LastBriefID != "") {
				// today-review v1.1.11 #1: once a slot is due and no brief has
				// ever been made, "Your first brief" sits above the snapshot.
				t.Fatalf("first brief card missing above the snapshot: %+v", view)
			}
			if err != nil || view.Brief == nil || view.Empty {
				t.Fatalf("live work hidden without a scheduled brief: %+v, %v", view, err)
			}
			assertTodayRefreshSources(t, *view.Brief)
			// 06:00 is before the morning slot: the snapshot is the morning,
			// never "evening" from the hour it was written (2026-10-27).
			if mode != "before-service-start" && view.Brief.DisplaySlot != mobileBriefSlotMorning {
				t.Fatalf("the 06:00 snapshot is named %q, want morning", view.Brief.DisplaySlot)
			}
			if mode != "before-service-start" && view.PendingSlot != "" {
				t.Fatalf("nothing is on its way before the first slot: %+v", view)
			}
			if view.Enabled != (mode != "schedule-off") {
				t.Fatalf("schedule switch changed: %+v", view)
			}
			latest, ok := svc.LatestMobileBrief()
			if !ok || !reflect.DeepEqual(latest, *view.Brief) {
				t.Fatal("phone and Today disagree")
			}
			loaded, ok := svc.GetMobileBrief(latest.BriefID)
			if !ok || !reflect.DeepEqual(loaded, latest) {
				t.Fatal("current brief not stored/readable by ID")
			}
			if gathers.Load() != 0 || len(svc.lastOnDemand) != 0 || !svc.windowStart(now).Equal(now.Add(-mobileBriefDefaultWindow)) {
				t.Fatal("local refresh consumed generation state")
			}
			if _, ok := svc.store.latest(); ok {
				t.Fatal("local snapshot replaced scheduled brief history")
			}
		})
	}
}

func addTodayRefreshSources(t *testing.T, rig *handoffTestRig, now time.Time) {
	t.Helper()
	ctx := context.Background()
	if err := rig.state.SaveCronJob(ctx, storage.CronJobRow{ID: "new-job", Name: "New automation", Enabled: true, NextRunAt: now.Add(time.Hour).UnixMilli(), ScheduleKind: "cron", ScheduleConfig: `{"expr":"0 12 * * *"}`, TargetKind: "agent", TargetConfig: `{"prompt":"review"}`}); err != nil {
		t.Fatal(err)
	}
	if err := rig.state.InsertCronRun(ctx, storage.CronRunRow{ID: "new-failure", JobID: "new-job", Status: "failed", Error: "Needs retry", StartedAt: now.Add(-time.Minute).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.handoffs.Save(handoff.Entry{ID: "new-task", Title: "New task", Status: handoff.StatusActive, UpdatedAt: now.Add(-time.Minute).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "live-note.md")
	if err := os.WriteFile(path, []byte("# New insight\nRemember this."), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, now.Add(-time.Minute), now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	checkMemoryFolderNow(rig.app)
	if _, err := rig.activity.SaveSummaryWithLanguage(now.Format("2006-01-02"), "daily", "Current day summary", "test", false, rig.app.currentUILanguage()); err != nil {
		t.Fatal(err)
	}
}

func assertTodayRefreshSources(t *testing.T, brief MobileBriefV1) {
	t.Helper()
	if len(brief.Upcoming) != 1 || brief.Upcoming[0].Context == nil || brief.Upcoming[0].Context.JobID != "new-job" || len(brief.Sections.Schedules) != 1 || brief.Sections.Schedules[0].Context == nil || brief.Sections.Schedules[0].Context.Preview != "Needs retry" || len(brief.Sections.OpenLoops) != 1 || len(brief.Sections.Learned) != 1 {
		t.Fatalf("live signals missing: %+v", brief)
	}
}

func assertTodayRefsResolve(t *testing.T, brief MobileBriefV1) {
	t.Helper()
	refs := map[string]bool{}
	for _, ref := range brief.Evidence {
		if refs[ref.Ref] {
			t.Fatalf("duplicate ref %s", ref.Ref)
		}
		refs[ref.Ref] = true
	}
	if len(refs) > mobileBriefEvidenceLimit {
		t.Fatal("evidence budget exceeded")
	}
	check := func(ids []string) {
		for _, id := range ids {
			if !refs[id] {
				t.Fatalf("dangling ref %s", id)
			}
		}
	}
	for _, section := range mobileBriefSectionLines(brief.Sections) {
		for _, item := range section.items {
			check(item.Refs)
		}
	}
	for _, item := range brief.Upcoming {
		check(item.Refs)
	}
	for _, para := range brief.Narrative {
		check(para.Refs)
	}
}

func TestTodayRefreshWithdrawsSynthesisWhenCrowdingDropsDependencies(t *testing.T) {
	rig := newHandoffTestRig(t)
	now := mobileBriefTestDay(11, 0)
	addTodayRefreshSources(t, rig, now)
	in := mobileBriefInput{AsOf: now.Add(-4 * time.Hour), Location: time.UTC}
	for sectionIndex, section := range []*[]mobileBriefCandidate{&in.Moved, &in.Doing, &in.Watching, &in.Meetings, &in.People, &in.OpenLoops, &in.Learned, &in.Deliverables, &in.Schedules} {
		for i := range mobileBriefItemsPerSection {
			*section = append(*section, mobileBriefCandidate{SourceKind: "history", SourceID: fmt.Sprintf("record-%d-%d", sectionIndex, i), Title: "Recap item"})
		}
	}
	base := buildMobileBriefV1(in)
	cited := base.Evidence[len(base.Evidence)-1]
	base.Narrative = []MobileBriefPara{{Text: "The original recap.", Refs: []string{cited.Ref}}}
	base.NarrativeStatus = "ok"
	base.Audio = &MobileBriefAudio{Status: mobileBriefAudioStatusOK, Mime: mobileBriefAudioMimeWAV, Bytes: 4}
	base.DayShape = &MobileBriefDay{ActiveMinutes: 42}
	refreshed := refreshMobileBriefToday(rig.app, base, now, time.UTC)
	if len(refreshed.Narrative) != 0 || refreshed.NarrativeStatus != "unavailable" || refreshed.Audio != nil || refreshed.DayShape != nil {
		t.Fatal("crowding dropped original dependencies but retained their synthesis")
	}
	if len(refreshed.Sections.Schedules) != 1 || refreshed.Sections.Schedules[0].Context == nil || refreshed.Sections.Schedules[0].Context.JobID != "new-job" {
		t.Fatal("refresh lost the failed automation action")
	}
	if len(refreshed.Upcoming) != 1 {
		t.Fatal("recap evidence displaced upcoming work")
	}
	assertTodayRefsResolve(t, refreshed)
	// Live actions keep their priority; the original synthesis cannot use
	// its one retained citation as proof that every other input survived.
	retainedOriginal := 0
	for _, ref := range refreshed.Evidence {
		if strings.HasPrefix(ref.SourceID, "record-") {
			retainedOriginal++
		}
	}
	if retainedOriginal >= len(base.Evidence) {
		t.Fatal("fixture did not displace an original dependency")
	}
}

// The live refresh rebuilds every section. Before it read the Purpose Map again,
// opening Today or fetching the latest phone brief erased the goals,
// commitments and waiting-on-you rows of a generated brief.
func TestMobileBriefTodayRefreshKeepsThePurposeMap(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	now := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	detail := mobileBriefPurposeFixture("refresh-goal", now)
	in := mobileBriefInput{AsOf: now, Location: time.UTC}
	collectMobileBriefPurposes(app, &in, []PurposeOpenLoopDetail{detail}, nil)
	base := buildMobileBriefV1(in)
	if len(base.Sections.WorkingToward) != 1 {
		t.Fatalf("the generated brief has no working-toward row to keep: %+v", base.Sections)
	}
	previous := mobileBriefRefreshPurposeDetails
	t.Cleanup(func() { mobileBriefRefreshPurposeDetails = previous })

	mobileBriefRefreshPurposeDetails = func(*App) []PurposeOpenLoopDetail { return []PurposeOpenLoopDetail{detail} }
	refreshed := refreshMobileBriefToday(app, base, now.Add(time.Minute), time.UTC)
	if len(refreshed.Sections.WorkingToward) != 1 || refreshed.Sections.WorkingToward[0].Title != "Purpose refresh-goal" {
		t.Fatalf("the refresh erased the Purpose Map: %+v", refreshed.Sections.WorkingToward)
	}
	assertTodayRefsResolve(t, refreshed)

	mobileBriefRefreshPurposeDetails = func(*App) []PurposeOpenLoopDetail { return nil }
	for _, mode := range []string{"current", "legacy-primary-evidence", "unavailable-evidence"} {
		t.Run(mode, func(t *testing.T) {
			stale := base
			item := base.Sections.WorkingToward[0]
			item.PurposeID = detail.OpenLoop.ID
			switch mode {
			case "legacy-primary-evidence":
				item.PurposeID = ""
			case "unavailable-evidence":
				item.EvidenceUnavailable = true
				item.Refs = nil
			}
			stale.Sections.WorkingToward = []MobileBriefItem{item}
			stale.Sections.OpenLoops = []MobileBriefItem{item}
			refreshed := refreshMobileBriefToday(app, stale, now.Add(2*time.Minute), time.UTC)
			if len(refreshed.Sections.WorkingToward) != 0 || len(refreshed.Sections.OpenLoops) != 0 {
				t.Fatalf("a Purpose that is no longer current stayed in the brief: %+v", refreshed.Sections)
			}
		})
	}
}

func TestMobileBriefLatestTodayRefreshesCurrentPurposeLedger(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	t.Setenv("YANSU_MOBILE_STATE_DIR", t.TempDir())
	store := NewPurposeMapStore(app.activity.store.db)
	detail := mobileTestPurpose(t, store, "today-current-purpose", PurposeKindGoal, "Review the project plan", false, nil)
	now := time.Now()
	svc := newMobileBriefService(app)
	svc.location = time.UTC
	svc.now = func() time.Time { return now }
	base := buildMobileBriefV1(mobileBriefInput{Slot: mobileBriefSlotMorning, AsOf: now, WindowStart: now.Add(-time.Hour), Location: time.UTC})
	if err := svc.store.save(base, now); err != nil {
		t.Fatal(err)
	}

	latest, ok := svc.latestWithToday()
	if !ok || len(latest.Sections.WorkingToward) != 1 || latest.Sections.WorkingToward[0].Title != detail.Current.Title || len(latest.Sections.OpenLoops) != 1 {
		t.Fatalf("production Today read did not collect the current Purpose: %+v", latest.Sections)
	}
	assertTodayRefsResolve(t, latest)
	input, err := userPurposeAppendInput(detail, userPurposeTransition{Key: "today-resolve-purpose", Origin: "desktop", Action: userPurposeResolve}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.AppendEvent(input); err != nil {
		t.Fatal(err)
	}
	now = time.Now()
	latest, ok = svc.latestWithToday()
	if !ok || len(latest.Sections.WorkingToward) != 0 || len(latest.Sections.OpenLoops) != 0 {
		t.Fatalf("production Today read restored the completed Purpose: %+v", latest.Sections)
	}
	stored, ok := svc.store.load(base.BriefID)
	if !ok || len(stored.Sections.WorkingToward) != 0 || len(stored.Sections.OpenLoops) != 0 {
		t.Fatalf("stored Today kept the completed Purpose: %+v", stored.Sections)
	}
}
