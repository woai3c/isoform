package main

import (
	"errors"
	"testing"

	"wails-gui/internal/handoff"
)

func desktopPlanTestApp(t *testing.T) (*App, *handoff.Store, int64) {
	t.Helper()
	bridge, store, now := mobileTestThreadBridge(t)
	app := bridge.app
	t.Cleanup(func() {
		if app.desktopTaskBridge != nil {
			app.desktopTaskBridge.Stop()
		}
	})
	return app, store, now.UnixMilli()
}

func TestDesktopTaskWaitingToStartShowsItsPlanFilesAndProject(t *testing.T) {
	app, store, now := desktopPlanTestApp(t)
	entry := handoff.Entry{ID: "plan-task", Title: "Draft the memo", Status: handoff.StatusActive, CreatedAt: now, UpdatedAt: now,
		Workdir: "/Users/example/code/isoform-web", KeyFiles: []string{"docs/memo.md"},
		Body: "## Desired result\nA one-page memo\n## Isoform will not\nSend it\n## Access needed\nNone\n"}
	if _, err := store.Save(entry); err != nil {
		t.Fatal(err)
	}
	detail, err := app.GetDesktopTaskThread(entry.ID)
	if err != nil || detail.Plan == nil || detail.Plan.Will != "A one-page memo." || detail.Plan.Wont != "Send it." || detail.Plan.Need != "" || detail.Plan.Unavailable {
		t.Fatalf("plan = %+v, %v", detail.Plan, err)
	}
	if detail.Project != "isoform-web" || len(detail.Files) != 1 || detail.Files[0] != "docs/memo.md" || detail.Checks != nil {
		t.Fatalf("detail = %+v", detail)
	}
}

func TestDesktopTaskAgencyPlanThatCannotBeReadIsUnavailable(t *testing.T) {
	app, store, now := desktopPlanTestApp(t)
	if _, err := store.Save(handoff.Entry{ID: "agency-task", Title: "Plan", Source: handoff.SourceAgency, Status: handoff.StatusActive, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	detail, err := app.GetDesktopTaskThread("agency-task")
	if err != nil || detail.Plan == nil || !detail.Plan.Unavailable {
		t.Fatalf("plan = %+v, %v", detail.Plan, err)
	}
}

func TestDesktopTaskChecksShowOnlyWhenOneWasNotMet(t *testing.T) {
	app, store, now := desktopPlanTestApp(t)
	result := &handoff.Result{Status: handoff.StatusCompleted, Criteria: []handoff.ResultCriterion{{Text: "One page", Met: true}, {Text: "Numbers match", Met: false}}}
	if _, err := store.Save(handoff.Entry{ID: "unmet", Title: "Memo", Status: handoff.StatusNeedsReview, CreatedAt: now, UpdatedAt: now, Result: result}); err != nil {
		t.Fatal(err)
	}
	detail, err := app.GetDesktopTaskThread("unmet")
	if err != nil || detail.Checks == nil || detail.Checks.Met != 1 || detail.Checks.Total != 2 || !detail.Checks.Unmet[1] || detail.Checks.Unmet[0] || detail.Plan != nil {
		t.Fatalf("checks = %+v, %v", detail.Checks, err)
	}
	allMet := &handoff.Result{Status: handoff.StatusCompleted, Criteria: []handoff.ResultCriterion{{Text: "One page", Met: true}}}
	if _, err := store.Save(handoff.Entry{ID: "met", Title: "Memo", Status: handoff.StatusCompleted, CreatedAt: now, UpdatedAt: now, Result: allMet}); err != nil {
		t.Fatal(err)
	}
	if detail, err := app.GetDesktopTaskThread("met"); err != nil || detail.Checks != nil {
		t.Fatalf("all met shows checks: %+v, %v", detail.Checks, err)
	}
	if _, err := store.Save(handoff.Entry{ID: "nothing", Title: "Memo", Status: handoff.StatusNeedsReview, CreatedAt: now, UpdatedAt: now, Verification: &handoff.Verification{Reason: "nothing verifiable"}}); err != nil {
		t.Fatal(err)
	}
	if detail, err := app.GetDesktopTaskThread("nothing"); err != nil || detail.Checks == nil || !detail.Checks.Unverifiable {
		t.Fatalf("nothing verifiable = %+v, %v", detail.Checks, err)
	}
}

func TestDesktopTaskLinkToADeletedTaskSaysItIsGone(t *testing.T) {
	app, store, now := desktopPlanTestApp(t)
	if _, err := store.Save(handoff.Entry{ID: "gone-task", Title: "Memo", Status: handoff.StatusActive, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := app.DeleteHandoff("gone-task"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.GetDesktopTaskThread("gone-task"); !errors.Is(err, errDesktopTaskGone) {
		t.Fatalf("err = %v, want task_gone", err)
	}
}
