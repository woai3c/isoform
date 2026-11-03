package main

import (
	"fmt"
	"strings"
	"testing"

	"wails-gui/internal/pii"
	"wails-gui/internal/sop"
)

func TestMemoryAndSOPReadsSurviveRawRetention(t *testing.T) {
	app := newSOPSourcePrivacyApp(t)
	store := app.activity.store
	content := "# Durable memory\n\n- **Activity Session**: later-private-source\n\n- Remember renewal work.\n"
	entry := saveSOPPrivacyEntry(t, app, "retained-playbook", sop.SourceTriage, "later-private-source")
	check := func(want bool) {
		t.Helper()
		if got := app.memorySourceAllowsRead(content); got != want {
			t.Fatalf("memory readable=%v, want %v", got, want)
		}
		if got := app.sopEntryAllowsModelRead(entry); got != want {
			t.Fatalf("SOP readable=%v, want %v", got, want)
		}
		if got := strings.Contains(app.buildSOPDedupSection(), entry.ID); got != want {
			t.Fatalf("SOP dedup present=%v, want %v", got, want)
		}
		if got := app.sopSeedSourcesAllowRead(sopSeedSources{Sessions: entry.SourceIDs}); got != want {
			t.Fatalf("SOP update seed readable=%v, want %v", got, want)
		}
	}
	check(true)
	if err := store.deleteRetainedSession("later-private-source"); err != nil {
		t.Fatal(err)
	}
	check(true)
	pii.AddUserSensitiveApp("com.example.diary", "Diary")
	check(false)
	pii.SetUserSensitiveApps(nil)
	check(true)
	if err := store.DeleteSession("later-private-source"); err != nil {
		t.Fatal(err)
	}
	check(false)
}

func TestRetainedMemoryAndSOPKeepPrivateAndPauseRestrictions(t *testing.T) {
	for _, reason := range []string{"private", "pause", "protected", "storage-error"} {
		t.Run(reason, func(t *testing.T) {
			app := newSOPSourcePrivacyApp(t)
			store := app.activity.store
			id := "safe-source"
			switch reason {
			case "private":
				id = "private-window-source"
			case "protected":
				if _, err := store.db.Exec(`UPDATE activity_sessions SET triage_status='protected' WHERE id=?`, id); err != nil {
					t.Fatal(err)
				}
			}
			entry := saveSOPPrivacyEntry(t, app, "retained-private", sop.SourceTriage, id)
			if err := store.deleteRetainedSession(id); err != nil {
				t.Fatal(err)
			}
			switch reason {
			case "pause":
				app.saveObservationPauses([]observationPause{{Start: 1000, End: 2000}})
			case "storage-error":
				if _, err := store.db.Exec(`DROP TABLE activity_retained_source_privacy`); err != nil {
					t.Fatal(err)
				}
			}
			content := fmt.Sprintf("# Retained note\n\n- **Apps**: Notes\n- **Activity Session**: %s\n", id)
			if app.memorySourceAllowsRead(content) || app.sopEntryAllowsModelRead(entry) {
				t.Fatal("retention removed a source restriction")
			}
		})
	}
}

func TestLegacyMissingMemoryAndSOPUseStoredApps(t *testing.T) {
	app := newSOPSourcePrivacyApp(t)
	content := "# Older retained memory\n\n- **Apps**: Diary\n- **Activity Session**: old-purged-session\n"
	entry := sop.Entry{Source: sop.SourceTriage, SourceIDs: []string{"old-purged-session"}, ReadProvenance: sopReadProvenanceJSON(sopReadProvenance{Version: 1, Apps: []string{"Diary"}})}
	if !app.memorySourceAllowsRead(content) || !app.sopEntryAllowsModelRead(entry) {
		t.Fatal("legacy retained derivative with source apps disappeared")
	}
	pii.AddUserSensitiveApp("com.example.diary", "Diary")
	if app.memorySourceAllowsRead(content) || app.sopEntryAllowsModelRead(entry) {
		t.Fatal("legacy retained derivative ignored current sensitive apps")
	}
}

func TestLegacyDailyCacheIgnoresCaptureProtectedGlance(t *testing.T) {
	app := newSOPSourcePrivacyApp(t)
	store := app.activity.store
	const date = "2026-04-20"
	seedActivitySummaryI18nSession(t, store, "legacy-safe", date, 9, []string{"Notes"})
	seedActivitySummaryI18nSession(t, store, "legacy-glance", date, 10, []string{"Bitwarden"})
	if _, err := store.db.Exec(`UPDATE activity_sessions SET triage_status='protected' WHERE id='legacy-glance'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveSummary(date, "daily", "safe cached narrative", "test", false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE activity_summaries SET source_provenance='' WHERE date=?`, date); err != nil {
		t.Fatal(err)
	}
	data, err := app.GetDailySummary(date)
	if err != nil || data.AISummary != "safe cached narrative" || !app.activityDayAllowsModelRead(date) {
		t.Fatalf("capture-protected glance vetoed legacy cache: data=%+v err=%v", data, err)
	}
	pii.AddUserSensitiveApp("com.apple.Notes", "Notes")
	if app.activityDayAllowsModelRead(date) {
		t.Fatal("legacy cache ignored later-sensitive source")
	}
}

func TestDigestRecallAcceptsRetainedMemoryAndHonorsLaterDeletion(t *testing.T) {
	app := newSOPSourcePrivacyApp(t)
	store := app.activity.store
	const date = "2026-04-20"
	seedActivitySummaryI18nSession(t, store, "digest-source", date, 9, []string{"Notes"})
	if _, err := store.SaveSummary(date, "daily", "retained narrative", "test", false); err != nil {
		t.Fatal(err)
	}
	if err := store.deleteRetainedSession("digest-source"); err != nil {
		t.Fatal(err)
	}
	content := "# Retained memory\n\n- **Activity Session**: digest-source\n"
	sources, err := app.summaryMemorySources(content, 1000, 2000)
	if err != nil || len(sources) < 2 || !app.summarySourcesAllowRead(sources) {
		t.Fatalf("retained memory blocked summary generation: sources=%+v err=%v", sources, err)
	}
	if !app.activityDayAllowsModelRead(date) {
		t.Fatal("retention lost cached digest")
	}
	if err := store.DeleteSession("digest-source"); err != nil {
		t.Fatal(err)
	}
	if app.activityDayAllowsModelRead(date) || app.summarySourcesAllowRead(sources) {
		t.Fatal("explicit deletion did not override retained proof")
	}
}
