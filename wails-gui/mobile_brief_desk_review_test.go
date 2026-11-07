package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"wails-gui/internal/handoff"
	"wails-gui/storage"
)

func TestTodayUnchangedReadsDoNotRewriteFiles(t *testing.T) {
	for _, generated := range []bool{false, true} {
		t.Run(map[bool]string{false: "local", true: "generated"}[generated], func(t *testing.T) {
			rig := newHandoffTestRig(t)
			clock := &fakeMobileBriefClock{now: mobileBriefTestDay(7, 15)}
			svc, _ := newTestMobileBriefService(t, clock)
			svc.app = rig.app
			// The work gives the brief content; an empty brief is quiet, and a
			// quiet slot is never the current brief.
			if _, err := rig.handoffs.Save(handoff.Entry{ID: "work", Title: "Review", Status: handoff.StatusActive, CreatedAt: mobileBriefTestDay(7, 0).UnixMilli(), UpdatedAt: mobileBriefTestDay(7, 0).UnixMilli()}); err != nil {
				t.Fatal(err)
			}
			if generated {
				svc.checkDue(context.Background())
				svc.wg.Wait()
			}
			_, ok := svc.LatestMobileBrief()
			if !generated && !ok {
				t.Fatal("missing brief")
			}
			names := []string{mobileBriefTodayFileName}
			if generated {
				// A generated brief may be quiet, and a quiet slot is never current,
				// so the stored brief is named from the store, not the choice.
				newest, found := svc.store.latest()
				if !found {
					t.Fatal("no brief was stored")
				}
				names = []string{newest.BriefID + ".json", "latest.json"}
			}
			check := func() {
				t.Helper()
				before := map[string]os.FileInfo{}
				for _, name := range names {
					info, err := os.Stat(filepath.Join(svc.store.dir, name))
					if err != nil {
						t.Fatal(err)
					}
					before[name] = info
				}
				for range 3 {
					svc.LatestMobileBrief()
				}
				for _, name := range names {
					after, err := os.Stat(filepath.Join(svc.store.dir, name))
					if err != nil {
						t.Fatal(err)
					}
					if !os.SameFile(before[name], after) || !before[name].ModTime().Equal(after.ModTime()) {
						t.Errorf("unchanged read rewrote %s", name)
					}
				}
			}
			check()
			if !generated {
				if _, err := rig.handoffs.UpdateStatus("work", handoff.StatusInProgress); err != nil {
					t.Fatal(err)
				}
				if _, err := rig.handoffs.UpdateStatus("work", handoff.StatusCompleted); err != nil {
					t.Fatal(err)
				}
				svc.LatestMobileBrief()
				check() // An empty local snapshot must also settle after one write.
			}
		})
	}
}

func TestTodayCollectionDoesNotLockBriefPatches(t *testing.T) {
	rig := newHandoffTestRig(t)
	now := mobileBriefTestDay(7, 15)
	svc, _ := newTestMobileBriefService(t, &fakeMobileBriefClock{now: now})
	svc.app = rig.app
	seedMobileBriefReadableHistory(t, rig.app, "h-morning")
	svc.checkDue(context.Background())
	svc.wg.Wait()
	loaded := make(chan struct{})
	rig.app.cronScheduler = &CronScheduler{loadedCh: loaded}
	entered, done := make(chan struct{}), make(chan MobileBriefV1, 1)
	var once sync.Once
	svc.now = func() time.Time { once.Do(func() { close(entered) }); return now }
	go func() { b, _ := svc.LatestMobileBrief(); done <- b }()
	<-entered
	unlocked := svc.patchMu.TryLock()
	if unlocked {
		latest, _ := svc.store.latest()
		latest.Audio = &MobileBriefAudio{Status: "downloading"}
		if err := svc.store.save(latest, now); err != nil {
			t.Error(err)
		}
		svc.patchMu.Unlock()
	}
	close(loaded)
	b := <-done
	if !unlocked {
		t.Fatal("source collection held patchMu while waiting for cron load")
	}
	// The patch is a source change: the brief is sealed again in the background,
	// and the next read shows it. The read that raced the patch may show the copy
	// from before it (reads never seal).
	svc.wg.Wait()
	b, _ = svc.LatestMobileBrief()
	if b.Audio == nil || b.Audio.Status != "downloading" {
		t.Fatal("refresh lost a concurrent audio patch")
	}
}

