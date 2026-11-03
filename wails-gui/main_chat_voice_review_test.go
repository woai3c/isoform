package main

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/internal/handoff"
	"wails-gui/storage"
)

func TestVoiceReviewActionTargetsReachTheirSource(t *testing.T) {
	app := mainChatTestApp(t)
	mail := proactiveVoiceTestFlaggedMail(t, app, time.Now(), "Please pay the invoice")
	for _, tc := range []struct {
		kind engagement.Kind
		want mainChatTarget
	}{
		{engagement.KindCapturePaused, mainChatTarget{Kind: "settings", ID: "activity"}},
		{engagement.KindWaitingOnReply, mainChatTarget{Kind: "mail"}},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			moment := engagement.Moment{ID: "source", Kind: tc.kind, SubjectID: "person-not-a-message-url"}
			if tc.kind == engagement.KindWaitingOnReply {
				moment = mail
				moment.ID = "source"
			}
			item, ok := app.proactiveItem(moment, engagementText{})
			if !ok || item.Target != tc.want || len(item.Evidence) != 1 || item.Evidence[0].Target != tc.want {
				t.Fatalf("action/evidence source = %+v, want %+v", item, tc.want)
			}
		})
	}
}

func TestVoiceReviewLateStatusReturnsToBottom(t *testing.T) {
	app := mainChatTestApp(t)
	ctx := context.Background()
	main, _ := app.ensureMainChat(ctx)
	at := time.Date(2026, 10, 16, 13, 0, 0, 0, time.Local)
	id := app.postMainChatStatus(mainChatStatusEntry{ID: "first", Kind: "digest_done", Text: "Updates are ready."}, at)
	old, _ := app.store.LoadStreamItem(ctx, id)
	app.appendMainChatItem(ctx, main.LocalID, storage.StreamItemRow{ID: "new-conversation", Kind: "user", Content: "Let's work on something else."}, "chat")
	later := at.Add(150 * time.Minute)
	if got := app.postMainChatStatus(mainChatStatusEntry{ID: "second", Kind: "schedule_ok", Text: "A schedule finished."}, later); got != id {
		t.Fatalf("period identity changed: %s -> %s", id, got)
	}
	rows, _ := app.store.LoadStreamItems(ctx, main.LocalID)
	last := rows[len(rows)-1]
	if last.ID != id || last.SortOrder <= old.SortOrder || last.CreatedAt != later.UnixMilli() || len(mainChatStatusEntries(t, app)) != 2 {
		t.Fatalf("new status stayed above conversation: %+v", rows)
	}
	after, err := app.store.LoadStreamItemsAfter(ctx, main.LocalID, old.SortOrder, 100)
	if err != nil {
		t.Fatal(err)
	}
	page := mobileChatPageFromRows(after.Items, after.HasMore, false, int64(old.SortOrder+1))
	if len(page.Items) == 0 || page.Items[len(page.Items)-1].Kind != "status" {
		t.Fatalf("new status did not reach incremental phone page: %+v", page)
	}
	app.postMainChatStatus(mainChatStatusEntry{ID: "second", Kind: "schedule_ok", Text: "A schedule finished."}, later.Add(time.Minute))
	unchanged, _ := app.store.LoadStreamItem(ctx, id)
	if unchanged.SortOrder != last.SortOrder {
		t.Fatal("duplicate status moved the row again")
	}
}

func TestVoiceReviewCompletedHandoffKeepsReviewCard(t *testing.T) {
	app := mainChatTestApp(t)
	entry := handoff.Entry{ID: "finished", Title: "Prepare the deck", Status: handoff.StatusCompleted, ChatSessionID: "result-chat"}
	app.postHandoffReport(entry)
	app.postHandoffReport(entry)
	rows := mainChatProactiveItems(t, app)
	if len(rows) != 1 || len(mainChatStatusEntries(t, app)) != 0 {
		t.Fatalf("completed work lost its review card: reports=%d statuses=%d", len(rows), len(mainChatStatusEntries(t, app)))
	}
	report := rows[0]
	if report.ActionIDs[0] != mainChatActionReview || report.Target != (mainChatTarget{Kind: mainChatTargetHandoff, ID: "finished"}) || report.Work == nil {
		t.Fatalf("review action lost its result: %+v", report)
	}
}

