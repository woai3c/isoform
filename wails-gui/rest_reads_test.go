package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"wails-gui/internal/interests"
)

// CPU round 4 (today-review v1.1.18 R24): the menu bar's five-second status
// tick rebuilt the latest brief through every delivery gate, even with the
// screen locked. It reads the saved finished brief instead.
func TestMenuBarTickReadsTheSavedBrief(t *testing.T) {
	r := newMemoryReadCountRig(t)
	app := r.app
	if _, err := app.menuBarView("en", false); err != nil {
		t.Fatal(err)
	}
	probe := instrumentPeopleProfileQueryCounts(t, app)
	clear(r.loads)
	const ticks = 3
	started := time.Now()
	for i := 0; i < ticks; i++ {
		view, err := app.menuBarView("en", false)
		if err != nil {
			t.Fatal(err)
		}
		if view.Brief.Kind == "none" {
			t.Fatalf("tick %d: the menu bar lost the brief: %+v", i, view.Brief)
		}
	}
	elapsed := time.Since(started) / ticks
	counts := probe.queryCountsSnapshot()
	perTick := (counts["state"] + counts["activity"]) / ticks
	t.Logf("menu bar tick with nothing changed: %d SQLite queries, %s per tick", perTick, elapsed.Round(time.Microsecond))
	if perTick > 30 {
		t.Errorf("the menu bar tick ran %d queries; want the saved brief read", perTick)
	}
}

func restTopicInterests(t *testing.T, app *App) []string {
	t.Helper()
	t.Setenv(topicsStateDirEnvVar, t.TempDir())
	var list []interests.Interest
	for i := 0; i < 20; i++ {
		it := testInterest(fmt.Sprintf("i-harbor-%02d", i), fmt.Sprintf("Harbor Lane topic %d", i), "You plan Harbor Lane walks.", "harbor lane walk")
		it.Evidence = []interests.Ref{{Key: fmt.Sprintf("about:harbor-note-%04d#0", i), Source: interests.SourceAbout, At: time.Now().Add(-time.Hour).UnixMilli()}}
		list = append(list, it)
	}
	searched := testInterest("i-harbor-search", "Harbor Lane routes", "You searched for routes.", "harbor lane routes")
	searched.Evidence = []interests.Ref{{Key: "search:" + strings.Repeat("a", 16), Source: interests.SourceSearch, At: time.Now().Add(-time.Hour).UnixMilli()}}
	list = append(list, searched)
	storeTestInterests(t, app, list...)
	ids := func() []string {
		var out []string
		for _, it := range app.interestsSvc().list() {
			out = append(out, it.ID)
		}
		return out
	}
	return ids()
}

func readableInterestIDs(app *App) []string {
	var out []string
	for _, it := range app.interestsSvc().list() {
		out = append(out, it.ID)
	}
	return out
}

// Today's topics re-checked every interest's evidence on each read, which
// collects every source again for an interest named from a search. With
// nothing changed a read reuses the decisions.
func TestTopicFindsRestWhenNothingChanged(t *testing.T) {
	rig, _ := newMemoryPrivacyQueryRig(t)
	app := rig.app
	restTopicInterests(t, app)
	probe := instrumentPeopleProfileQueryCounts(t, app)
	if _, err := app.GetTopicFinds(); err != nil {
		t.Fatal(err)
	}
	probe.reset()
	const repeats = 3
	started := time.Now()
	for i := 0; i < repeats; i++ {
		if _, err := app.GetTopicFinds(); err != nil {
			t.Fatal(err)
		}
	}
	elapsed := time.Since(started) / repeats
	counts := probe.queryCountsSnapshot()
	perRead := (counts["state"] + counts["activity"]) / repeats
	t.Logf("GetTopicFinds at %d notes, 21 interests, nothing changed: %d SQLite queries, %s per read", privacyQueryNotes, perRead, elapsed.Round(time.Microsecond))
	if perRead > 10 {
		t.Errorf("GetTopicFinds ran %d queries per read with nothing changed", perRead)
	}
}

