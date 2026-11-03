package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/storage"
	"yansu-agent/agent"
	"yansu-agent/agent/tools"
)

func awaitEntryUses(t *testing.T, app *App, kind, id string, total int) EntryUses {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		got, err := app.store.EntryUses(t.Context(), kind, id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Total == total {
			return got
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("%s/%s uses=%+v want %d", kind, id, got, total)
		}
	}
}

func TestContextUseChatInjectionLogsOnlySelectedMemoryAndKnowledge(t *testing.T) {
	app := newMemoryInjectionPrivacyFixture(t)
	for _, tc := range []struct{ dir, id string }{{app.memoryStore.baseDir, "m1"}, {app.memoryStore.knowledgeDir, "k1"}} {
		if err := os.MkdirAll(tc.dir, 0700); err != nil {
			t.Fatal(err)
		}
		body := "# Recall\n- **Dimension**: user_insight\n\ncontextuseneedle BODY_MUST_NEVER_ENTER_LOG\n"
		if err := os.WriteFile(filepath.Join(tc.dir, tc.id+".md"), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.memoryStore.Sync("context-use-fixture"); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), contextUseRefKey{}, "chat-session")
	got := app.injectMemoryContext(ctx, "Visible prompt", "contextuseneedle")
	if !strings.Contains(got, "BODY_MUST_NEVER_ENTER_LOG") {
		t.Fatal("fixture did not inject")
	}
	for _, tc := range []struct{ kind, id string }{{"memory", "m1"}, {"knowledge", "k1"}} {
		uses := awaitEntryUses(t, app, tc.kind, tc.id, 1)
		if len(uses.Surfaces) != 1 || uses.Surfaces[0].Surface != "chat" {
			t.Fatalf("uses=%+v", uses)
		}
	}
	counts, err := app.store.EntryUseCounts(t.Context(), "memory", []string{"cooking-00"})
	if err != nil || counts["cooking-00"] != 0 {
		t.Fatalf("unselected use=%v %v", counts, err)
	}
}