func TestTodayReadsDoNotWaitForAudioPublication(t *testing.T) {
	rig := newHandoffTestRig(t)
	now := mobileBriefTestDay(7, 15)
	svc, _ := newTestMobileBriefService(t, &fakeMobileBriefClock{now: now})
	svc.app = rig.app
	svc.compose = testModelCompose // model text, as a model brief has (design R74)
	if _, err := rig.handoffs.Save(handoff.Entry{ID: "h-morning", Title: "Shipped the plan", Source: "manual", Status: handoff.StatusCompleted}); err != nil {
		t.Fatal(err)
	}
	svc.checkDue(context.Background())
	svc.wg.Wait()
	b, _ := svc.store.latest()
	svc.speaker = fakeSpeaker("en", nil, nil)
	svc.speaker.voiceReady = func(string) bool { return false }
	svc.speaker.downloadVoice = func(context.Context, string) bool { return true }
	publishing, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	svc.publish = func(current MobileBriefV1, notify bool) error {
		if current.Audio == nil {
			t.Error("audio was not persisted before publication")
		}
		close(publishing)
		<-release
		return nil
	}
	go func() { svc.addAudio(context.Background(), b, now); close(done) }()
	select {
	case <-publishing:
	case <-time.After(5 * time.Second):
		t.Fatal("readable audio never reached publication")
	}
	unlocked := svc.patchMu.TryLock()
	if unlocked {
		svc.patchMu.Unlock()
	}
	close(release)
	<-done
	if !unlocked {
		t.Fatal("audio publication held the brief read lock")
	}
}

func TestTodaySnapshotExpiresByDateAndRetention(t *testing.T) {
	clock := &fakeMobileBriefClock{now: time.Date(2026, 10, 9, 23, 59, 0, 0, time.Local)}
	svc, _ := newTestMobileBriefService(t, clock)
	svc.location = time.Local
	b := buildMobileBriefV1(mobileBriefInput{Slot: mobileBriefSlotOnRequest, AsOf: clock.Now(), Location: time.Local})
	if err := svc.store.saveCurrentToday(b); err != nil {
		t.Fatal(err)
	}
	if _, ok := svc.GetMobileBrief(b.BriefID); !ok {
		t.Fatal("today snapshot missing")
	}
	clock.Advance(2 * time.Minute)
	if got, ok := svc.GetMobileBrief(b.BriefID); ok || got.BriefID != "" {
		t.Error("yesterday snapshot served by id")
	}
	svc.store.mu.Lock()
	svc.store.sweepLocked(clock.Now().AddDate(0, 0, mobileBriefRetentionDays))
	svc.store.mu.Unlock()
	if _, err := os.Stat(filepath.Join(svc.store.dir, mobileBriefTodayFileName)); !os.IsNotExist(err) {
		t.Fatal("expired today.json escaped retention")
	}
}

