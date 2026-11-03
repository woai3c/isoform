package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"wails-gui/internal/handoff"
)

func agencyHandoffViewByID(
	t *testing.T,
	items []HandoffEntryView,
	id string,
) HandoffEntryView {
	t.Helper()
	for _, item := range items {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("Handoff %q missing from views: %+v", id, items)
	return HandoffEntryView{}
}

func TestAgencyHandoffProductViewVerifiesPlanWithoutEffects(t *testing.T) {
	rig := newAgencyHandoffTestRig(t)
	path := filepath.Join(rig.app.handoffStore.Dir(), rig.entry.ID+".json")
	beforeFile, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	protectedBefore := map[string]int{}
	for _, table := range []string{
		"agency_origination_permission_requests_v1",
		"agency_permission_decision_receipts_v1",
	} {
		protectedBefore[table] = handoffTestTableCount(
			t,
			rig.activity.db,
			table,
		)
	}

	items, err := rig.app.ListHandoffs()
	if err != nil {
		t.Fatal(err)
	}
	listed := agencyHandoffViewByID(t, items, rig.entry.ID)
	detail, err := handoffViewForTest(rig.app, rig.entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	chips, err := rig.app.GetActiveHandoffChips(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(chips) != 1 {
		t.Fatalf("active Handoff chips=%+v", chips)
	}

	for _, view := range []HandoffEntryView{listed, detail, chips[0]} {
		if view.Body != "" || view.Plan == nil {
			t.Fatalf("Agency product view leaked body or lost plan: %+v", view)
		}
		plan := view.Plan
		if plan.Purpose != rig.plan.Purpose ||
			plan.DesiredResult != rig.plan.DesiredOutcome ||
			plan.ScopeSummary != rig.plan.Summary ||
			!slices.Equal(plan.Includes, rig.plan.Includes) ||
			!slices.Equal(plan.Excludes, rig.plan.Excludes) ||
			plan.WhyNow != rig.plan.Rationale {
			t.Fatalf("Agency product plan=%+v", plan)
		}
		raw, marshalErr := json.Marshal(view)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		for _, forbidden := range []string{
			"Complete this bounded Yansu-originated work",
			"Required evidence references",
			"Do not infer additional permission",
			"current-work:1",
		} {
			if strings.Contains(string(raw), forbidden) {
				t.Fatalf("product view leaked %q: %s", forbidden, raw)
			}
		}
	}

	rawEntry, err := rig.app.handoffStore.Get(rig.entry.ID)
	if err != nil || !strings.Contains(
		rawEntry.Body,
		"Complete this bounded Yansu-originated work",
	) || !strings.Contains(rawEntry.Body, "current-work:1") {
		t.Fatalf("persisted execution record changed: %+v err=%v", rawEntry, err)
	}
	afterFile, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeFile, afterFile) {
		t.Fatal("product reads changed the persisted Handoff")
	}
	for table, before := range protectedBefore {
		if after := handoffTestTableCount(
			t,
			rig.activity.db,
			table,
		); after != before {
			t.Fatalf("product reads changed %s from %d to %d", table, before, after)
		}
	}
}

func TestAgencyHandoffProductViewKeepsHistoricalPlanReadable(t *testing.T) {
	rig := newAgencyHandoffTestRig(t)
	if _, err := rig.app.handoffStore.UpdateStatus(
		rig.entry.ID,
		handoff.StatusStale,
	); err != nil {
		t.Fatal(err)
	}

	view, err := handoffViewForTest(rig.app, rig.entry.ID)
	if err != nil || view.Status != handoff.StatusStale || view.Plan == nil ||
		view.Body != "" || view.Plan.DesiredResult != rig.plan.DesiredOutcome {
		t.Fatalf("historical product view=%+v err=%v", view, err)
	}
}

func TestAgencyHandoffProductViewFailsClosed(t *testing.T) {
	rig := newAgencyHandoffTestRig(t)
	entry, err := rig.app.handoffStore.Get(rig.entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	entry.SourceID = "different-source-run"
	if _, err := rig.app.handoffStore.Save(entry); err != nil {
		t.Fatal(err)
	}

	view, err := handoffViewForTest(rig.app, rig.entry.ID)
	if err != nil || view.Plan != nil || view.Body != "" {
		t.Fatalf("mismatched source did not fail closed: %+v err=%v", view, err)
	}
	withoutAgencyStore := &App{handoffStore: rig.app.handoffStore}
	view, err = handoffViewForTest(withoutAgencyStore, rig.entry.ID)
	if err != nil || view.Plan != nil || view.Body != "" {
		t.Fatalf("unavailable source did not fail closed: %+v err=%v", view, err)
	}
}

func TestLegacyHandoffProductViewPreservesAuthoredBody(t *testing.T) {
	rig := newAgencyHandoffTestRig(t)
	legacy := handoff.Entry{
		ID:        "legacy-product-view",
		Title:     "Continue a legacy draft",
		Status:    handoff.StatusActive,
		Source:    handoff.SourceFallback,
		CreatedAt: rig.entry.CreatedAt + 1,
		UpdatedAt: rig.entry.CreatedAt + 1,
		Body:      "# Existing handoff\n\nKeep this authored Markdown.",
	}
	if _, err := rig.app.handoffStore.Save(legacy); err != nil {
		t.Fatal(err)
	}
	persisted, err := rig.app.handoffStore.Get(legacy.ID)
	if err != nil {
		t.Fatal(err)
	}

	view, err := handoffViewForTest(rig.app, legacy.ID)
	if err != nil || view.Plan != nil || view.Body != persisted.Body {
		t.Fatalf("legacy product view=%+v err=%v", view, err)
	}
}

// handoffViewForTest reads one handoff the way the Handoffs page shows it.
func handoffViewForTest(app *App, id string) (HandoffEntryView, error) {
	entry, err := app.handoffStore.Get(id)
	if err != nil {
		return HandoffEntryView{}, err
	}
	return app.productHandoffEntryView(entry), nil
}
