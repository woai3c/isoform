package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"wails-gui/storage"
)

func TestSavedThreadWorkNameUsesCompleteSourceClause(t *testing.T) {
	tests := []struct{ source, body, want string }{
		{"Review agent's rebuild on 8528cc4ba and film-en videos in Claude", "The rebuild is still running.", "Review agent's rebuild on 8528cc4ba"},
		{"Review the launch plan.", "The plan is ready.", "Review the launch plan"},
		{"Review the launch plan", "Review the launch plan", ""},
		{"A long work title without any natural break that goes far beyond the permitted limit for the card", "Other text", ""},
		{"One line\nAnother line", "Other text", ""},
		{"The rebuild is ready. Review it", "Other text", ""},
		{"Review work on", "Other text", ""},
		{"Review research and development milestones for the new project through the end of this year", "Other text", ""},
	}
	for _, tc := range tests {
		if got := savedThreadWorkName(tc.source, tc.body); got != tc.want {
			t.Errorf("%q = %q, want %q", tc.source, got, tc.want)
		}
	}
}

func TestRewriteSavedWorkNamesNestedAndIdempotent(t *testing.T) {
	input := `{"kind":"digest","unknown":{"keep":true},"items":["legacy",{"momentId":"a","kind":"decision_due","headline":"Old title.","body":"The update is ready.","target":{"kind":"thread","id":"allowed"},"activityOutcomeSource":"proof","localized":{"en":{"headline":"Old title.","body":"The update is ready.","extra":"kept"}}},{"kind":"decision_due","headline":"Leave this unchanged.","body":"Body","target":{"kind":"thread","id":"unsafe"},"activityOutcomeSource":"proof"}]}`
	resolve := func(target, source, body, headline string) string {
		if target == "allowed" && source == "proof" {
			return "Review the launch plan"
		}
		return ""
	}
	updated, changed := rewriteSavedWorkNames(input, resolve)
	if !changed || !strings.Contains(updated, `"legacy"`) || !strings.Contains(updated, `"savedWorkNameV1":"Review the launch plan"`) || !strings.Contains(updated, `"headline":"Leave this unchanged."`) || !strings.Contains(updated, `"extra":"kept"`) || !strings.Contains(updated, `"keep":true`) {
		t.Fatalf("updated = %s", updated)
	}
	if again, changed := rewriteSavedWorkNames(updated, resolve); changed || again != updated {
		t.Fatalf("not idempotent: %s", again)
	}
	protected := strings.Replace(input, `"kind":"digest"`, `"kind":"digest","readReceipt":"proof"`, 1)
	if got, changed := rewriteSavedWorkNames(protected, resolve); !changed || !strings.Contains(got, `"readReceipt":"proof"`) {
		t.Fatal("nested name not saved alongside the receipt")
	}
}

