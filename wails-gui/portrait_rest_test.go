package main

import (
	"slices"
	"testing"
	"time"
)

// CPU round 5: the portrait's check and read loaded and parsed the forget
// markers (.forgotten.json) once per note. They are loaded once per memory
// snapshot, and a forget still applies on the next read.
func TestPortraitReadsTheForgetMarkersOncePerMemoryChange(t *testing.T) {
	rig, dir := newMemoryPrivacyQueryRig(t)
	app := rig.app
	if err := recordMemoryTombstone(dir, "harbor-note-0005", []string{"never stored"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	ids := func() []string {
		entries, err := app.portraitService().readableEntries(false)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]string, 0, len(entries))
		for _, entry := range entries {
			out = append(out, entry.ID)
		}
		return out
	}
	if got := ids(); len(got) != privacyQueryNotes-1 || slices.Contains(got, "harbor-note-0005") {
		t.Fatalf("fixture: %d readable notes", len(got))
	}
	before := memoryTombstoneLoads.Load()
	const repeats = 3
	started := time.Now()
	for i := 0; i < repeats; i++ {
		ids()
	}
	elapsed := time.Since(started) / repeats
	loads := (memoryTombstoneLoads.Load() - before) / repeats
	t.Logf("portrait source read at %d notes, nothing changed: %d forget-marker loads, %s per read", privacyQueryNotes, loads, elapsed.Round(time.Microsecond))
	if loads != 0 {
		t.Fatalf("%d forget-marker loads per read with nothing changed, want 0", loads)
	}

	body, err := app.GetMemoryEntry("harbor-note-0006")
	if err != nil {
		t.Fatal(err)
	}
	if err := recordMemoryTombstone(dir, "harbor-note-0006", []string{body.Content}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := ids(); slices.Contains(got, "harbor-note-0006") {
		t.Fatal("a note forgotten since the last read was still read")
	}
}

// GetAboutYouPortrait is read by every chat-waiting pass (first read
// projection) and by the portrait's own check. With nothing changed a read
// returns the last result without reading the notes again.
func TestPortraitReadRestsWhenNothingChanged(t *testing.T) {
	rig, dir := newMemoryPrivacyQueryRig(t)
	app := rig.app
	// The first read of a cold snapshot starts its load in the background;
	// settle it so the counts below only see the reads this test makes.
	waitPortraitSnapshot(t, app)
	before := portraitSourceReads.Load()
	for i := 0; i < 3; i++ {
		if _, err := app.GetAboutYouPortrait(); err != nil {
			t.Fatal(err)
		}
	}
	reads := portraitSourceReads.Load() - before
	t.Logf("3 portrait reads with nothing changed: %d source reads", reads)
	if reads != 0 {
		t.Fatalf("%d source reads with nothing changed, want 0", reads)
	}
	if err := recordMemoryTombstone(dir, "harbor-note-0007", []string{"never stored"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	before = portraitSourceReads.Load()
	if _, err := app.GetAboutYouPortrait(); err != nil {
		t.Fatal(err)
	}
	if portraitSourceReads.Load()-before != 1 {
		t.Fatal("a forget did not make the next portrait read check the notes again")
	}
	if err := app.SetClosedEyesModeEnabled(true); err != nil {
		t.Fatal(err)
	}
	before = portraitSourceReads.Load()
	if _, err := app.GetAboutYouPortrait(); err != nil {
		t.Fatal(err)
	}
	// The withdrawal dropped the value, so the read starts a background load.
	waitPortraitSnapshot(t, app)
	if portraitSourceReads.Load()-before != 1 {
		t.Fatal("Closed Eyes did not make the next portrait read check the notes again")
	}
}

// The menu bar's status tick stamps the saved brief's sources
// (finishedReadKey) only after something changed.
func TestMenuBarTickSkipsTheBriefStampWhenNothingChanged(t *testing.T) {
	r := newMemoryReadCountRig(t)
	app := r.app
	if _, err := app.menuBarView("en", false); err != nil {
		t.Fatal(err)
	}
	before := mobileBriefFinishedReadKeys.Load()
	for i := 0; i < 3; i++ {
		view, err := app.menuBarView("en", false)
		if err != nil {
			t.Fatal(err)
		}
		if view.Brief.Kind == "none" {
			t.Fatalf("tick %d lost the brief", i)
		}
	}
	stamps := mobileBriefFinishedReadKeys.Load() - before
	t.Logf("3 menu bar ticks with nothing changed: %d brief stamps", stamps)
	if stamps != 0 {
		t.Fatalf("%d brief stamps with nothing changed, want 0", stamps)
	}
	noteMobileBriefReadInputsChanged()
	before = mobileBriefFinishedReadKeys.Load()
	if _, err := app.menuBarView("en", false); err != nil {
		t.Fatal(err)
	}
	if mobileBriefFinishedReadKeys.Load() == before {
		t.Fatal("a privacy change did not make the next tick read the brief again")
	}
	app.invalidateMemoryExternalReads()
	before = mobileBriefFinishedReadKeys.Load()
	if _, err := app.menuBarView("en", false); err != nil {
		t.Fatal(err)
	}
	if mobileBriefFinishedReadKeys.Load() == before {
		t.Fatal("a memory write did not make the next tick read the brief again")
	}
}
