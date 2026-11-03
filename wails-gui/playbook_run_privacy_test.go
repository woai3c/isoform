package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wails-gui/internal/pii"
	"wails-gui/internal/sop"
)

func TestPlaybookRunRechecksGeneratedSourcePrivacy(t *testing.T) {
	app := newSOPSourcePrivacyApp(t)
	plans, err := sop.NewPlanStore(filepath.Join(os.Getenv("HOME"), "sops"))
	if err != nil {
		t.Fatal(err)
	}
	app.playbookPlans = plans
	entry := saveSOPPrivacyEntry(t, app, "generated-playbook", sop.SourceManual, "later-private-source")
	entry, err = app.sopStore.Get(entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plans.Update(entry.ID, func(plan *sop.Plan) {
		plan.Schedule = &sop.Schedule{JobID: "approved-job", Enabled: true}
		plan.BodyHash = sop.BodyHash(entry)
	}); err != nil {
		t.Fatal(err)
	}
	for _, job := range []string{"approved-job", "unapproved-job"} {
		prompt, grants, err := app.preparePlaybookRun(job, entry.ID)
		if err != nil || !strings.Contains(prompt, entry.Body) || (job == "approved-job") != (len(grants) > 0) {
			t.Fatalf("readable run %s: prompt=%q grants=%d err=%v", job, prompt, len(grants), err)
		}
	}
	pii.SetUserSensitiveApps(map[string]string{"com.example.diary": "Diary"})
	for _, job := range []string{"approved-job", "unapproved-job"} {
		prompt, grants, err := app.preparePlaybookRun(job, entry.ID)
		if err == nil || prompt != "" || len(grants) != 0 {
			t.Fatalf("protected run %s: prompt=%q grants=%d err=%v", job, prompt, len(grants), err)
		}
	}
	pii.SetUserSensitiveApps(nil)
	entry.SourceIDs = nil
	entry.ReadProvenance = sopReadProvenanceJSON(sopReadProvenance{Version: 1, ReadBlocked: true})
	if _, err := app.sopStore.Save(entry); err != nil {
		t.Fatal(err)
	}
	if prompt, grants, err := app.preparePlaybookRun("unapproved-job", entry.ID); err == nil || prompt != "" || len(grants) != 0 {
		t.Fatalf("blocked generated proof: prompt=%q grants=%d err=%v", prompt, len(grants), err)
	}
}