func TestRepairSavedChatWorkNamesPersistsNestedOnce(t *testing.T) {
	f := newMainChatReadMemoFixture(t)
	fixture := threadOutcomeFixtures(t)[0]
	row := seedThreadFixture(t, f.app, "saved-title", fixture, time.Now())
	if _, err := f.app.activity.store.db.Exec(`UPDATE activities SET title = ? WHERE id = ?`, "Review the launch plan", row.ID); err != nil {
		t.Fatal(err)
	}
	row.Title = "Review the launch plan"
	row = saveFixtureOutcome(t, f.app, row, fixture)
	readable := f.app.readableThreadOutcome(t.Context(), row)
	if readable == nil {
		t.Fatal("fixture outcome must be readable")
	}
	source := threadOutcomeSourceID(row.ID, readable)
	if !f.app.threadOutcomeSourceReadable(t.Context(), source) {
		t.Fatal("fixture source must be readable")
	}
	child := map[string]any{"momentId": "child", "kind": "decision_due", "headline": "Old title.", "body": "The update is ready.", "target": map[string]string{"kind": "thread", "id": row.ID}, "activityOutcomeSource": source, "unknown": "preserved"}
	payload, err := json.Marshal(map[string]any{"momentId": "root", "kind": "digest", "items": []any{child}, "readReceipt": "receipt-preserved", "other": "preserved"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.app.store.AppendStreamItem(t.Context(), f.mainID, storage.StreamItemRow{ID: "saved-nested-title", Kind: "system", ToolName: mainChatProactiveTool, ToolInput: string(payload), Content: "unchanged"}); err != nil {
		t.Fatal(err)
	}
	if err := f.app.repairSavedChatWorkNames(t.Context()); err != nil {
		t.Fatal(err)
	}
	stored, err := f.app.store.LoadStreamItem(t.Context(), "saved-nested-title")
	if err != nil || stored == nil {
		t.Fatal(err)
	}
	if !strings.Contains(stored.ToolInput, `"savedWorkNameV1":"Review the launch plan"`) || !strings.Contains(stored.ToolInput, `"unknown":"preserved"`) || stored.Content != "unchanged" {
		t.Fatalf("stored = %+v", stored)
	}
	var original, repaired mainChatProactive
	if json.Unmarshal(payload, &original) != nil || json.Unmarshal([]byte(stored.ToolInput), &repaired) != nil || proactiveVoicePresentationHash(original) != proactiveVoicePresentationHash(repaired) {
		t.Fatal("repair changed the source-bound presentation hash")
	}
	_, displayed := f.app.projectProactiveVoiceDelivery(t.Context(), mainChatProactiveTool, stored.Content, stored.ToolInput)
	if !strings.Contains(displayed, `"headline":"Review the launch plan"`) {
		t.Fatalf("displayed = %s", displayed)
	}
	f.app.closedEyes.enabled.Store(true)
	_, held := f.app.projectProactiveVoiceDelivery(t.Context(), mainChatProactiveTool, stored.Content, stored.ToolInput)
	if strings.Contains(held, "Review the launch plan") {
		t.Fatal("saved name escaped source withdrawal")
	}
	f.app.closedEyes.enabled.Store(false)
	before := strings.Replace(stored.ToolInput, "Review the launch plan", "A name chosen later", 1)
	if err := f.app.store.UpdateStreamItemToolInput(t.Context(), stored.ID, before); err != nil {
		t.Fatal(err)
	}
	if err := f.app.repairSavedChatWorkNames(t.Context()); err != nil {
		t.Fatal(err)
	}
	stored, err = f.app.store.LoadStreamItem(t.Context(), "saved-nested-title")
	if err != nil || stored.ToolInput != before {
		t.Fatalf("second repair changed card: %+v %v", stored, err)
	}
}

func TestSavedThreadWorkNameDoesNotReadWithoutSource(t *testing.T) {
	if got := (&App{}).savedThreadWorkNameFromSource(context.Background(), "missing", "proof", "body", ""); got != "" {
		t.Fatal("returned a name without a readable source")
	}
}

func TestSavedThreadFallbackNameUsesOnlyWholeSavedPhrase(t *testing.T) {
	tests := []struct{ source, headline, body, want string }{
		{"Review agent's rebuild on 8528cc4ba and film-en videos in Claude", "Review agent's rebuild on 8528cc4ba and film-en videos in", "Other", "Review agent's rebuild on 8528cc4ba and film-en videos"},
		{"Review the launch plan in detail", "Review the launch plan in", "Other", "Review the launch plan"},
		{"Review the launch plan in detail", "Something else in", "Other", ""},
		{"Review the launch plan in", "Review the launch plan in", "Other", ""},
		{"Review research and development", "Review research and", "Other", ""},
		{"Review the launch plan carefully", "Review the launch plan", "Other", ""},
		{"Review the launch plan.", "Review the launch plan", "Review the launch plan.", "Review the launch plan"},
	}
	for _, tc := range tests {
		if got := savedThreadFallbackName(tc.source, tc.headline, tc.body); got != tc.want {
			t.Errorf("%q = %q, want %q", tc.headline, got, tc.want)
		}
	}
}

func TestRepairSavedChatWorkNamesV2AfterV1(t *testing.T) {
	f := newMainChatReadMemoFixture(t)
	fixture := threadOutcomeFixtures(t)[0]
	row := seedThreadFixture(t, f.app, "saved-title-v2", fixture, time.Now())
	title := "Review agent's rebuild on 8528cc4ba and film-en videos in Claude"
	if _, err := f.app.activity.store.db.Exec(`UPDATE activities SET title = ? WHERE id = ?`, title, row.ID); err != nil {
		t.Fatal(err)
	}
	row.Title = title
	row = saveFixtureOutcome(t, f.app, row, fixture)
	readable := f.app.readableThreadOutcome(t.Context(), row)
	if readable == nil {
		t.Fatal("fixture outcome must be readable")
	}
	source := threadOutcomeSourceID(row.ID, readable)
	headline := "Review agent's rebuild on 8528cc4ba and film-en videos in"
	child := map[string]any{"momentId": "v2-child", "kind": "decision_due", "headline": headline, "body": "The rebuild is still running.", "target": map[string]string{"kind": "thread", "id": row.ID}, "activityOutcomeSource": source}
	payload, _ := json.Marshal(map[string]any{"momentId": "root-v2", "kind": "digest", "items": []any{child}, "readReceipt": "keep"})
	if _, err := f.app.store.AppendStreamItem(t.Context(), f.mainID, storage.StreamItemRow{ID: "saved-v2", Kind: "system", ToolName: mainChatProactiveTool, ToolInput: string(payload), Content: "original"}); err != nil {
		t.Fatal(err)
	}
	if err := f.app.store.SetUIState(t.Context(), "saved_chat_work_names_v1", "done"); err != nil {
		t.Fatal(err)
	}
	if err := f.app.repairSavedChatWorkNames(t.Context()); err != nil {
		t.Fatal(err)
	}
	saved, err := f.app.store.LoadStreamItem(t.Context(), "saved-v2")
	if err != nil {
		t.Fatal(err)
	}
	want := "Review agent's rebuild on 8528cc4ba and film-en videos"
	if !strings.Contains(saved.ToolInput, `"savedWorkNameV1":"`+want+`"`) || !strings.Contains(saved.ToolInput, `"readReceipt":"keep"`) || saved.Content != "original" {
		t.Fatalf("repair did not preserve card: %+v", saved)
	}
	_, projected := f.app.projectProactiveVoiceDelivery(t.Context(), mainChatProactiveTool, saved.Content, saved.ToolInput)
	if !strings.Contains(projected, `"headline":"`+want+`"`) {
		t.Fatalf("fallback not displayed: %s", projected)
	}
	before := saved.ToolInput
	if err := f.app.repairSavedChatWorkNames(t.Context()); err != nil {
		t.Fatal(err)
	}
	saved, err = f.app.store.LoadStreamItem(t.Context(), "saved-v2")
	if err != nil || saved.ToolInput != before {
		t.Fatal("second pass changed the card")
	}
}
