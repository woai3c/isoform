package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/interests"
	"wails-gui/internal/platform/activitymon"
)

// P0 round 3: every memory read ran the source privacy check of each note as
// its own SQLite queries (session state, then the pause log), so a read at
// 2243 notes ran thousands of queries. A read with nothing changed must run a
// fixed number of queries, and a privacy change must apply on the next read.

const privacyQueryNotes = 2000

func newMemoryPrivacyQueryRig(t *testing.T) (*handoffTestRig, string) {
	t.Helper()
	rig := newHandoffTestRig(t)
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-48 * time.Hour)
	for i := 0; i < privacyQueryNotes; i++ {
		session := fmt.Sprintf("harbor-session-%04d", i)
		at := start.Add(time.Duration(i) * time.Minute)
		events := []activitymon.ActivityEvent{{Kind: activitymon.EventFocus, Timestamp: at.UnixMilli(), AppName: "Notes", AppBundleID: "com.apple.Notes"}}
		if err := rig.activity.SaveSession(ActivitySessionRow{ID: session, StartTime: at.UnixMilli(), EndTime: at.Add(30 * time.Second).UnixMilli(), AppNames: `["Notes"]`, TriageStatus: "done"}, events, nil); err != nil {
			t.Fatal(err)
		}
		body := fmt.Sprintf("---\nokf_version: \"0.1\"\ntype: Isoform Memory\ntitle: Harbor note %d\nsource: activity\napps:\n  - Notes\nactivity_sessions:\n  - %s\n---\n\nRowan Vale planned the Harbor Lane walk for week %d.\n", i, session, i%52)
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("harbor-note-%04d.md", i)), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	settleMemoryFolder(t, dir)
	return rig, dir
}

func externalMemoryIDs(t *testing.T, app *App) []string {
	t.Helper()
	entries, err := app.memoryEntriesForExternalRead()
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.ID)
	}
	return ids
}

func TestMemoryPrivacyChecksRunFixedQueriesPerRead(t *testing.T) {
	rig, _ := newMemoryPrivacyQueryRig(t)
	app := rig.app
	if got := len(externalMemoryIDs(t, app)); got != privacyQueryNotes {
		t.Fatalf("fixture: %d readable notes, want %d", got, privacyQueryNotes)
	}
	probe := instrumentPeopleProfileQueryCounts(t, app)
	queries := func() int {
		counts := probe.queryCountsSnapshot()
		probe.reset()
		return counts["state"] + counts["activity"]
	}
	ctx := context.Background()
	reads := []struct {
		name string
		read func()
	}{
		{"memoryEntriesForExternalRead", func() { externalMemoryIDs(t, app) }},
		{"mobileBriefExclusions", func() { app.mobileBriefExclusions() }},
		{"collectTodayChatSources", func() { app.collectTodayChatSources(ctx, time.Now()) }},
	}
	for _, r := range reads {
		r.read()
		queries()
		const repeats = 3
		for i := 0; i < repeats; i++ {
			r.read()
		}
		perRead := queries() / repeats
		t.Logf("%s at %d notes, nothing changed: %d SQLite queries per read", r.name, privacyQueryNotes, perRead)
		if perRead > 40 {
			t.Errorf("%s ran %d queries per read at %d notes; want a fixed number", r.name, perRead, privacyQueryNotes)
		}
	}
}

func TestMemoryPrivacyChangesApplyOnTheNextRead(t *testing.T) {
	rig, _ := newMemoryPrivacyQueryRig(t)
	app := rig.app
	_ = externalMemoryIDs(t, app)

	if err := app.SetClosedEyesModeEnabled(true); err != nil {
		t.Fatal(err)
	}
	if got := externalMemoryIDs(t, app); len(got) != 0 {
		t.Fatalf("Closed Eyes on: %d notes read", len(got))
	}
	if err := app.SetClosedEyesModeEnabled(false); err != nil {
		t.Fatal(err)
	}
	if got := externalMemoryIDs(t, app); len(got) != privacyQueryNotes {
		t.Fatalf("Closed Eyes off: %d notes read", len(got))
	}

	if err := rig.activity.UpdateTriageStatus("harbor-session-0003", "protected"); err != nil {
		t.Fatal(err)
	}
	if got := externalMemoryIDs(t, app); slices.Contains(got, "harbor-note-0003") {
		t.Fatal("a note from a session protected since the last read was still read")
	}

	if err := app.DeleteMemoryEntry("harbor-note-0004"); err != nil {
		t.Fatal(err)
	}
	if got := externalMemoryIDs(t, app); slices.Contains(got, "harbor-note-0004") || len(got) != privacyQueryNotes-2 {
		t.Fatalf("after a forget: %d notes read, forgotten note present=%v", len(got), slices.Contains(got, "harbor-note-0004"))
	}
}

