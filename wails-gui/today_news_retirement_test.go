package main

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"wails-gui/storage"
)

func TestRetiredTodayNewsDeletesJobAndRuns(t *testing.T) {
	rig := newHandoffTestRig(t)
	ctx := context.Background()
	job := storage.CronJobRow{
		ID: "retired-news", Name: todayNewsJobName, SurfaceKind: todayNewsSurfaceKind,
		Enabled: true, ScheduleKind: "manual", ScheduleConfig: `{}`,
		TargetKind: "agent", TargetConfig: `{}`, NextRunAt: time.Now().Add(-time.Minute).UnixMilli(),
	}
	if err := rig.state.SaveCronJob(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err := rig.state.InsertCronRun(ctx, storage.CronRunRow{ID: "old-news-run", JobID: job.ID, StartedAt: 1, Status: "ok", TriggerKind: "manual"}); err != nil {
		t.Fatal(err)
	}
	ordinary := job
	ordinary.ID, ordinary.SurfaceKind = "user-news", ""
	if err := rig.state.SaveCronJob(ctx, ordinary); err != nil {
		t.Fatal(err)
	}
	if err := rig.state.SaveProject(ctx, storage.ProjectRow{ID: "p", Name: "p", Path: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if err := rig.state.SaveSession(ctx, storage.SessionRow{LocalID: "news-chat", ProjectID: "p", Label: "Story discussion"}); err != nil {
		t.Fatal(err)
	}
	s := NewCronScheduler(rig.state, func(string, any) {}, func(string, string) {}, nil)
	if err := s.loadJobs(ctx); err != nil {
		t.Fatal(err)
	}
	stored, err := rig.state.LoadCronJob(ctx, job.ID)
	if !errors.Is(err, sql.ErrNoRows) || stored != nil {
		t.Fatalf("retired job = %+v, error %v", stored, err)
	}
	if s.dispatchJob(ctx, &job, "catchup") {
		t.Fatal("retired news dispatched")
	}
	if err := s.RunNow(ctx, job.ID); err == nil {
		t.Fatal("retired news ran manually")
	}
	on := true
	if _, err := s.UpdateJob(ctx, job.ID, CronJobPatch{Enabled: &on}); err == nil {
		t.Fatal("retired news was re-enabled")
	}
	if _, err := s.AddJob(ctx, CronJobInput{Name: todayNewsJobName, SurfaceKind: todayNewsSurfaceKind}); err == nil {
		t.Fatal("retired news was recreated")
	}
	if err := rig.app.GenerateTodayNews(); err == nil {
		t.Fatal("retired news was generated")
	}
	runs, err := rig.state.ListCronRuns(ctx, job.ID, 10)
	if err != nil || len(runs) != 0 {
		t.Fatalf("history = %+v, error %v", runs, err)
	}
	if _, err := rig.state.LoadCronJob(ctx, ordinary.ID); err != nil {
		t.Fatalf("user job removed: %v", err)
	}
	if chat, err := rig.state.LoadSession(ctx, "news-chat"); err != nil || chat == nil {
		t.Fatalf("chat removed: %+v, %v", chat, err)
	}
	if err := s.loadJobs(ctx); err != nil {
		t.Fatalf("second load: %v", err)
	}
}