func TestVoiceReviewSuggestionDecisionKeepsItsOutcome(t *testing.T) {
	for _, tc := range []struct{ action, state string }{{"dismiss", "dismissed"}, {"not_now", "deferred"}} {
		t.Run(tc.action, func(t *testing.T) {
			app, rig, activity := suggestionTestApp(t)
			saveProposedAutomation(t, activity, "init-review", "downloads cleanup", 0.8, "manual", []time.Time{rig.clock.AddDate(0, 0, -5), rig.clock.AddDate(0, 0, -3), rig.clock.AddDate(0, 0, -1)})
			app.checkSuggestionEngagement(context.Background(), rig.clock)
			moments := suggestionMoments(t, rig)
			if len(moments) != 1 {
				t.Fatalf("fixture moments = %+v", moments)
			}
			moment := momentFromRow(moments[0])
			app.postEngagementReport(moment, engagementText{PushBody: "A routine could be easier.", Title: "Review downloads cleanup."})
			state, err := app.ResolveMainChatProactiveAction(moment.ID, tc.action)
			if err != nil || state != tc.state {
				t.Fatalf("%s outcome = %q, %v; want %s", tc.action, state, err, tc.state)
			}
			if got := mainChatProactiveItems(t, app)[0].State; got != tc.state {
				t.Fatalf("stored outcome = %q", got)
			}
			if repeated, err := app.ResolveMainChatProactiveAction(moment.ID, tc.action); err != nil || repeated != tc.state {
				t.Fatalf("repeated decision = %q, %v", repeated, err)
			}
		})
	}
}

func TestVoiceReviewDigestOwnsSourcesOnlyOnChildren(t *testing.T) {
	app := mainChatTestApp(t)
	entries := []engagementDigestItem{{Moment: engagement.Moment{ID: "one-update", Kind: engagement.KindBriefReady, SubjectID: "brief"}, Text: engagementText{PushBody: "Your brief is ready.", Title: "Your morning brief is ready."}}}
	if _, err := app.engagementDigest(context.Background(), entries); err != nil {
		t.Fatal(err)
	}
	item := mainChatProactiveItems(t, app)[0]
	if len(item.Evidence) != 0 || len(item.Items) != 1 || len(item.Items[0].Evidence) != 1 {
		t.Fatalf("digest duplicates or loses sources: %+v", item)
	}
	if strings.Contains(item.Headline, "few") || item.Headline != engagement.DigestHeadline("en", 1) {
		t.Fatalf("single-item digest has plural headline: %q", item.Headline)
	}
}

func proactiveUnboundedStreamLoads(file *ast.File) []string {
	var violations []string
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || (fn.Name.Name != "postProactiveItem" && fn.Name.Name != "ResolveMainChatProactiveAction") {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			if selector, ok := node.(*ast.SelectorExpr); ok && strings.HasPrefix(selector.Sel.Name, "Load") && (strings.Contains(selector.Sel.Name, "StreamItems") || selector.Sel.Name == "LoadConversationHistory") {
				violations = append(violations, fn.Name.Name+": "+selector.Sel.Name)
			}
			return true
		})
	}
	return violations
}

func TestVoiceReviewProactivePathsNeverLoadWholeTranscript(t *testing.T) {
	// This is a resource-boundary check: large tool results must never be
	// materialized by a proactive append or click, even after years of chat.
	file, err := parser.ParseFile(token.NewFileSet(), "main_chat_proactive.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if violations := proactiveUnboundedStreamLoads(file); len(violations) != 0 {
		t.Fatalf("proactive paths use transcript loaders instead of indexed identities: %v", violations)
	}
}

func TestVoiceReviewTranscriptGuardRejectsEveryBroadLoader(t *testing.T) {
	for _, name := range []string{"LoadStreamItems", "LoadAllStreamItems", "LoadRecentStreamItems", "LoadRecentStreamItemsByTurns", "LoadConversationHistory", "LoadStreamItemsByTool", "LoadFutureStreamItems"} {
		for _, path := range []string{"postProactiveItem", "ResolveMainChatProactiveAction"} {
			t.Run(path+"/"+name, func(t *testing.T) {
				source := fmt.Sprintf("package main; func (a *App) %s() { a.store.%s(ctx) }", path, name)
				file, err := parser.ParseFile(token.NewFileSet(), "regression.go", source, 0)
				if err != nil {
					t.Fatal(err)
				}
				if violations := proactiveUnboundedStreamLoads(file); len(violations) != 1 {
					t.Fatalf("guard accepted transcript loader %s: %v", name, violations)
				}
			})
		}
	}
	file, err := parser.ParseFile(token.NewFileSet(), "bounded.go", `package main; func (a *App) postProactiveItem() { a.store.LoadStreamItem(ctx, id); a.store.FindProactiveStreamItem(ctx, session, moment) }`, 0)
	if err != nil {
		t.Fatal(err)
	}
	if violations := proactiveUnboundedStreamLoads(file); len(violations) != 0 {
		t.Fatalf("guard rejected indexed reads: %v", violations)
	}
}