// Topic finds re-check each stored interest's references on every read; an
// interest named from a search re-derives the readable inputs, which checks
// every note again.
func TestTopicFindsReadRunsFixedPrivacyQueries(t *testing.T) {
	rig, _ := newMemoryPrivacyQueryRig(t)
	app := rig.app
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
	probe := instrumentPeopleProfileQueryCounts(t, app)
	queries := func() int {
		counts := probe.queryCountsSnapshot()
		probe.reset()
		return counts["state"] + counts["activity"]
	}
	if _, err := app.GetTopicFinds(); err != nil {
		t.Fatal(err)
	}
	queries()
	const repeats = 3
	for i := 0; i < repeats; i++ {
		app.topicsSvc().invalidate()
		if _, err := app.GetTopicFinds(); err != nil {
			t.Fatal(err)
		}
	}
	perRead := queries() / repeats
	t.Logf("GetTopicFinds at %d notes, 21 interests, nothing changed: %d SQLite queries per read", privacyQueryNotes, perRead)
	if perRead > 200 {
		t.Errorf("GetTopicFinds ran %d queries per read at %d notes; want a fixed number", perRead, privacyQueryNotes)
	}
}

// The batched gate decides every source the same way as the per-note check:
// live, protected, retained-receipt, private-receipt, missing and
// stored-apps-only sessions, sensitive apps and blocked read provenance.
func TestMemorySourceReadGateMatchesPerNoteCheck(t *testing.T) {
	rig := newHandoffTestRig(t)
	app := rig.app
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-24 * time.Hour)
	for _, id := range []string{"harbor-live", "harbor-protected"} {
		events := []activitymon.ActivityEvent{{Kind: activitymon.EventFocus, Timestamp: at.UnixMilli(), AppName: "Notes", AppBundleID: "com.apple.Notes"}}
		if err := rig.activity.SaveSession(ActivitySessionRow{ID: id, StartTime: at.UnixMilli(), EndTime: at.Add(time.Minute).UnixMilli(), AppNames: `["Notes"]`, TriageStatus: "done"}, events, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := rig.activity.UpdateTriageStatus("harbor-protected", "protected"); err != nil {
		t.Fatal(err)
	}
	tx, err := rig.activity.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []activitySourceProvenance{
		{SessionID: "harbor-receipt", StartTime: at.UnixMilli(), EndTime: at.Add(time.Minute).UnixMilli(), Apps: []string{"Notes"}, Retained: true},
		{SessionID: "harbor-private-receipt", StartTime: at.UnixMilli(), EndTime: at.Add(time.Minute).UnixMilli(), Apps: []string{"Notes"}, Private: true, Retained: true},
	} {
		if err := saveActivitySourcePrivacyReceipt(tx, source); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	note := func(apps, sessions string) string {
		return "---\nokf_version: \"0.1\"\ntype: Isoform Memory\ntitle: Harbor note\nsource: activity\n" + apps + sessions + "---\n\nRowan Vale planned the Harbor Lane walk.\n"
	}
	notes := map[string]string{
		"live":            note("apps:\n  - Notes\n", "activity_sessions:\n  - harbor-live\n"),
		"protected":       note("apps:\n  - Notes\n", "activity_sessions:\n  - harbor-protected\n"),
		"receipt":         note("apps:\n  - Notes\n", "activity_sessions:\n  - harbor-receipt\n"),
		"private-receipt": note("apps:\n  - Notes\n", "activity_sessions:\n  - harbor-private-receipt\n"),
		"stored-apps":     note("apps:\n  - Notes\n", "activity_sessions:\n  - harbor-gone\n"),
		"no-apps":         note("", "activity_sessions:\n  - harbor-gone\n"),
		"two-sessions":    note("apps:\n  - Notes\n", "activity_sessions:\n  - harbor-live\n  - harbor-protected\n"),
		"sensitive-app":   note("apps:\n  - 1Password\n", ""),
		"no-sessions":     note("apps:\n  - Notes\n", ""),
		"blocked":         note("apps:\n  - Notes\n", "read_provenance: '{\"version\":1,\"readBlocked\":true}'\n"),
	}
	for id, body := range notes {
		if err := os.WriteFile(filepath.Join(dir, "gate-"+id+".md"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	gate := app.memorySourceReadGateContext(context.Background())
	allowed := 0
	for id, body := range notes {
		want := app.memorySourceAllowsRead(body)
		if got := gate.allows(body); got != want {
			t.Errorf("%s: batched gate says %v, per-note check says %v", id, got, want)
		}
		if want {
			allowed++
		}
	}
	if !app.memorySourceAllowsRead(notes["live"]) || app.memorySourceAllowsRead(notes["protected"]) || allowed == 0 || allowed == len(notes) {
		t.Fatalf("fixture does not cover both outcomes: %d of %d allowed", allowed, len(notes))
	}
}
