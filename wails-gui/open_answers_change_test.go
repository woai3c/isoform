package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"wails-gui/storage"
)

func TestParseOpenAnswerChangeBlockStrictly(t *testing.T) {
	valid := `{"id":"answer-1","action":"retime","at":"2030-05-06T18:00:00Z"}`
	parsed, err := parseOpenAnswerChangeBlock(valid)
	if err != nil || parsed.ID != "answer-1" || parsed.Action != "retime" || parsed.At != "2030-05-06T18:00:00Z" {
		t.Fatalf("valid block = %+v, %v", parsed, err)
	}
	for _, raw := range []string{
		`not json`,
		`{"id":"answer-1","action":"cancel","extra":true}`,
		`{"id":"answer-1","ID":"other","action":"cancel"}`,
		`{"id":"answer-1","action":"cancel","action":"brief"}`,
		`{"id":"answer-1","action":"cancel","at":"2030-05-06T18:00:00Z"}`,
		`{"id":"answer-1","action":"retime","at":"tomorrow"}`,
		`{"id":"answer-1","action":"cancel"} trailing`,
		`[]`,
	} {
		if got, err := parseOpenAnswerChangeBlock(raw); err == nil {
			t.Errorf("accepted %q as %+v", raw, got)
		}
	}
}

func TestOpenAnswerChangeStreamHidesAndValidatesBlocks(t *testing.T) {
	block := `<open_answer_change>{"id":"answer-1","action":"cancel"}</open_answer_change>`
	for _, tc := range []struct {
		name   string
		chunks []AgentEvent
		want   bool
	}{
		{name: "standalone", chunks: []AgentEvent{{Type: "assistant", Content: block}, {Type: "done"}}, want: true},
		{name: "split delimiter", chunks: []AgentEvent{{Type: "assistant", Content: block[:17]}, {Type: "assistant", Content: block[17:]}, {Type: "done"}}, want: true},
		{name: "malformed", chunks: []AgentEvent{{Type: "assistant", Content: `<open_answer_change>{"id":</open_answer_change>`}, {Type: "done"}}},
		{name: "unknown field", chunks: []AgentEvent{{Type: "assistant", Content: `<open_answer_change>{"id":"answer-1","action":"cancel","extra":true}</open_answer_change>`}, {Type: "done"}}},
		{name: "text before block", chunks: []AgentEvent{{Type: "assistant", Content: "Okay. " + block}, {Type: "done"}}},
		{name: "text after block", chunks: []AgentEvent{{Type: "assistant", Content: block + "Done."}, {Type: "done"}}},
		{name: "other structured block", chunks: []AgentEvent{{Type: "assistant", Content: `<open_answer>{"question":"another promise"}</open_answer>` + block}, {Type: "done"}}},
		{name: "two blocks", chunks: []AgentEvent{{Type: "assistant", Content: block + block}, {Type: "done"}}},
		{name: "tool boundary", chunks: []AgentEvent{{Type: "assistant", Content: block}, {Type: "tool_use_start"}, {Type: "done"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stream openAnswerStream
			var visible strings.Builder
			for _, evt := range tc.chunks {
				stream.emit(evt, func(event AgentEvent) {
					if event.Type == "assistant" {
						visible.WriteString(event.Content)
					}
				})
			}
			if got := stream.completedChange() != nil; got != tc.want {
				t.Fatalf("completed change = %v, want %v", got, tc.want)
			}
			if strings.Contains(visible.String(), "open_answer_change") || strings.Contains(visible.String(), "answer-1") {
				t.Fatalf("structured block leaked: %q", visible.String())
			}
		})
	}
}

func TestApplyOpenAnswerChangeActions(t *testing.T) {
	for _, tc := range []struct {
		action, wantState, wantDelivery, wantAck string
	}{
		{"settled_only", "open", "settled_only", "Okay, only when it's settled."},
		{"brief", "open", "brief", "Okay, it'll be in tomorrow's brief."},
		{"cancel", "cancelled", "around", "Okay, I've stopped checking."},
	} {
		t.Run(tc.action, func(t *testing.T) {
			app, sessionID, userID, runID := openAnswerChangeTestApp(t)
			row := createOpenAnswerChangeRecord(t, app, sessionID, "target", "Today News", time.Now().Add(time.Hour))
			app.applyOpenAnswerChange(completedOpenAnswerChange{
				sessionID: sessionID, userID: userID, runID: runID,
				change: openAnswerChangeBlock{ID: row.ID, Action: tc.action},
			})
			got, err := app.store.GetOpenAnswer(t.Context(), row.ID)
			if err != nil || got.State != tc.wantState || got.Delivery != tc.wantDelivery {
				t.Fatalf("updated row = %+v, %v", got, err)
			}
			assertOpenAnswerChangeAck(t, app, sessionID, runID, tc.wantAck)
		})
	}
}

func TestApplyOpenAnswerChangeRetimesNamedWindow(t *testing.T) {
	app, sessionID, userID, runID := openAnswerChangeTestApp(t)
	row := createOpenAnswerChangeRecord(t, app, sessionID, "retime-target", "Today News", time.Now().Add(time.Hour))
	at := time.Now().Add(3 * time.Hour).UTC().Truncate(time.Second)
	app.applyOpenAnswerChange(completedOpenAnswerChange{
		sessionID: sessionID, userID: userID, runID: runID,
		change: openAnswerChangeBlock{ID: row.ID, Action: "retime", At: at.Format(time.RFC3339)},
	})
	got, err := app.store.GetOpenAnswer(t.Context(), row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.WindowStart != at.UnixMilli() || got.WindowEnd != at.UnixMilli() ||
		got.ExpiresAt != at.Add(24*time.Hour).UnixMilli() || got.LastCheckAt != 0 {
		t.Fatalf("retimed record = %+v", got)
	}
	assertOneOpenAnswerCheckAt(t, app, row.ID, at.UnixMilli())
	assertOpenAnswerChangeAck(t, app, sessionID, runID, openAnswerChangeAck("retime", at, "English"))
}

func TestApplyOpenAnswerChangeRestoresScheduleWhenRetimeCheckFails(t *testing.T) {
	app, sessionID, userID, runID := openAnswerChangeTestApp(t)
	row := createOpenAnswerChangeRecord(t, app, sessionID, "retime-failure", "Today News", time.Now().Add(time.Hour))
	app.cronScheduler = nil
	at := time.Now().Add(3 * time.Hour).UTC().Truncate(time.Second)
	app.applyOpenAnswerChange(completedOpenAnswerChange{
		sessionID: sessionID, userID: userID, runID: runID,
		change: openAnswerChangeBlock{ID: row.ID, Action: "retime", At: at.Format(time.RFC3339)},
	})
	got, err := app.store.GetOpenAnswer(t.Context(), row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.WindowStart != row.WindowStart || got.WindowEnd != row.WindowEnd || got.ExpiresAt != row.ExpiresAt {
		t.Fatalf("failed retime changed the previous schedule: got %+v, previous %+v", got, row)
	}
	items, err := app.store.LoadStreamItems(t.Context(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Kind == "assistant" && item.RunID == runID && strings.TrimSpace(item.Content) != "I will let you know." {
			t.Fatalf("failed retime wrote acknowledgement: %+v", item)
		}
	}
}

func TestApplyOpenAnswerChangeRejectsInvalidTargetsAndTimes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		setup  func(*testing.T, *App, string) storage.OpenAnswerRow
		change func(storage.OpenAnswerRow) openAnswerChangeBlock
	}{
		{
			name: "wrong session",
			setup: func(t *testing.T, app *App, sessionID string) storage.OpenAnswerRow {
				addOpenAnswerChangeTestSession(t, app, sessionID, "another-session")
				return createOpenAnswerChangeRecord(t, app, "another-session", "wrong-session", "Today News", time.Now().Add(time.Hour))
			},
			change: func(row storage.OpenAnswerRow) openAnswerChangeBlock {
				return openAnswerChangeBlock{ID: row.ID, Action: "cancel"}
			},
		},
		{
			name: "non-open",
			setup: func(t *testing.T, app *App, sessionID string) storage.OpenAnswerRow {
				row := createOpenAnswerChangeRecord(t, app, sessionID, "closed", "Today News", time.Now().Add(time.Hour))
				if err := app.store.CancelOpenAnswer(t.Context(), row.ID); err != nil {
					t.Fatal(err)
				}
				row.State = "cancelled"
				return row
			},
			change: func(row storage.OpenAnswerRow) openAnswerChangeBlock {
				return openAnswerChangeBlock{ID: row.ID, Action: "brief"}
			},
		},
		{
			name: "Mail retime",
			setup: func(t *testing.T, app *App, sessionID string) storage.OpenAnswerRow {
				return createOpenAnswerChangeRecord(t, app, sessionID, "mail", "Mail", time.Time{})
			},
			change: func(row storage.OpenAnswerRow) openAnswerChangeBlock {
				return openAnswerChangeBlock{ID: row.ID, Action: "retime", At: time.Now().Add(time.Hour).Format(time.RFC3339)}
			},
		},
		{
			name: "eight days away",
			setup: func(t *testing.T, app *App, sessionID string) storage.OpenAnswerRow {
				return createOpenAnswerChangeRecord(t, app, sessionID, "too-far", "Today News", time.Now().Add(time.Hour))
			},
			change: func(row storage.OpenAnswerRow) openAnswerChangeBlock {
				return openAnswerChangeBlock{ID: row.ID, Action: "retime", At: time.Now().Add(8 * 24 * time.Hour).Format(time.RFC3339)}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, sessionID, userID, runID := openAnswerChangeTestApp(t)
			row := tc.setup(t, app, sessionID)
			app.applyOpenAnswerChange(completedOpenAnswerChange{
				sessionID: sessionID, userID: userID, runID: runID, change: tc.change(row),
			})
			got, err := app.store.GetOpenAnswer(t.Context(), row.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.State != row.State || got.Delivery != row.Delivery || got.WindowStart != row.WindowStart || got.ExpiresAt != row.ExpiresAt {
				t.Fatalf("invalid change mutated record: got=%+v want=%+v", got, row)
			}
			items, err := app.store.LoadStreamItems(context.Background(), sessionID)
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range items {
				if item.RunID == runID && item.Kind == "assistant" && strings.Contains(item.Content, "Okay,") {
					t.Fatalf("invalid change appended acknowledgement: %+v", item)
				}
			}
		})
	}
}

func TestOpenAnswerChangeAckUsesLocalizedClock(t *testing.T) {
	at := time.Date(2030, time.May, 6, 15, 4, 0, 0, time.Local)
	for _, tc := range []struct{ language, want string }{
		{"English", "Okay, I'll tell you around " + briefShortClock(at.Hour(), at.Minute(), "English") + "."},
		{"Simplified Chinese", "好的，我大约" + briefShortClock(at.Hour(), at.Minute(), "Simplified Chinese") + "告诉你。"},
	} {
		if got := openAnswerChangeAck("retime", at, tc.language); got != tc.want {
			t.Errorf("%s acknowledgement = %q, want %q", tc.language, got, tc.want)
		}
	}
}

func openAnswerChangeTestApp(t *testing.T) (*App, string, string, string) {
	t.Helper()
	app := openAnswerAdmissionApp(t)
	sessionID, userID, _ := appendOpenAnswerAdmissionTurn(t, app, "Change my promise")
	return app, sessionID, userID, "open-answer-run"
}

func createOpenAnswerChangeRecord(t *testing.T, app *App, sessionID, id, source string, at time.Time) storage.OpenAnswerRow {
	t.Helper()
	now := time.Now()
	windowStart, windowEnd := int64(0), int64(0)
	if !at.IsZero() {
		windowStart, windowEnd = at.UnixMilli(), at.Add(time.Hour).UnixMilli()
	}
	row := storage.OpenAnswerRow{
		ID: id, ChatSessionID: sessionID, AssistantMessageID: "promise-" + id,
		Question: "When will the result be known?", Topic: "results", Settles: "result", Source: source,
		WindowStart: windowStart, WindowEnd: windowEnd, Delivery: "around",
		ExpiresAt: now.Add(8 * 24 * time.Hour).UnixMilli(), State: "open", CreatedAt: now.UnixMilli(),
	}
	if source == "Mail" {
		row.Settles = "reply"
		row.WatchedEvent = `{"who":"Priya","label":"plans"}`
	}
	if err := app.store.CreateOpenAnswer(context.Background(), row); err != nil {
		t.Fatal(err)
	}
	return row
}

func addOpenAnswerChangeTestSession(t *testing.T, app *App, sourceID, targetID string) {
	t.Helper()
	source, err := app.store.LoadSession(t.Context(), sourceID)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.store.SaveSession(t.Context(), storage.SessionRow{
		LocalID: targetID, ProjectID: source.ProjectID, WorkspaceID: source.WorkspaceID,
		Label: "Another chat", Kind: storage.SessionKindMain,
	}); err != nil {
		t.Fatal(err)
	}
}

func assertOpenAnswerChangeAck(t *testing.T, app *App, sessionID, runID, want string) {
	t.Helper()
	items, err := app.store.LoadStreamItems(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	var found []storage.StreamItemRow
	for _, item := range items {
		if item.Kind == "assistant" && item.RunID == runID && strings.TrimSpace(item.Content) != "I will let you know." {
			found = append(found, item)
		}
	}
	if len(found) != 1 || found[0].Content != want {
		t.Fatalf("acknowledgement items = %+v, want one %q", found, want)
	}
}

func TestResolveCompletedOpenAnswerChangeRequiresPersistedTurnIdentity(t *testing.T) {
	change := &openAnswerChangeBlock{ID: "answer", Action: "cancel"}
	resolved, ok := resolveCompletedOpenAnswerChange("session", "user", "run", change)
	if !ok || resolved.sessionID != "session" || resolved.userID != "user" || resolved.runID != "run" {
		t.Fatalf("resolved change = %+v, %v", resolved, ok)
	}
	if _, ok := resolveCompletedOpenAnswerChange("session", "", "run", change); ok {
		t.Fatal("resolved change without a persisted user row")
	}
}
