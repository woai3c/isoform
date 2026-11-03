package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"

	"wails-gui/storage"
)

// sideChatOriginJSON encodes the origin payload the way the renderer stores it.
func sideChatOriginJSON(parentID, itemID string) string {
	raw, _ := json.Marshal(sideChatOrigin{ParentID: parentID, ItemID: itemID})
	return string(raw)
}

// seedSideChat creates a main chat with two turns and a side chat branched
// from the last reply. It returns the main and side ids.
func seedSideChat(t *testing.T, app *App, withOrigin bool) (string, string) {
	t.Helper()
	ctx := context.Background()
	main, err := app.ensureMainChat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []storage.StreamItemRow{
		{ID: uuid.NewString(), Kind: "user", Content: "Can you draft the pricing page?"},
		{ID: uuid.NewString(), Kind: "assistant", Content: "Here is a first draft of the pricing page with three tiers."},
		{ID: uuid.NewString(), Kind: "tool", ToolName: "Read", Content: "ignored"},
		{ID: uuid.NewString(), Kind: "user", Content: "Make the middle tier the default."},
		{ID: uuid.NewString(), Kind: "assistant", Content: "Done: the Pro tier is now highlighted as the default."},
	} {
		if _, err := app.store.AppendStreamItem(ctx, main.LocalID, row); err != nil {
			t.Fatal(err)
		}
	}
	side := storage.SessionRow{LocalID: "side-" + uuid.NewString(), ProjectID: main.ProjectID, WorkspaceID: main.WorkspaceID, Label: "Pricing tiers", Kind: storage.SessionKindSide, ParentID: main.LocalID}
	if err := app.store.SaveSession(ctx, side); err != nil {
		t.Fatal(err)
	}
	if withOrigin {
		if _, err := app.store.AppendStreamItem(ctx, side.LocalID, storage.StreamItemRow{
			ID: uuid.NewString(), Kind: "system", ToolName: sideChatOriginTool,
			ToolInput: sideChatOriginJSON(main.LocalID, "item-9"),
			Content:   "Done: the Pro tier is now highlighted as the default.",
		}); err != nil {
			t.Fatal(err)
		}
	}
	return main.LocalID, side.LocalID
}

func TestInjectSideChatContextFirstTurnOnly(t *testing.T) {
	app := mainChatTestApp(t)
	ctx := context.Background()
	mainID, sideID := seedSideChat(t, app, true)

	// The renderer persists the user's first message before the run starts.
	if _, err := app.store.AppendStreamItem(ctx, sideID, storage.StreamItemRow{ID: uuid.NewString(), Kind: "user", Content: "Should the Pro tier be monthly or yearly?"}); err != nil {
		t.Fatal(err)
	}
	block := app.injectSideChatContext(sideID)
	for _, want := range []string{
		"<side-chat-context>",
		`("Isoform")`,
		"<main-chat-recent-turns>",
		"User: Can you draft the pricing page?",
		"Isoform: Done: the Pro tier is now highlighted as the default.",
		"<branched-from-message>",
		"</side-chat-context>",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("context block missing %q:\n%s", want, block)
		}
	}
	if strings.Contains(block, "ignored") {
		t.Fatalf("tool rows leaked into the context block:\n%s", block)
	}

	// A retry before any reply is still the first turn.
	if _, err := app.store.AppendStreamItem(ctx, sideID, storage.StreamItemRow{ID: uuid.NewString(), Kind: "error", Content: "network"}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.store.AppendStreamItem(ctx, sideID, storage.StreamItemRow{ID: uuid.NewString(), Kind: "user", Content: "Should the Pro tier be monthly or yearly?"}); err != nil {
		t.Fatal(err)
	}
	if got := app.injectSideChatContext(sideID); !strings.Contains(got, "<side-chat-context>") {
		t.Fatalf("retry before a reply lost the context:\n%s", got)
	}

	// Once a reply exists, later turns get nothing; the main chat and plain chats get nothing.
	for _, row := range []storage.StreamItemRow{
		{ID: uuid.NewString(), Kind: "assistant", Content: "Yearly, with a monthly option."},
		{ID: uuid.NewString(), Kind: "user", Content: "Thanks."},
	} {
		if _, err := app.store.AppendStreamItem(ctx, sideID, row); err != nil {
			t.Fatal(err)
		}
	}
	if got := app.injectSideChatContext(sideID); got != "" {
		t.Fatalf("second turn injected context:\n%s", got)
	}
	if got := app.injectSideChatContext(mainID); got != "" {
		t.Fatalf("main chat injected side-chat context:\n%s", got)
	}
	if got := app.injectSideChatContext("missing"); got != "" {
		t.Fatalf("unknown session injected context:\n%s", got)
	}
}

