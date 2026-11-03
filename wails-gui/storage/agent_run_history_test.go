package storage

import (
	"fmt"
	"testing"
)

func TestAgentRunRoutesHistoryLookup(t *testing.T) {
	store := newTestStore(t)
	for _, row := range []AgentRunRouteRow{
		{RunID: "chat-run", Backend: "claude-code", Status: "done", Model: "opus"},
		{RunID: "background-run", Backend: "native", Status: "blocked", FallbackFrom: `["claude-code"]`},
		{RunID: "unrequested", Backend: "codex"},
	} {
		if err := store.RecordAgentRunRoute(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := store.AgentRunRoutes(ctx, []string{"chat-run", "background-run", "chat-run", "legacy", "' OR 1=1 --"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("requested history = %+v", rows)
	}
	byID := map[string]AgentRunRouteRow{}
	for _, row := range rows {
		byID[row.RunID] = row
	}
	if byID["chat-run"].Model != "opus" || byID["background-run"].Status != "blocked" || byID["background-run"].FallbackFrom != `["claude-code"]` {
		t.Fatalf("history attribution lost: %+v", rows)
	}
	rows, err = store.AgentRunRoutes(ctx, nil)
	if err != nil || len(rows) != 0 {
		t.Fatalf("empty lookup = %+v, %v", rows, err)
	}
	tooMany := make([]string, 201)
	for i := range tooMany {
		tooMany[i] = fmt.Sprint(i)
	}
	if _, err := store.AgentRunRoutes(ctx, tooMany); err == nil {
		t.Fatal("unbounded history lookup accepted")
	}
}