func TestVoiceReviewStorageHasNoUnusedToolLoader(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "storage/items.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range file.Decls {
		if fn, ok := declaration.(*ast.FuncDecl); ok && fn.Name.Name == "LoadStreamItemsByTool" {
			t.Fatal("unused LoadStreamItemsByTool should not remain after indexed proactive lookups replaced it")
		}
	}
}

func TestVoiceReviewLegacySourceTargetsOnMobile(t *testing.T) {
	for _, kind := range []string{"capture_paused", "waiting_on_reply"} {
		target := mainChatTarget{Kind: "today"}
		want := mainChatTarget{Kind: "mail"}
		if kind == "capture_paused" {
			target = mainChatTarget{Kind: "settings", ID: "notifications"}
			want = mainChatTarget{Kind: "settings", ID: "activity"}
		}
		payload, _ := json.Marshal(mainChatProactive{MomentID: "legacy", Kind: kind, Headline: "An update", State: "pending", Target: target, Evidence: []mainChatEvidenceRef{{Kind: "source", ID: "source-1", Target: target}}})
		view, ok := mobileChatItemFromRow(storage.StreamItemRow{ID: "legacy-row", Kind: "system", ToolName: mainChatProactiveTool, ToolInput: string(payload), Content: "An update"})
		if !ok || view.Proactive == nil || view.Proactive.Target != want || view.Proactive.Evidence[0].Target != want {
			t.Fatalf("legacy %s route = %+v", kind, view.Proactive)
		}
	}
	payload, _ := json.Marshal(mainChatStatus{Period: "period", Entries: []mainChatStatusEntry{{ID: "capture", Kind: "capture_paused", Text: "Capture is paused.", Target: mainChatTarget{Kind: "settings", ID: "notifications"}}}})
	view, _ := mobileChatItemFromRow(storage.StreamItemRow{ID: "status-row", Kind: "system", ToolName: mainChatStatusTool, ToolInput: string(payload), Content: "Capture is paused."})
	if view.Status == nil || view.Status.Entries[0].Target.ID != "activity" {
		t.Fatalf("legacy status route = %+v", view.Status)
	}
}

func TestVoiceReviewOldDigestRetryAndActionUseIdentityLookup(t *testing.T) {
	app := mainChatTestApp(t)
	ctx := context.Background()
	entries := []engagementDigestItem{
		{Moment: engagement.Moment{ID: "old-first", Kind: engagement.KindBriefReady, SubjectID: "brief"}, Text: engagementText{PushBody: "Your brief is ready.", Title: "Your morning brief is ready."}},
		{Moment: engagement.Moment{ID: "old-second", Kind: engagement.KindCalendarConflict, SubjectID: "calendar", Params: map[string]string{
			"event_type": "overlap", "event_first_title": "Planning", "event_first_start": time.Now().Add(time.Hour).Format(time.RFC3339),
			"event_second_title": "Review", "event_second_start": time.Now().Add(2 * time.Hour).Format(time.RFC3339),
			"summary": "Two meetings overlap.",
		}}, Text: engagementText{PushBody: "Your calendar needs a look.", Title: "Two events overlap."}},
	}
	if _, err := app.engagementDigest(ctx, entries); err != nil {
		t.Fatal(err)
	}
	main, _ := app.ensureMainChat(ctx)
	var fillers []storage.StreamItemRow
	for i := 0; i <= mainChatReportDedupeWindow; i++ {
		id := fmt.Sprintf("later-card-%d", i)
		payload, _ := json.Marshal(mainChatProactive{MomentID: id, Kind: "brief_ready", Headline: "A brief", State: "pending"})
		fillers = append(fillers, storage.StreamItemRow{ID: id, Kind: "system", ToolName: mainChatProactiveTool, ToolInput: string(payload)})
	}
	if err := app.store.SaveStreamItems(ctx, main.LocalID, fillers); err != nil {
		t.Fatal(err)
	}
	before := len(mainChatStructuredRows(t, app, mainChatProactiveTool))
	if _, err := app.engagementDigest(ctx, entries[1:]); err != nil {
		t.Fatal(err)
	}
	if after := len(mainChatStructuredRows(t, app, mainChatProactiveTool)); after != before {
		t.Fatalf("old child duplicated beyond dedupe window: %d -> %d", before, after)
	}
	state, err := app.ResolveMainChatProactiveAction("old-second", "open")
	if err != nil || state != "opened" {
		t.Fatalf("old digest child cannot be opened: %q %v", state, err)
	}
}