func TestInjectSideChatContextWithoutOriginUsesParentTurns(t *testing.T) {
	app := mainChatTestApp(t)
	_, sideID := seedSideChat(t, app, false)
	block := app.injectSideChatContext(sideID)
	if !strings.Contains(block, "<main-chat-recent-turns>") || strings.Contains(block, "<branched-from-message>") {
		t.Fatalf("block = %s", block)
	}
	if got := buildSideChatContext("Isoform", nil, ""); got != "" {
		t.Fatalf("empty inputs produced %q", got)
	}
	long := strings.Repeat("x", 3000)
	trimmed := trimSideChatText(long, sideChatContextItemMax)
	if len([]rune(trimmed)) > sideChatContextItemMax+10 || !strings.Contains(trimmed, "[...]") {
		t.Fatalf("trim produced %d runes", len([]rune(trimmed)))
	}
}

func TestBringBackToMainChatPostsSummaryWithLink(t *testing.T) {
	app := mainChatTestApp(t)
	ctx := context.Background()
	mainID, sideID := seedSideChat(t, app, true)

	// A side chat with no turns has nothing to bring back; a plain chat is refused.
	if _, err := app.BringBackToMainChat(sideID); err == nil {
		t.Fatal("empty side chat was brought back")
	}
	if _, err := app.BringBackToMainChat(mainID); err != errNotSideChat {
		t.Fatalf("main chat bring-back err = %v, want errNotSideChat", err)
	}

	for _, row := range []storage.StreamItemRow{
		{ID: uuid.NewString(), Kind: "user", Content: "Monthly or yearly for Pro?"},
		{ID: uuid.NewString(), Kind: "assistant", Content: "Go yearly by default and offer monthly at a 20% premium.\nThat keeps churn low."},
	} {
		if _, err := app.store.AppendStreamItem(ctx, sideID, row); err != nil {
			t.Fatal(err)
		}
	}
	// No runner in the test app: the summary falls back to the last reply's first line.
	summary, err := app.BringBackToMainChat(sideID)
	if err != nil {
		t.Fatalf("BringBackToMainChat: %v", err)
	}
	if summary != "Go yearly by default and offer monthly at a 20% premium." {
		t.Fatalf("summary = %q", summary)
	}
	reports := mainChatReports(t, app)
	if len(reports) != 1 {
		t.Fatalf("reports = %d, want 1", len(reports))
	}
	report := decodeReport(t, reports[0])
	if report.Source != mainChatSourceSideChat || report.Target.Kind != mainChatTargetSession || report.Target.ID != sideID || report.Title != "Pricing tiers" || report.Summary != summary {
		t.Fatalf("report = %+v", report)
	}
	if !strings.Contains(reports[0].Content, "Pricing tiers") || !strings.Contains(reports[0].Content, summary) {
		t.Fatalf("text = %q", reports[0].Content)
	}
	// Bringing back twice records twice: each bring-back is a new moment.
	if _, err := app.BringBackToMainChat(sideID); err != nil {
		t.Fatal(err)
	}
	if got := len(mainChatReports(t, app)); got != 2 {
		t.Fatalf("second bring-back reports = %d, want 2", got)
	}
	if got := firstSentence(strings.Repeat("a", 300), 50); len([]rune(got)) != 50 {
		t.Fatalf("firstSentence cut to %d runes", len([]rune(got)))
	}
}