func TestTopicInterestPrivacyChangesApplyOnTheNextRead(t *testing.T) {
	rig, _ := newMemoryPrivacyQueryRig(t)
	app := rig.app
	before := restTopicInterests(t, app)
	if !slices.Contains(before, "i-harbor-01") {
		t.Fatalf("fixture: readable interests %v", before)
	}
	_ = readableInterestIDs(app)

	if err := app.SetClosedEyesModeEnabled(true); err != nil {
		t.Fatal(err)
	}
	if got := readableInterestIDs(app); len(got) != 0 {
		t.Fatalf("Closed Eyes on: %d interests read", len(got))
	}
	if err := app.SetClosedEyesModeEnabled(false); err != nil {
		t.Fatal(err)
	}
	if got := readableInterestIDs(app); !slices.Equal(got, before) {
		t.Fatalf("Closed Eyes off: %v, want %v", got, before)
	}

	if err := app.DeleteMemoryEntry("harbor-note-0001"); err != nil {
		t.Fatal(err)
	}
	if got := readableInterestIDs(app); slices.Contains(got, "i-harbor-01") {
		t.Fatal("an interest whose note was forgotten was still read")
	}

	if err := rig.activity.UpdateTriageStatus("harbor-session-0002", "protected"); err != nil {
		t.Fatal(err)
	}
	if got := readableInterestIDs(app); slices.Contains(got, "i-harbor-02") {
		t.Fatal("an interest whose source session was protected was still read")
	}
}

// With the screen locked or the display asleep, the status poll skips its
// ticks; the first refresh and a nudge still run.
func TestDoingNowLoopRestsWhileTheScreenIsLocked(t *testing.T) {
	var resting atomic.Bool
	resting.Store(true)
	previousResting, previousTick := doingNowResting, doingNowTickEvery
	doingNowResting, doingNowTickEvery = resting.Load, 20*time.Millisecond
	t.Cleanup(func() { doingNowResting, doingNowTickEvery = previousResting, previousTick })
	app := &App{activeTags: map[string]bool{}}
	next := doingNowEventKinds(t, app)
	app.startDoingNow(context.Background())
	t.Cleanup(app.stopDoingNow)
	if kind := next(2 * time.Second); kind != string(doingNowIdle) {
		t.Fatalf("first line = %q, want idle", kind)
	}
	// A change no event announces waits for a tick, and a locked screen skips
	// the ticks.
	app.closedEyes.enabled.Store(true)
	if kind := next(300 * time.Millisecond); kind != "" {
		t.Fatalf("a locked screen's tick refreshed the status line: %q", kind)
	}
	app.nudgeDoingNow()
	if kind := next(2 * time.Second); kind != string(doingNowPaused) {
		t.Fatalf("a nudge while locked = %q, want paused", kind)
	}
	resting.Store(false)
	app.closedEyes.enabled.Store(false)
	if kind := next(2 * time.Second); kind != string(doingNowIdle) {
		t.Fatalf("after unlock the tick = %q, want idle", kind)
	}
}

// The loop refreshes on start and on a nudge whatever the lock detector
// says on the machine running the tests.
func TestDoingNowLoopRefreshesOnANudgeWhileResting(t *testing.T) {
	previous := doingNowResting
	doingNowResting = func() bool { return true }
	t.Cleanup(func() { doingNowResting = previous })
	app := &App{activeTags: map[string]bool{}}
	next := doingNowEventKinds(t, app)
	app.startDoingNow(context.Background())
	t.Cleanup(app.stopDoingNow)
	if kind := next(2 * time.Second); kind != string(doingNowIdle) {
		t.Fatalf("first line = %q, want idle", kind)
	}
	app.closedEyes.enabled.Store(true)
	app.noteDoingNowEvent(closedEyesModeChangedEvent)
	if kind := next(2 * time.Second); kind != string(doingNowPaused) {
		t.Fatalf("after the nudge = %q, want paused", kind)
	}
}
