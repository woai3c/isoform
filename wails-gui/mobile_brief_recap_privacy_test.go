package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/pii"
	"wails-gui/internal/platform/activitymon"
)

// addMobileBriefRecapProvenance gives a summary the same run/session links
// that the composer records, so collection tests exercise the read policy.
func addMobileBriefRecapProvenance(t *testing.T, rig *handoffTestRig, row ActivityRow) ActivityRow {
	t.Helper()
	apps, _ := topicAppTime(row.ActiveMsByApp)
	if len(apps) == 0 {
		apps = []string{"Code"}
	}
	appNames, err := json.Marshal(apps)
	if err != nil {
		t.Fatal(err)
	}
	session := ActivitySessionRow{
		ID: "recap-source-" + row.ID, StartTime: row.StartMs, EndTime: row.EndMs,
		AppNames: string(appNames), TriageStatus: "done",
	}
	if err := rig.activity.SaveSession(session, []activitymon.ActivityEvent{{
		Kind: activitymon.EventFocus, Timestamp: row.StartMs, AppName: apps[0], WindowTitle: row.Title,
	}}, nil); err != nil {
		t.Fatal(err)
	}
	run := testRun("recap-run-"+row.ID, apps[0], row.StartMs, row.EndMs, row.EndMs-row.StartMs, 1)
	run.SessionIDs = []string{session.ID}
	insertComposableTimelineRun(t, rig.activity, run, ActivityTimelineGroupSummary{Title: row.Title})
	runIDs, err := json.Marshal([]string{run.ID})
	if err != nil {
		t.Fatal(err)
	}
	row.RunIDs = string(runIDs)
	return row
}

func TestMobileBriefEveningRecapRechecksCurrentPrivacy(t *testing.T) {
	for _, state := range []string{"readable", "closed-eyes", "observing-paused", "sensitive", "protected", "missing-session", "private-session"} {
		t.Run(state, func(t *testing.T) {
			isolateActivityPrivacyTest(t)
			rig := newHandoffTestRig(t)
			now := mobileBriefTestDay(18, 0)
			const title = "Reviewed the confidential launch plan"
			row := ActivityRow{
				ID: "evening-recap", Title: title, Status: "done", ActiveMsByApp: `{"Code":1800000}`,
				StartMs: now.Add(-8 * time.Hour).UnixMilli(), EndMs: now.Add(-7*time.Hour - 30*time.Minute).UnixMilli(),
			}
			row.CreatedAt, row.UpdatedAt = row.StartMs, row.EndMs
			row = addMobileBriefRecapProvenance(t, rig, row)
			if err := rig.activity.InsertActivity(row); err != nil {
				t.Fatal(err)
			}
			// Privacy changes after the observed work ended. The summary still
			// has an ordinary app label and must not bypass its backing session.
			switch state {
			case "closed-eyes":
				rig.app.closedEyes.enabled.Store(true)
				rig.app.saveObservationPauses([]observationPause{{Start: now.Add(-7 * time.Hour).UnixMilli()}})
			case "observing-paused":
				rig.app.saveObservationPauses([]observationPause{{Start: now.Add(-7 * time.Hour).UnixMilli()}})
			case "sensitive":
				pii.AddUserSensitiveApp("com.example.recap-private", "Recap Private")
				if _, err := rig.activity.db.Exec(`UPDATE activity_sessions SET app_names = '["Code","Recap Private"]' WHERE id = ?`, "recap-source-"+row.ID); err != nil {
					t.Fatal(err)
				}
			case "protected":
				if err := rig.activity.UpdateTriageStatus("recap-source-"+row.ID, "protected"); err != nil {
					t.Fatal(err)
				}
			case "missing-session":
				if _, err := rig.activity.db.Exec(`DELETE FROM activity_sessions WHERE id = ?`, "recap-source-"+row.ID); err != nil {
					t.Fatal(err)
				}
			case "private-session":
				if _, err := rig.activity.db.Exec(`UPDATE activity_events SET data = '{"kind":"focus","appName":"Code","privateWindow":true}' WHERE session_id = ?`, "recap-source-"+row.ID); err != nil {
					t.Fatal(err)
				}
			}
			in := mobileBriefInput{
				Slot: mobileBriefSlotEvening, WindowStart: now.Add(-12 * time.Hour), AsOf: now, Location: time.UTC,
			}
			collectMobileBriefRecap(rig.app, &in)
			brief := buildMobileBriefV1(in)
			store := newMobileBriefStore(t.TempDir())
			if err := store.save(brief, now); err != nil {
				t.Fatal(err)
			}
			stored, err := os.ReadFile(filepath.Join(store.dir, brief.BriefID+".json"))
			if err != nil {
				t.Fatal(err)
			}
			prompt := mobileBriefTestPrompt(brief, "English")
			wantReadable := state == "readable"
			if got := len(in.Moved) == 1; got != wantReadable {
				t.Errorf("collected recap = %+v, readable = %v", in.Moved, wantReadable)
			}
			// An observed recap is activity only (mobile_brief_anchor.go): it
			// stays under What you worked on and never reaches the brief text.
			if len(brief.Sections.Highlights) != 0 || strings.Contains(prompt, title) {
				t.Errorf("a recap reached the brief text: %+v", brief.Sections.Highlights)
			}
			if got := strings.Contains(string(stored), title); got != wantReadable {
				t.Errorf("stored document contains recap = %v, readable = %v", got, wantReadable)
			}
		})
	}
}