func TestDailyFindsRequiresGeneratedBrief(t *testing.T) {
	rig := newHandoffTestRig(t)
	svc, _ := newTestMobileBriefService(t, &fakeMobileBriefClock{now: mobileBriefTestDay(7, 15)})
	svc.app = rig.app
	seedMobileBriefReadableHistory(t, rig.app, "h-morning")
	rig.app.mobileBriefSvc = svc
	if _, err := rig.handoffs.Save(handoff.Entry{ID: "work", Title: "Review", Status: handoff.StatusActive, CreatedAt: mobileBriefTestDay(7, 0).UnixMilli(), UpdatedAt: mobileBriefTestDay(7, 0).UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	if _, ok := svc.LatestMobileBrief(); !ok {
		t.Fatal("local snapshot missing")
	}
	if _, ok := rig.app.latestMobileBrief(); ok {
		t.Error("daily finds accepted a local snapshot as a generated brief")
	}
	svc.checkDue(context.Background())
	svc.wg.Wait()
	if b, ok := rig.app.latestMobileBrief(); !ok || b.Slot != mobileBriefSlotMorning {
		t.Fatal("generated morning brief must open the daily finds gate")
	}
}

func TestTodayLaunchReadRetainsOneOwnedService(t *testing.T) {
	rig := newHandoffTestRig(t)
	t.Setenv("YANSU_MOBILE_STATE_DIR", t.TempDir())
	if _, err := rig.app.GetLatestBrief(); err != nil {
		t.Fatal(err)
	}
	owner := rig.app.mobileBriefSource()
	if owner == nil {
		t.Fatal("launch read created a throwaway service")
	}
	if _, err := rig.app.GetLatestBrief(); err != nil {
		t.Fatal(err)
	}
	if rig.app.mobileBriefSource() != owner {
		t.Fatal("launch reads replaced the app-owned service")
	}
	if owner.(*mobileBriefService).started {
		t.Fatal("launch read started scheduler before stores were ready")
	}
}

func TestTodaySchedulesStoreStructureAndLocalizedUntitledNames(t *testing.T) {
	for _, lang := range []string{"en", "zh-CN"} {
		t.Run(lang, func(t *testing.T) {
			rig := newHandoffTestRig(t)
			if err := rig.state.SetUIState(context.Background(), uiLanguageStateKey, lang); err != nil {
				t.Fatal(err)
			}
			now := mobileBriefTestDay(8, 0)
			job := storage.CronJobRow{ID: "unnamed", Enabled: true, NextRunAt: now.Add(time.Hour).UnixMilli(), ScheduleKind: "cron", ScheduleConfig: `{"expr":"0 9 * * 1-5","tz":"Asia/Shanghai"}`, TargetKind: "agent", TargetConfig: `{}`}
			if err := rig.state.SaveCronJob(context.Background(), job); err != nil {
				t.Fatal(err)
			}
			if err := rig.state.InsertCronRun(context.Background(), storage.CronRunRow{ID: "failed", JobID: job.ID, Status: "failed", StartedAt: now.Add(-time.Minute).UnixMilli()}); err != nil {
				t.Fatal(err)
			}
			in := mobileBriefInput{Slot: mobileBriefSlotMorning, AsOf: now, Location: time.UTC}
			collectMobileBriefToday(rig.app, &in)
			b := buildMobileBriefV1(in)
			if len(b.Upcoming) != 1 {
				t.Fatal("missing schedule")
			}
			want := "Automation"
			if lang == "zh-CN" {
				want = "自动化"
			}
			if len(b.Sections.Schedules) != 1 || b.Sections.Schedules[0].Title != want {
				t.Errorf("unnamed failed run is not localized: %+v", b.Sections.Schedules)
			}
			if b.Upcoming[0].Title != want {
				t.Errorf("unnamed title=%q, want %q", b.Upcoming[0].Title, want)
			}
			data, _ := json.Marshal(b.Upcoming[0].Context)
			var extra map[string]any
			_ = json.Unmarshal(data, &extra)
			spec, ok := extra["scheduleSpec"].(map[string]any)
			if !ok || spec["kind"] != "cron" || spec["expression"] != "0 9 * * 1-5" || spec["timezone"] != "Asia/Shanghai" || spec["nextRunAt"] != float64(job.NextRunAt) {
				t.Fatalf("schedule is not structured: %s", data)
			}
			if extra["schedule"] != nil {
				t.Fatal("English schedule description still stored")
			}
		})
	}
}

func TestFrontendReadmeOnlyDocumentsShippedOnboarding(t *testing.T) {
	data, err := os.ReadFile("frontend/src/README.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, "onboarding-screen.tsx") && strings.Contains(line, "stand"+"alone") {
			t.Fatal("README documents removed onboarding order")
		}
	}
}

func TestTodayReadsDoNotWaitForFindsPublication(t *testing.T) {
	rig := newHandoffTestRig(t)
	now := mobileBriefTestDay(7, 15)
	svc, _ := newTestMobileBriefService(t, &fakeMobileBriefClock{now: now})
	svc.app = rig.app
	svc.checkDue(context.Background())
	svc.wg.Wait()
	publishing, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		svc.PatchLatestMobileBrief(func(b MobileBriefV1) (MobileBriefV1, bool) { b.NarrativeStatus = "updated"; return b, true }, func(MobileBriefV1) { close(publishing); <-release })
		close(done)
	}()
	<-publishing
	unlocked := svc.patchMu.TryLock()
	if unlocked {
		svc.patchMu.Unlock()
	}
	close(release)
	<-done
	if !unlocked {
		t.Fatal("finds publication held the brief read lock")
	}
}

func TestBriefDeliveryReadsNewestPatchAfterWaiting(t *testing.T) {
	now := mobileBriefTestDay(7, 15)
	svc, _ := newTestMobileBriefService(t, &fakeMobileBriefClock{now: now})
	svc.checkDue(context.Background())
	svc.wg.Wait()
	original, _ := svc.store.latest()
	svc.deliveryMu.Lock()
	delivered := make(chan MobileBriefV1, 1)
	go svc.deliverStoredBrief(original, func(b MobileBriefV1) { delivered <- b })
	changed := original
	changed.Audio = &MobileBriefAudio{Status: "downloading"}
	if err := svc.store.save(changed, now); err != nil {
		svc.deliveryMu.Unlock()
		t.Fatal(err)
	}
	svc.deliveryMu.Unlock()
	if b := <-delivered; b.Audio == nil {
		t.Fatal("queued send delivered a stale patch")
	}
	changed.AsOf = formatMobileBriefTime(now.Add(time.Minute))
	if err := svc.store.save(changed, now); err != nil {
		t.Fatal(err)
	}
	svc.deliverStoredBrief(original, func(MobileBriefV1) { t.Fatal("old generation delivered over its replacement") })
}