func TestContextUseTodaySendAndLogFailure(t *testing.T) {
	for _, failLog := range []bool{false, true} {
		t.Run(fmt.Sprint(failLog), func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			path := filepath.Join(t.TempDir(), "sessions.db")
			store, err := storage.New(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { store.Close() })
			app := &App{store: store, activeTags: map[string]bool{}, eventBroadcaster: NewEventBroadcaster()}
			main, err := app.ensureMainChat(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := app.SaveMemoryEntry("launch", "# Launch plan\nRelease Friday."); err != nil {
				t.Fatal(err)
			}
			dir, _ := knowledgeDir()
			if err := os.WriteFile(filepath.Join(dir, "guide.md"), []byte("# Launch guide\nRelease Friday."), 0600); err != nil {
				t.Fatal(err)
			}
			raw, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			if failLog {
				if _, err := raw.Exec(`CREATE TRIGGER fail_context_use BEFORE INSERT ON context_uses BEGIN SELECT RAISE(FAIL,'fixture log failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			ref := TodayChatContextRef{SourceKind: "memory", SourceID: "launch"}
			if _, err := app.PrepareTodayChatTurn(main.LocalID, "Help with release", ref); err != nil {
				t.Fatal(err)
			}
			before, _ := store.EntryUses(t.Context(), "memory", "launch")
			if before.Total != 0 {
				t.Fatal("prepare counted a use")
			}
			runner := &upcomingCaptureRunner{inputs: make(chan *agent.TaskInput, 1)}
			app.runner = runner
			app.tagCancels = map[string]context.CancelFunc{}
			app.sessionTempDirs = map[string]string{}
			app.streams = map[string]context.CancelFunc{}
			app.runIDByTag = map[string]string{}
			app.pendingTraceIDs = map[string]string{}
			if _, err := app.SendPromptWithTodayContext("Help with release", "", "m", "", main.LocalID, nil, nil, false, false, "en", "", StreamItemData{ID: "user-1", Kind: "user"}, ref); err != nil {
				t.Fatal(err)
			}
			var input *agent.TaskInput
			select {
			case input = <-runner.inputs:
			case <-time.After(5 * time.Second):
				t.Fatal("send did not reach runner")
			}
			prompt, _ := input.PromptFunc()
			if !strings.Contains(prompt, "Release Friday") {
				t.Fatal("source absent from send")
			}
			waitForTagIdle(t, app, main.LocalID)
			if !failLog {
				for _, tc := range []struct{ kind, id string }{{"memory", "launch"}, {"knowledge", "guide"}} {
					uses := awaitEntryUses(t, app, tc.kind, tc.id, 1)
					if len(uses.Surfaces) != 1 || uses.Surfaces[0].Surface != "today_chat" {
						t.Fatalf("uses=%+v", uses)
					}
				}
			}
			var forbidden int
			if err := raw.QueryRow(`SELECT COUNT(*) FROM context_uses WHERE entry_id LIKE '%Release%' OR ref LIKE '%Release%' OR surface LIKE '%Release%'`).Scan(&forbidden); err != nil || forbidden != 0 {
				t.Fatalf("text stored=%d %v", forbidden, err)
			}
		})
	}
}

func TestContextUseTodayPackingOmitsUnsentRecords(t *testing.T) {
	records := []todayChatRecord{{Kind: "memory", ID: "fits", At: 10, Text: "A short note."}, {Kind: "memory", ID: strings.Repeat("x", 10000), At: 1, Text: "Does not fit."}}
	var used []string
	text, count := packTodayChatSection(records, 100, func(record todayChatRecord) { used = append(used, record.ID) })
	if count != 1 || len(used) != 1 || used[0] != "fits" || !strings.Contains(text, "short note") {
		t.Fatalf("text=%q count=%d used=%v", text, count, used)
	}
}

func TestContextUseAssistantToolPublicationAndMCP(t *testing.T) {
	app := newChatContextSourceApp(t)
	c := chatContextMCPClient(t, &agent.TaskInput{WorkDir: os.Getenv("HOME"), ExtraTools: app.chatContextTools("chat-session")})
	result := chatContextMCPCall(t, c, "MemorySearch", map[string]any{"query": "project"})
	if !strings.Contains(result, "project memory marker") {
		t.Fatalf("missing tool result: %s", result)
	}
	uses := awaitEntryUses(t, app, "memory", "context-memory", 1)
	if len(uses.Surfaces) != 1 || uses.Surfaces[0].Surface != "assistant_tool" {
		t.Fatalf("wrong tool surface: %+v", uses)
	}
	// A distinct MCP read records the original ID after all current gates.
	listed := callMCPReadTest(t, app, "memory_search", map[string]any{"query": "project"})
	if len(listed.Items) == 0 {
		t.Fatal("MCP source absent")
	}
	awaitEntryUses(t, app, "memory", "context-memory", 2)
}

func TestContextUseReadOnlyCLIRecordsReturnedEntries(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	writePrivacyMemory(t, "entry", "# A preference\n- **Source**: manual\n\nUse clear plans.")
	server := &cronAPIServer{app: app}
	response := httptest.NewRecorder()
	server.handleMemoryShow(response, httptest.NewRequest("GET", "/memory/show/entry", nil))
	if response.Code != 200 {
		t.Fatalf("show=%d %s", response.Code, response.Body.String())
	}
	uses := awaitEntryUses(t, app, "memory", "entry", 1)
	if uses.Surfaces[0].Surface != "assistant_tool" {
		t.Fatalf("uses=%+v", uses)
	}
}

func TestContextUseBriefRecordsOnlyCitedLearnedEntries(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	svc, _ := newTestMobileBriefService(t, &fakeMobileBriefClock{now: now})
	t.Setenv("HOME", t.TempDir())
	dir, _ := memoryDir()
	content := renderOKFDocument(map[string]any{"type": "Isoform Memory", "title": "Clear plans", "source": "manual", "timestamp": now.Format(time.RFC3339)}, "Use clear plans.")
	if err := os.WriteFile(filepath.Join(dir, "m1.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	knowledge, _ := knowledgeDir()
	if err := os.WriteFile(filepath.Join(knowledge, "k1.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	svc.gather = func(ctx context.Context, slot string, start, end time.Time) (mobileBriefInput, error) {
		return mobileBriefInput{Slot: slot, WindowStart: start, AsOf: end, Location: time.UTC, Learned: []mobileBriefCandidate{
			{SourceKind: "memory", SourceID: "m1", Title: "Clear plans", At: now.Add(-time.Minute)},
			{SourceKind: "knowledge", SourceID: "k1", Title: "Reusable plans", At: now.Add(-time.Minute)},
			{SourceKind: "memory", SourceID: "not-cited", Title: "Protected", At: now.Add(-time.Minute), Protected: true},
		}}, nil
	}
	brief, err := svc.generate(t.Context(), mobileBriefSlotOnRequest, mobileBriefReasonSettings)
	if err != nil {
		t.Fatal(err)
	}
	if len(brief.Sections.Learned) != 2 {
		raw, _ := json.Marshal(brief)
		t.Fatalf("fixture lost learned sources: %s", raw)
	}
	for _, tc := range []struct{ kind, id string }{{"memory", "m1"}, {"knowledge", "k1"}} {
		uses := awaitEntryUses(t, svc.app, tc.kind, tc.id, 1)
		if len(uses.Surfaces) != 1 || uses.Surfaces[0].Surface != "brief" {
			t.Fatalf("uses=%+v", uses)
		}
	}
	none, _ := svc.app.store.EntryUses(t.Context(), "memory", "not-cited")
	if none.Total != 0 {
		t.Fatal("protected candidate recorded")
	}
}

func TestContextUseForgetAndKnowledgeDeleteEraseUses(t *testing.T) {
	app, dir := newForgetTestApp(t)
	state, err := storage.New(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	app.store = state
	body := "# A preference\n\nI prefer concise reviews.\n"
	if err := os.WriteFile(filepath.Join(dir, "memory.md"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	knowledge, _ := knowledgeDir()
	if err := os.WriteFile(filepath.Join(knowledge, "knowledge.md"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	app.recordContextEntries([]contextUseEntry{{"memory", "memory", 0}, {"knowledge", "knowledge", 0}}, "chat", "session")
	awaitEntryUses(t, app, "memory", "memory", 1)
	awaitEntryUses(t, app, "knowledge", "knowledge", 1)
	preview, err := app.GetMemoryForgetPreview("memory")
	if err != nil {
		t.Fatal(err)
	}
	if err := app.ForgetMemoryEntry("memory", preview.ConfirmationToken); err != nil {
		t.Fatal(err)
	}
	if err := app.DeleteKnowledgeEntry("knowledge"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ kind, id string }{{"memory", "memory"}, {"knowledge", "knowledge"}} {
		got, err := state.EntryUses(t.Context(), tc.kind, tc.id)
		if err != nil || got.Total != 0 {
			t.Fatalf("uses remain=%+v %v", got, err)
		}
	}
}

func TestContextUseBindingsApplyCurrentPrivacy(t *testing.T) {
	app := newPrivacyReadTestApp(t)
	writePrivacyMemory(t, "memory", "# Writing preference\n- **Source**: manual\n\nUse clear plans.")
	app.recordContextUses("memory", []string{"memory"}, "chat", "session")
	awaitEntryUses(t, app, "memory", "memory", 1)
	got, err := app.GetEntryUses("memory", "memory")
	if err != nil || got.Total != 1 {
		t.Fatalf("uses=%+v %v", got, err)
	}
	counts, err := app.GetEntryUseCounts("memory", []string{"memory"})
	if err != nil || counts["memory"] != 1 {
		t.Fatalf("counts=%v %v", counts, err)
	}
	app.closedEyes.enabled.Store(true)
	got, err = app.GetEntryUses("memory", "memory")
	if err != nil || got.Total != 0 {
		t.Fatalf("private uses=%+v %v", got, err)
	}
	counts, err = app.GetEntryUseCounts("memory", []string{"memory"})
	if err != nil || counts["memory"] != 0 {
		t.Fatalf("private counts=%v %v", counts, err)
	}
	if _, err := app.GetEntryUses("unknown", "memory"); err == nil {
		t.Fatal("invalid kind accepted")
	}
}

func TestContextUseAssistantRefusalDoesNotCountAUse(t *testing.T) {
	app := newChatContextSourceApp(t)
	tool := &chatContextTool{app: app, name: "MemorySearch", tag: "session"}
	call := tools.ToolCall{ID: "refused", Input: map[string]any{"query": "project"}}
	result, err := tool.Execute(t.Context(), call)
	if err != nil || result.IsError || !strings.Contains(result.Content, "project memory marker") {
		t.Fatalf("fixture read=%+v %v", result, err)
	}
	if _, err := app.store.EntryUses(t.Context(), "memory", "context-memory"); err != nil {
		t.Fatal(err)
	}
	app.closedEyes.enabled.Store(true)
	release, err := tool.LockResultPublication(t.Context(), call)
	if release != nil {
		release()
	}
	if err == nil {
		t.Fatal("withdrawn result published")
	}
	got, err := app.store.EntryUses(t.Context(), "memory", "context-memory")
	if err != nil || got.Total != 0 {
		t.Fatalf("refused read counted=%+v %v", got, err)
	}
}
