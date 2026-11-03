package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// mobileBriefStoredBrief builds a minimal brief for one slot on the given local date.
func mobileBriefStoredBrief(t *testing.T, localDate, slot string, hour int) MobileBriefV1 {
	t.Helper()
	asOf := mobileBriefMustParseDay(t, localDate).Add(time.Duration(hour) * time.Hour)
	brief := buildMobileBriefV1(mobileBriefInput{
		Slot:     slot,
		AsOf:     asOf,
		Location: time.Local,
		Moved: []mobileBriefCandidate{{
			SourceKind: "history", SourceID: localDate + slot, Title: "Something moved", At: asOf,
		}},
	})
	brief.NarrativeStatus = "unavailable"
	return brief
}

func mobileBriefMustParseDay(t *testing.T, value string) time.Time {
	t.Helper()
	day, err := time.ParseInLocation("2006-01-02", value, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	return day
}

func TestMobileBriefStateDirUsesEnvironmentThenHome(t *testing.T) {
	t.Setenv("YANSU_MOBILE_STATE_DIR", "  /custom/mobile-state  ")
	got, err := mobileBriefStateDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != "/custom/mobile-state" {
		t.Fatalf("state dir = %q, want the trimmed environment value", got)
	}

	t.Setenv("YANSU_MOBILE_STATE_DIR", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err = mobileBriefStateDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".yansu-agent", "mobile"); got != want {
		t.Fatalf("state dir = %q, want %q", got, want)
	}
}

func TestMobileBriefStoreSaveLoadAndList(t *testing.T) {
	store := newMobileBriefStore(t.TempDir())
	now := time.Date(2026, 10, 9, 14, 0, 0, 0, time.Local)

	morning := mobileBriefStoredBrief(t, "2026-10-09", mobileBriefSlotMorning, 7)
	evening := mobileBriefStoredBrief(t, "2026-10-08", mobileBriefSlotEvening, 18)
	onRequest := mobileBriefStoredBrief(t, "2026-10-09", mobileBriefSlotOnRequest, 12)
	for _, brief := range []MobileBriefV1{evening, morning, onRequest} {
		if err := store.save(brief, now); err != nil {
			t.Fatalf("save %s: %v", brief.BriefID, err)
		}
	}

	loaded, ok := store.load(morning.BriefID)
	if !ok {
		t.Fatalf("load %s: not found", morning.BriefID)
	}
	if loaded.BriefID != morning.BriefID || loaded.LocalDate != "2026-10-09" {
		t.Fatalf("loaded brief = %+v", loaded)
	}
	if !store.exists(morning.BriefID) || store.exists("2026-10-09-evening") {
		t.Fatal("exists did not match the stored briefs")
	}

	list := store.list(10)
	if len(list) != 3 {
		t.Fatalf("list has %d rows, want 3", len(list))
	}
	if list[0].BriefID != onRequest.BriefID {
		t.Fatalf("newest first: first row is %s, want %s", list[0].BriefID, onRequest.BriefID)
	}
	if list[2].BriefID != evening.BriefID {
		t.Fatalf("oldest last: last row is %s, want %s", list[2].BriefID, evening.BriefID)
	}
	if got := store.list(1); len(got) != 1 {
		t.Fatalf("list(1) returned %d rows", len(got))
	}

	latest, ok := store.latest()
	if !ok || latest.BriefID != onRequest.BriefID {
		t.Fatalf("latest = %s (ok=%v), want %s", latest.BriefID, ok, onRequest.BriefID)
	}
}

func TestMobileBriefStoreLatestFallsBackToNewestFile(t *testing.T) {
	dir := t.TempDir()
	store := newMobileBriefStore(dir)
	now := time.Date(2026, 10, 9, 14, 0, 0, 0, time.Local)
	brief := mobileBriefStoredBrief(t, "2026-10-09", mobileBriefSlotMorning, 7)
	if err := store.save(brief, now); err != nil {
		t.Fatal(err)
	}
	pointer := filepath.Join(dir, "briefs", mobileBriefLatestFileName)
	if err := os.WriteFile(pointer, []byte(`{"briefId":"2020-01-01-morning"}`), 0600); err != nil {
		t.Fatal(err)
	}
	latest, ok := store.latest()
	if !ok || latest.BriefID != brief.BriefID {
		t.Fatalf("stale pointer: latest = %s (ok=%v), want %s", latest.BriefID, ok, brief.BriefID)
	}
}

func TestMobileBriefStoreRejectsUnsafeIDs(t *testing.T) {
	store := newMobileBriefStore(t.TempDir())
	now := time.Date(2026, 10, 9, 14, 0, 0, 0, time.Local)
	brief := mobileBriefStoredBrief(t, "2026-10-09", mobileBriefSlotMorning, 7)
	if err := store.save(brief, now); err != nil {
		t.Fatal(err)
	}
	hostile := []string{
		"../2026-10-09-morning",
		"2026-10-09-morning/../2026-10-09-morning",
		"/etc/passwd",
		"latest",
		"2026-10-09-morning.json",
		"2026-10-09-noon",
		"",
	}
	for _, id := range hostile {
		if _, ok := store.load(id); ok {
			t.Errorf("load accepted unsafe id %q", id)
		}
		if store.exists(id) {
			t.Errorf("exists accepted unsafe id %q", id)
		}
	}
	bad := brief
	bad.BriefID = "../escape"
	if err := store.save(bad, now); err == nil {
		t.Fatal("save accepted a brief ID with path parts")
	}
}

func TestMobileBriefStoreRejectsMismatchedFile(t *testing.T) {
	dir := t.TempDir()
	store := newMobileBriefStore(dir)
	briefsDir := filepath.Join(dir, mobileBriefDirName)
	if err := os.MkdirAll(briefsDir, 0700); err != nil {
		t.Fatal(err)
	}
	// A file whose body names a different brief must not be served under the
	// requested ID.
	body := `{"version":1,"briefId":"2026-10-08-evening","slot":"evening"}`
	if err := os.WriteFile(filepath.Join(briefsDir, "2026-10-09-morning.json"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.load("2026-10-09-morning"); ok {
		t.Fatal("a file whose body names another brief was served")
	}
}

// TestMobileBriefStoreRetentionSweep saves each brief at its own time, so the
// sweep that runs on a later save decides what survives. Retention keeps the
// last 14 local dates counted back from the save time.
func TestMobileBriefStoreRetentionSweep(t *testing.T) {
	store := newMobileBriefStore(t.TempDir())

	old := mobileBriefStoredBrief(t, "2026-09-24", mobileBriefSlotMorning, 7)
	edge := mobileBriefStoredBrief(t, "2026-09-25", mobileBriefSlotEvening, 18)
	recent := mobileBriefStoredBrief(t, "2026-10-08", mobileBriefSlotMorning, 7)
	if err := store.save(old, time.Date(2026, 9, 24, 14, 0, 0, 0, time.Local)); err != nil {
		t.Fatal(err)
	}
	if err := store.save(edge, time.Date(2026, 9, 25, 19, 0, 0, 0, time.Local)); err != nil {
		t.Fatal(err)
	}
	if !store.exists(old.BriefID) || !store.exists(edge.BriefID) {
		t.Fatal("briefs inside the window were removed before the boundary moved")
	}

	if err := store.save(recent, time.Date(2026, 10, 9, 14, 0, 0, 0, time.Local)); err != nil {
		t.Fatal(err)
	}
	if store.exists(old.BriefID) {
		t.Errorf("%s is past the retention window and should be removed", old.BriefID)
	}
	if !store.exists(edge.BriefID) {
		t.Errorf("%s is on the retention boundary and should be kept", edge.BriefID)
	}
	if !store.exists(recent.BriefID) {
		t.Errorf("%s is recent and should be kept", recent.BriefID)
	}
}

func TestMobileBriefStoreFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits do not apply on Windows")
	}
	dir := t.TempDir()
	store := newMobileBriefStore(dir)
	now := time.Date(2026, 10, 9, 14, 0, 0, 0, time.Local)
	if err := store.save(mobileBriefStoredBrief(t, "2026-10-09", mobileBriefSlotMorning, 7), now); err != nil {
		t.Fatal(err)
	}
	briefsDir := filepath.Join(dir, mobileBriefDirName)
	mobileBriefAssertMode(t, briefsDir, 0700)
	mobileBriefAssertMode(t, filepath.Join(briefsDir, "2026-10-09-morning.json"), 0600)
	mobileBriefAssertMode(t, filepath.Join(briefsDir, mobileBriefLatestFileName), 0600)
}

func TestMobileBriefStoreWithoutStateDirIsUnavailable(t *testing.T) {
	store := newMobileBriefStore("")
	now := time.Date(2026, 10, 9, 14, 0, 0, 0, time.Local)
	if err := store.save(mobileBriefStoredBrief(t, "2026-10-09", mobileBriefSlotMorning, 7), now); err == nil {
		t.Fatal("save without a state directory must fail rather than write to the working directory")
	}
	if _, ok := store.latest(); ok {
		t.Fatal("latest without a state directory should be empty")
	}
	if got := store.list(5); len(got) != 0 {
		t.Fatalf("list without a state directory = %d rows", len(got))
	}
}

func mobileBriefAssertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s has mode %o, want %o", path, got, want)
	}
}
