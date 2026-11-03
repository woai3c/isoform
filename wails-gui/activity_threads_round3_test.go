package main

import (
	"reflect"
	"testing"
	"time"
)

func seedActivityThreadOutcomeDay(t *testing.T) (*App, ActivityRow, int64) {
	t.Helper()
	app := newThreadTestApp(t)
	day, dayMs := threadTestDay(t)
	fixture := threadOutcomeFixtures(t)[0]
	row := seedThreadFixture(t, app, "reader-outcome", fixture, at(day, 18, 0))
	row.DayStartMs = dayMs
	if err := app.activity.store.UpdateActivity(row); err != nil {
		t.Fatal(err)
	}
	if err := app.activity.store.UpsertActivityRunLink(composeParseStrings(row.RunIDs)[0], row.ID, 1, row.EndMs); err != nil {
		t.Fatal(err)
	}
	if _, err := app.MoveActivityToThread(row.ID, ""); err != nil {
		t.Fatal(err)
	}
	return app, saveFixtureOutcome(t, app, row, fixture), dayMs
}

func TestActivityThreadProjectionReadsWhileCaptureIsOccupied(t *testing.T) {
	for _, warm := range []bool{false, true} {
		name := "cold"
		if warm {
			name = "warm"
		}
		t.Run(name, func(t *testing.T) {
			app, row, day := seedActivityThreadOutcomeDay(t)
			baseline, err := app.GetDayThreads(day, day+activityThreadDayMs-1)
			if err != nil || len(baseline.Threads) != 1 || !baseline.Threads[0].Unfinished || len(baseline.Threads[0].Stretches) != 1 {
				t.Fatalf("missing populated projection control: %+v %v", baseline, err)
			}
			if !warm {
				activityThreadCacheForget(day)
			}
			tx, err := app.activity.store.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := tx.Exec(`UPDATE activities SET title='Uncommitted' WHERE id=?`, row.ID); err != nil {
				t.Fatal(err)
			}
			type result struct {
				value ActivityDayThreads
				err   error
			}
			done := make(chan result, 1)
			go func() {
				value, err := app.GetDayThreads(day, day+activityThreadDayMs-1)
				done <- result{value, err}
			}()
			select {
			case got := <-done:
				if got.err != nil || !reflect.DeepEqual(got.value, baseline) {
					t.Fatalf("reader did not retain committed projection: %+v %v", got.value, got.err)
				}
			case <-time.After(5 * time.Second):
				tx.Rollback()
				<-done
				t.Fatal("thread projection queued behind capture")
			}
		})
	}
}

func TestActivityThreadCacheRejectsReopenedReaderRevision(t *testing.T) {
	app, row, day := seedActivityThreadOutcomeDay(t)
	// Both old and new physical readers start at data_version=1. A counter
	// without its connection identity would serve the withdrawn outcome.
	reader := app.activity.store.readDB
	reader.SetMaxIdleConns(0)
	reader.SetMaxIdleConns(4)
	baseline, err := app.GetDayThreads(day, day+activityThreadDayMs-1)
	if err != nil || len(baseline.Threads) != 1 || !baseline.Threads[0].Unfinished {
		t.Fatalf("missing healthy warm cache: %+v %v", baseline, err)
	}
	before, _, err := app.activityThreadProjectionSignature(t.Context(), day)
	if err != nil {
		t.Fatal(err)
	}
	reader.SetMaxIdleConns(0)
	if _, err := app.activity.store.db.Exec(`UPDATE activities SET thread_outcome='' WHERE id=?`, row.ID); err != nil {
		t.Fatal(err)
	}
	reader.SetMaxIdleConns(4)
	after, _, err := app.activityThreadProjectionSignature(t.Context(), day)
	if err != nil || before.version != after.version || before.connection == after.connection {
		t.Fatalf("fixture did not recreate equal counters on different readers: before=%+v after=%+v err=%v", before, after, err)
	}
	got, err := app.GetDayThreads(day, day+activityThreadDayMs-1)
	if err != nil || len(got.Threads) != 1 || got.Threads[0].Unfinished {
		t.Fatalf("reopened reader served withdrawn outcome: %+v %v", got, err)
	}
}

func TestActivityThreadOpenPauseWithdrawsAndRecoversOutcome(t *testing.T) {
	app, row, day := seedActivityThreadOutcomeDay(t)
	baseline, err := app.GetDayThreads(day, day+activityThreadDayMs-1)
	if err != nil || len(baseline.Threads) != 1 || !baseline.Threads[0].Unfinished {
		t.Fatalf("missing healthy warm cache: %+v %v", baseline, err)
	}
	start := max(row.EndMs+1, time.Now().Add(time.Hour).UnixMilli())
	app.saveObservationPauses([]observationPause{{Start: start}})
	got, err := app.GetDayThreads(day, day+activityThreadDayMs-1)
	if err != nil || len(got.Threads) != 1 || got.Threads[0].Unfinished {
		t.Fatalf("open pause did not withdraw warm outcome: %+v %v", got, err)
	}
	if ordinary := app.readableThreadOutcome(t.Context(), row); ordinary != nil {
		t.Fatal("ordinary reader disagrees with the paused projection")
	}
	// An ended future interval does not pause the current state or overlap
	// the evidence. Both readers must recover without waiting for cache TTL.
	app.saveObservationPauses([]observationPause{{Start: start, End: start + 1}})
	recovered, err := app.GetDayThreads(day, day+activityThreadDayMs-1)
	if err != nil || !reflect.DeepEqual(recovered, baseline) || app.readableThreadOutcome(t.Context(), row) == nil {
		t.Fatalf("closed pause did not recover original outcome: %+v %v", recovered, err)
	}
}

func TestActivityThreadCacheTracksFallbackReaderWrites(t *testing.T) {
	app, row, day := seedActivityThreadOutcomeDay(t)
	store := app.activity.store
	previous := store.readWriter
	store.readWriter = nil // Exercise reader()'s in-memory/migration fallback.
	t.Cleanup(func() { store.readWriter = previous })
	baseline, err := app.GetDayThreads(day, day+activityThreadDayMs-1)
	if err != nil || len(baseline.Threads) != 1 || !baseline.Threads[0].Unfinished {
		t.Fatalf("missing healthy fallback cache: %+v %v", baseline, err)
	}
	if _, err := store.db.Exec(`UPDATE activities SET thread_outcome='' WHERE id=?`, row.ID); err != nil {
		t.Fatal(err)
	}
	got, err := app.GetDayThreads(day, day+activityThreadDayMs-1)
	if err != nil || len(got.Threads) != 1 || got.Threads[0].Unfinished {
		t.Fatalf("fallback reader served withdrawn outcome: %+v %v", got, err)
	}
}
