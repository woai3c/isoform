package main

import (
	"reflect"
	"strings"
	"testing"
)

// A TEMP view makes only the projection's correction-provenance read fail.
// It leaves the underlying fixture data and writer revision unchanged, so a
// bad cached result cannot escape the recovery guard through invalidation.
func failActivityThreadStretchRead(t *testing.T, app *App) func() {
	t.Helper()
	reader := app.activity.store.reader()
	reader.SetMaxOpenConns(1)
	// The fixture reader uses query_only; disable it only while changing its
	// connection-local TEMP schema. The database itself remains read-only.
	if _, err := reader.Exec(`PRAGMA query_only=OFF`); err != nil {
		t.Fatal(err)
	}
	defer reader.Exec(`PRAGMA query_only=ON`)
	if _, err := reader.Exec(`CREATE TEMP VIEW activity_thread_corrections AS SELECT * FROM activity_thread_unavailable_corrections`); err != nil {
		t.Fatal(err)
	}
	repaired := false
	repair := func() {
		t.Helper()
		if !repaired {
			if _, err := reader.Exec(`PRAGMA query_only=OFF`); err != nil {
				t.Fatal(err)
			}
			defer reader.Exec(`PRAGMA query_only=ON`)
			if _, err := reader.Exec(`DROP VIEW temp.activity_thread_corrections`); err != nil {
				t.Fatal(err)
			}
			repaired = true
		}
	}
	t.Cleanup(repair)
	return repair
}

func TestActivityThreadStretchReadFailureReturnsError(t *testing.T) {
	app, first, _, day := seedActivityThreadEvidenceFixture(t)
	baseline := activityThreadViewForMoment(t, app, day, first.ID)
	if len(baseline.Stretches) == 0 {
		t.Fatal("missing healthy stretch control")
	}
	activityThreadCacheForget(day)
	failActivityThreadStretchRead(t, app)
	if got, err := app.GetDayThreads(day, day+activityThreadDayMs-1); err == nil || !strings.Contains(err.Error(), "activity_thread_unavailable_corrections") {
		t.Fatalf("stretch read failure returned success or lost its cause: day=%+v err=%v", got, err)
	}
}

func TestActivityThreadStretchReadFailureIsNotCached(t *testing.T) {
	app, _, _, day := seedActivityThreadEvidenceFixture(t)
	baseline, err := app.GetDayThreads(day, day+activityThreadDayMs-1)
	if err != nil || len(baseline.Threads) == 0 || len(baseline.Threads[0].Stretches) == 0 {
		t.Fatalf("missing healthy day control: %+v %v", baseline, err)
	}
	activityThreadCacheForget(day)
	signature, cacheable, err := app.activityThreadProjectionSignature(t.Context(), day)
	if err != nil || !cacheable {
		t.Fatalf("fixture is not cacheable: %v", err)
	}
	repair := failActivityThreadStretchRead(t, app)
	_, _ = app.GetDayThreads(day, day+activityThreadDayMs-1)
	activityThreadCacheMu.Lock()
	_, cached := activityThreadCache[activityThreadCacheKey{store: app.activity.store, day: day}]
	activityThreadCacheMu.Unlock()
	// Repair before asserting so recovery is tested with the same signature,
	// even on the pre-fix implementation which caches its degraded result.
	repair()
	after, cacheable, err := app.activityThreadProjectionSignature(t.Context(), day)
	if err != nil || !cacheable || after != signature {
		t.Fatalf("repair invalidated the cache rather than exercising recovery: %v", err)
	}
	got, err := app.GetDayThreads(day, day+activityThreadDayMs-1)
	if err != nil {
		t.Fatal(err)
	}
	if cached || !reflect.DeepEqual(got, baseline) {
		t.Fatalf("failed read was cached or hid recovered stretches: cached=%v got=%+v want=%+v", cached, got, baseline)
	}
}
