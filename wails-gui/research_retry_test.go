package main

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"wails-gui/storage"
)

func TestResearchUnavailableRunDoesNotRetryRetiredNews(t *testing.T) {
	previousRegenerate, previousLaunch := todayNewsRegenerate, appLaunchedAt
	t.Cleanup(func() {
		todayNewsRegenerate, appLaunchedAt = previousRegenerate, previousLaunch
		todayNewsStaleRetried.Store(false)
		todayNewsRetryPending.Store("")
	})
	for _, status := range []string{"ok", "error"} {
		t.Run(status, func(t *testing.T) {
			rig := newHandoffTestRig(t)
			ctx := context.Background()
			rig.app.ctx = ctx
			job := upcomingTestJob("retired-news", todayNewsJobName, "manual", `{}`, true)
			job.SurfaceKind = todayNewsSurfaceKind
			if err := rig.state.SaveCronJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			if err := rig.state.InsertCronRun(ctx, storage.CronRunRow{ID: "old-run", JobID: job.ID, StartedAt: time.Now().Add(-time.Hour).UnixMilli(), Status: status, TriggerKind: "manual", ResultSummary: "[command timed out]"}); err != nil {
				t.Fatal(err)
			}
			ordinary := upcomingTestJob("ordinary", todayNewsJobName, "manual", `{}`, true)
			if err := rig.state.SaveCronJob(ctx, ordinary); err != nil {
				t.Fatal(err)
			}
			if err := rig.state.InsertCronRun(ctx, storage.CronRunRow{ID: "ordinary-run", JobID: ordinary.ID, StartedAt: 1, Status: "ok", TriggerKind: "manual"}); err != nil {
				t.Fatal(err)
			}
			scheduler := NewCronScheduler(rig.state, func(string, any) {}, func(string, string) {}, nil)
			rig.app.cronScheduler = scheduler
			if err := scheduler.loadJobs(ctx); err != nil {
				t.Fatal(err)
			}
			close(scheduler.loadedCh)
			if _, err := rig.state.LoadCronJob(ctx, job.ID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("retired job remained: %v", err)
			}
			if runs, err := rig.state.ListCronRuns(ctx, job.ID, 10); err != nil || len(runs) != 0 {
				t.Fatalf("retired runs remained: %+v, %v", runs, err)
			}
			if runs, err := rig.state.ListCronRuns(ctx, ordinary.ID, 10); err != nil || len(runs) != 1 || runs[0].ID != "ordinary-run" {
				t.Fatalf("ordinary schedule history changed: %+v, %v", runs, err)
			}
			appLaunchedAt = time.Now().Add(time.Minute)
			dispatched := make(chan struct{}, 1)
			todayNewsRegenerate = func(*App) error { dispatched <- struct{}{}; return nil }
			data, err := rig.app.GetTodayNews()
			if err != nil || (data.Status != "none" && data.Status != "unconfigured") {
				t.Fatalf("retired news read = %+v, %v", data, err)
			}
			select {
			case <-dispatched:
				t.Fatal("retired news was retried")
			case <-time.After(100 * time.Millisecond):
			}
			if err := rig.app.GenerateTodayNews(); err == nil {
				t.Fatal("retired news was recreated")
			}
			if _, err := rig.state.LoadCronJob(ctx, job.ID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("retired job returned after read: %v", err)
			}
		})
	}
}
