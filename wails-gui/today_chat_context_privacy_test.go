package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/pii"
	"wails-gui/internal/platform/recentfiles"
)

func createTodayContextPurpose(t *testing.T, a *App, key, title string, evidence PurposeEvidenceInput) PurposeOpenLoopDetail {
	t.Helper()
	store, err := a.purposeProductStore()
	if err != nil {
		t.Fatal(err)
	}
	input := purposeTestCreateInput(key, PurposeKindCommitment, key, []PurposeEvidenceInput{evidence}, evidence.SourceObservedAt)
	input.Title = title
	created, _, err := store.Create(input)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := store.GetDetail(created.OpenLoop.ID)
	if err != nil {
		t.Fatal(err)
	}
	return detail
}

func TestTodayChatContextPurposeInheritedFieldsRecheckTheirSourceEvents(t *testing.T) {
	for _, changedField := range []string{"none", "title", "outcome", "counterparty"} {
		t.Run(changedField, func(t *testing.T) {
			a := newTodayContextTestApp(t, true)
			base := time.Now().Add(-time.Minute).UnixMilli()
			if err := a.activity.store.SaveSession(ActivitySessionRow{ID: "original", AppNames: `["Notes"]`, StartTime: base, EndTime: base + 1}, nil, nil); err != nil {
				t.Fatal(err)
			}
			evidence := purposeTestEvidence("original", PurposeEvidenceObservedUser, PurposeStanceSupport, base)
			evidence.SourceKind, evidence.SourceID = "activity_session", "original"
			detail := createTodayContextPurpose(t, a, "confidential outcome", "Atlas confidential title", evidence)
			store, _ := a.purposeProductStore()
			update := purposeTestAppendInput(detail, "public-update", PurposeEventRevised, PurposeStanceUpdate, PurposeEvidenceExplicitUser, "public", base+10)
			update.Evidence[0].SourceKind = "explicit_user_statement"
			switch changedField {
			case "title":
				update.Title = "Atlas public title"
			case "outcome":
				update.DesiredOutcome = "Public outcome"
			case "counterparty":
				update.Counterparty = "Public collaborator"
			}
			if _, _, err := store.AppendEvent(update); err != nil {
				t.Fatal(err)
			}
			// Another status snapshot must not sever the original field chain.
			current, err := store.GetDetail(detail.OpenLoop.ID)
			if err != nil {
				t.Fatal(err)
			}
			latest := purposeTestAppendInput(current, "latest-update", PurposeEventReinforced, PurposeStanceSupport, PurposeEvidenceExplicitUser, "latest", base+20)
			latest.Evidence[0].SourceKind = "explicit_user_statement"
			if _, _, err := store.AppendEvent(latest); err != nil {
				t.Fatal(err)
			}
			ref := TodayChatContextRef{SourceKind: "purpose", SourceID: detail.OpenLoop.ID}
			if _, err := a.buildTodayChatContext(context.Background(), ref); err != nil {
				t.Fatalf("allowed source before privacy change: %v", err)
			}
			previous := pii.GetUserSensitiveApps()
			t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
			pii.SetUserSensitiveApps(map[string]string{"com.apple.Notes": "Notes"})
			got, err := a.buildTodayChatContext(context.Background(), ref)
			if changedField == "none" {
				if !errors.Is(err, errTodayChatItemUnavailable) {
					t.Fatalf("item with every payload field excluded remained available: %v\n%s", err, got.Text)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{detail.Current.Title, detail.Current.DesiredOutcome, detail.Current.Counterparty} {
				if strings.Contains(got.Text, secret) {
					t.Errorf("inherited field survived source exclusion: %q\n%s", secret, got.Text)
				}
			}
			public := map[string]string{"title": update.Title, "outcome": update.DesiredOutcome, "counterparty": update.Counterparty}[changedField]
			if !strings.Contains(got.Text, public) {
				t.Errorf("independently supported field was dropped: %q\n%s", public, got.Text)
			}
		})
	}
}

func TestTodayChatContextPurposePartialProjectionCannotSupportAnotherPurpose(t *testing.T) {
	a := newTodayContextTestApp(t, false)
	original := PurposeOpenLoopEventRecord{ID: "original", Title: "Old title", DesiredOutcome: "Excluded outcome"}
	current := PurposeOpenLoopEventRecord{ID: "current", PredecessorEventID: original.ID, Title: "Public title", DesiredOutcome: original.DesiredOutcome}
	sources := todayChatSources{Purposes: []PurposeOpenLoopDetail{
		{OpenLoop: PurposeOpenLoopRecord{ID: "child"}, Current: current, Events: []PurposeOpenLoopEventRecord{original, current}, Evidence: []PurposeEvidenceRecord{
			{EventID: original.ID, SourceKind: "memory", SourceID: "forgotten", PrivacyDecision: TaskContextPrivacyInclude},
			{EventID: current.ID, SourceKind: "explicit_user_statement", SourceID: "current", PrivacyDecision: TaskContextPrivacyInclude},
		}},
		{OpenLoop: PurposeOpenLoopRecord{ID: "parent"}, Current: PurposeOpenLoopEventRecord{Title: "Excluded outcome"}, Evidence: []PurposeEvidenceRecord{{SourceKind: "purpose", SourceID: "child", PrivacyDecision: TaskContextPrivacyInclude}}},
	}}
	got := a.filterTodayChatPurposes(sources)
	if len(got) != 1 || got[0].OpenLoop.ID != "child" || got[0].Current.Title != "Public title" || got[0].Current.DesiredOutcome != "" {
		t.Fatalf("dependent projection bypassed field exclusion: %+v", got)
	}
}

func TestTodayChatContextPurposeMissingOrCyclicFieldProvenanceFailsClosed(t *testing.T) {
	for _, broken := range []string{"missing", "cycle"} {
		t.Run(broken, func(t *testing.T) {
			a := newTodayContextTestApp(t, false)
			current := PurposeOpenLoopEventRecord{ID: "current", PredecessorEventID: broken, Title: "Unverifiable title"}
			purpose := PurposeOpenLoopDetail{OpenLoop: PurposeOpenLoopRecord{ID: "purpose"}, Current: current, Evidence: []PurposeEvidenceRecord{{EventID: "current", SourceKind: "explicit_user_statement", PrivacyDecision: TaskContextPrivacyInclude}}}
			if broken == "cycle" {
				purpose.Events = []PurposeOpenLoopEventRecord{{ID: "cycle", PredecessorEventID: "current", Title: current.Title}, current}
			}
			got := a.filterTodayChatPurposes(todayChatSources{Purposes: []PurposeOpenLoopDetail{purpose}})
			if len(got) != 1 || got[0].Current.Title != "" {
				t.Fatalf("unverifiable field survived: %+v", got)
			}
		})
	}
}

func saveTodayContextFileBrief(t *testing.T, a *App, detail string, now time.Time) TodayChatContextRef {
	t.Helper()
	if err := a.store.SetUIState(context.Background(), permissionUIStateKey(filesPermissionKey), "true"); err != nil {
		t.Fatal(err)
	}
	row := FileActivityRow{ID: "budget-file", Name: "Atlas budget.txt", ActiveAt: now.UnixMilli(), Action: recentfiles.ActionEdited, AppBundleID: "com.example.editor", AppName: "Editor"}
	if _, err := a.activity.store.UpsertFileActivity([]FileActivityRow{row}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	brief := buildMobileBriefV1(mobileBriefInput{Slot: "morning", WindowStart: now.Add(-time.Hour), AsOf: now.Add(time.Minute), Doing: []mobileBriefCandidate{{SourceKind: mobileBriefFileSource, SourceID: "file:" + row.ID, Title: row.Name, Detail: detail, At: now}}})
	dir, err := mobileBriefStateDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := newMobileBriefStore(dir).save(brief, now); err != nil {
		t.Fatal(err)
	}
	if len(brief.Sections.Doing) != 1 {
		t.Fatalf("missing file card: %+v", brief.Sections.Doing)
	}
	return TodayChatContextRef{SourceKind: "brief_item", SourceID: brief.Sections.Doing[0].ID, BriefID: brief.BriefID}
}

func TestTodayChatContextFileFallbackOmitsExcludedCommitment(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	now := time.Now()
	base := now.Add(-time.Minute).UnixMilli()
	evidence := purposeTestEvidence("excluded", PurposeEvidenceExplicitUser, PurposeStanceSupport, base)
	evidence.SourceKind = "mail_thread"
	const title = "Atlas budget confidential commitment"
	createTodayContextPurpose(t, a, "excluded", title, evidence)
	ref := saveTodayContextFileBrief(t, a, "Edited", now)
	// Prove that the display collector would find and append the excluded title.
	in := mobileBriefInput{AsOf: now.Add(time.Second), Location: time.Local}
	collectMobileBriefDoing(a, &in)
	if len(in.Doing) != 1 || !strings.Contains(in.Doing[0].Detail, title) {
		t.Fatalf("fixture did not exercise the commitment fallback: %+v", in.Doing)
	}
	got, err := a.buildTodayChatContext(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.Text, title) || !strings.Contains(got.Text, "Atlas budget.txt") {
		t.Fatalf("file fallback bypassed Purpose privacy: %s", got.Text)
	}
}

func TestTodayChatContextFileCachedDetailRevalidatesContributingSources(t *testing.T) {
	for _, change := range []string{"forgotten-memory", "edited-memory", "revised-purpose", "excluded-source"} {
		t.Run(change, func(t *testing.T) {
			a := newTodayContextTestApp(t, true)
			now := time.Now()
			const body = "# Atlas budget\n\n- **Apps**: Notes\n\nAtlas budget planning."
			writeTodayContextMemory(t, "memo", body)
			evidence := purposeTestEvidence("memo", PurposeEvidenceExplicitUser, PurposeStanceSupport, now.Add(-time.Minute).UnixMilli())
			evidence.SourceKind, evidence.SourceID, evidence.SourceHash = "memory", "memo", agencyPayloadHash([]byte(strings.TrimSpace(body)))
			const secret = "Atlas budget confidential promise"
			detail := createTodayContextPurpose(t, a, "memo-purpose", secret, evidence)
			ref := saveTodayContextFileBrief(t, a, "Edited; for "+secret, now)
			before, err := a.buildTodayChatContext(context.Background(), ref)
			if err != nil || !strings.Contains(before.Text, secret) {
				t.Fatalf("fixture missing the original commitment: %v\n%s", err, before.Text)
			}
			switch change {
			case "forgotten-memory":
				dir, _ := memoryDir()
				if err := recordMemoryTombstone(dir, "memo", []string{body}, time.Now()); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(filepath.Join(dir, "memo.md")); err != nil {
					t.Fatal(err)
				}
			case "edited-memory":
				writeTodayContextMemory(t, "memo", "# Atlas budget\n\n- **Source**: user_edit\n\nAtlas budget corrected plan.")
			case "revised-purpose":
				store, _ := a.purposeProductStore()
				update := purposeTestAppendInput(detail, "public-plan", PurposeEventRevised, PurposeStanceUpdate, PurposeEvidenceExplicitUser, "public-plan", now.Add(-time.Second).UnixMilli())
				update.Evidence[0].SourceKind = "explicit_user_statement"
				update.Title = "Atlas budget public plan"
				if _, _, err := store.AppendEvent(update); err != nil {
					t.Fatal(err)
				}
			case "excluded-source":
				previous := pii.GetUserSensitiveApps()
				t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
				pii.SetUserSensitiveApps(map[string]string{"com.apple.Notes": "Notes"})
			}
			got, err := a.buildTodayChatContext(context.Background(), ref)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(got.Text, secret) || !strings.Contains(got.Text, "Atlas budget.txt") {
				t.Fatalf("cached file detail revived superseded context: %s", got.Text)
			}
			if change == "revised-purpose" && !strings.Contains(got.Text, "Atlas budget public plan") {
				t.Fatalf("current Purpose was not rebuilt: %s", got.Text)
			}
		})
	}
}
