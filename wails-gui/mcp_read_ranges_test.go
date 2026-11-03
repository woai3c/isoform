package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"wails-gui/storage"
)

func TestMCPReadDateOnlyRangesIncludeEndDay(t *testing.T) {
	previous := time.Local
	zone, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	time.Local = zone
	t.Cleanup(func() { time.Local = previous })
	for _, date := range []string{"2026-10-16", "2026-03-08", "2026-11-01"} {
		start, end, err := mcpReadRange(date, date, false)
		if err != nil || start.Format("2006-01-02") != date || !end.Equal(start.AddDate(0, 0, 1)) {
			t.Fatalf("same day %s: %v %v %v", date, start, end, err)
		}
	}
	for _, dates := range [][2]string{{"2026-10-14", "2026-10-15"}, {"2026-10-01", "2026-10-31"}, {"2026-11-01", "2026-12-01"}} {
		start, end, err := mcpReadRange(dates[0], dates[1], false)
		if err != nil || !start.Before(end) || end.AddDate(0, 0, -1).Format("2006-01-02") != dates[1] {
			t.Fatalf("inclusive range: %v %v %v", start, end, err)
		}
	}
	if _, _, err := mcpReadRange("2026-10-01", "2026-11-01", false); err == nil {
		t.Fatal("accepted 32 days")
	}
	start, end, err := mcpReadRange("2026-10-16T08:00:00-07:00", "2026-10-16T09:00:00-07:00", false)
	if err != nil || end.Sub(start) != time.Hour {
		t.Fatalf("timestamp end changed: %v %v", end, err)
	}
}

func TestMCPReadCalendarPaginationAndLocalTimes(t *testing.T) {
	a := newMCPReadTestApp(t)
	start := time.Date(2026, 10, 1, 10, 0, 0, 0, time.Local)
	events := []storage.CalendarEventRow{}
	for i := 0; i < 60; i++ {
		at := start.AddDate(0, 0, i/2).Add(time.Duration(i%2) * time.Hour).UnixMilli()
		events = append(events, storage.CalendarEventRow{ID: fmt.Sprintf("event-%02d", i), Source: "eventkit", Title: fmt.Sprintf("Review %d", i), StartAt: at, EndAt: at + 60000})
	}
	events = append(events, storage.CalendarEventRow{ID: "withheld", Source: "web", Title: "private", StartAt: start.UnixMilli(), EndAt: start.Add(time.Minute).UnixMilli()})
	if err := a.store.UpsertCalendarEvents(a.ctx, events, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for offset := 0; offset < 60; offset += 20 {
		result := callMCPReadTest(t, a, "calendar_range", map[string]any{"from": "2026-10-01", "to": "2026-10-31", "limit": 100, "offset": offset})
		if len(result.Items) != 20 || result.Total != 60 || result.Withheld != 1 || result.Omitted != max(0, 40-offset) || result.Truncated != (offset < 40) || result.Bounded != (offset == 40) {
			t.Fatalf("page %d: %+v", offset, result)
		}
		if offset < 40 && (result.NextOffset == nil || *result.NextOffset != offset+20) {
			t.Fatalf("missing next offset: %+v", result)
		}
		for _, item := range result.Items {
			if ids[item.ID.Text] {
				t.Fatal("duplicate across pages")
			}
			ids[item.ID.Text] = true
			local, err := time.Parse(time.RFC3339, item.AtLocal)
			if err != nil || local.UnixMilli() != item.At || item.EndAtLocal == "" {
				t.Fatalf("local time: %+v %v", item, err)
			}
		}
	}
	if len(ids) != 60 {
		t.Fatal("pagination missed events")
	}
	same := callMCPReadTest(t, a, "calendar_range", map[string]any{"from": "2026-10-30", "to": "2026-10-30"})
	if len(same.Items) != 2 {
		t.Fatalf("same date lost events: %+v", same)
	}
	today := time.Now()
	at := time.Date(today.Year(), today.Month(), today.Day(), 12, 0, 0, 0, time.Local).UnixMilli()
	if err := a.store.UpsertCalendarEvents(a.ctx, []storage.CalendarEventRow{{ID: "today", Source: "eventkit", Title: "Today", StartAt: at, EndAt: at + 60000}}, at); err != nil {
		t.Fatal(err)
	}
	result := callMCPReadTest(t, a, "calendar_range", map[string]any{"from": today.Format("2006-01-02"), "to": today.Format("2006-01-02")})
	found := false
	for _, item := range result.Items {
		if item.ID.Text == "today" {
			found = true
		}
	}
	if !found {
		t.Fatal("range ending today omitted midday")
	}
	if err := a.store.SetUIState(a.ctx, permissionUIStateKey(calendarPermissionKey), "false"); err != nil {
		t.Fatal(err)
	}
	result = callMCPReadTest(t, a, "calendar_today", map[string]any{})
	if result.Reason == "" || !strings.Contains(result.Reason, "permission") || len(result.Items) != 0 {
		t.Fatalf("permission reason: %+v", result)
	}
}

func TestMCPReadValidationAndEmptyReasons(t *testing.T) {
	a := newMCPReadTestApp(t)
	for _, args := range []string{`{"from":"Oct 16","to":"2026-10-16"}`, `{"from":"2026-10-01","to":"2026-12-01"}`, `{"offset":-1}`} {
		_, err := a.callMCPReadTool(a.ctx, "calendar_range", json.RawMessage(args))
		if _, ok := err.(*mcpReadValidationError); !ok {
			t.Fatalf("not a code-owned validation error: %v", err)
		}
	}
	got := callMCPReadTest(t, a, "activity_search", map[string]any{"query": "nothing"})
	if got.Reason == "" || got.Truncated || !got.Bounded {
		t.Fatalf("empty result unexplained: %+v", got)
	}
	seedMCPReadActivity(t, a, "hidden", "Editor", "com.example.editor", "project evidence", "", "done")
	if err := a.store.SetUIState(a.ctx, "sensitive_apps", `{"com.example.editor":"Editor"}`); err != nil {
		t.Fatal(err)
	}
	got = callMCPReadTest(t, a, "activity_search", map[string]any{"query": "project"})
	if got.Withheld != 0 || !strings.Contains(strings.ToLower(got.Reason), "privacy") {
		t.Fatalf("search withholding must not expose keyword matches: %+v", got)
	}
}

func TestMCPReadActivityCapsAfterSessionDedupe(t *testing.T) {
	a := newMCPReadTestApp(t)
	at := seedMCPReadActivity(t, a, "busy", "Editor", "com.example.editor", "project evidence", "", "done")
	seedMCPReadActivity(t, a, "other", "Editor", "com.example.editor", "project evidence", "", "done")
	tx, err := a.activity.store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < 1100; i++ {
		if _, err := tx.Exec(`INSERT INTO activity_ocr_frames(session_id,segment_id,frame_time,text,confidence) VALUES(?,?,?,?,?)`, "busy", 0, float64(i), "project evidence repeats", 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	result := callMCPReadTest(t, a, "activity_search", map[string]any{"query": "project", "from": time.UnixMilli(at).Format("2006-01-02"), "to": time.Now().Format("2006-01-02")})
	if len(result.Items) != 2 || result.Truncated {
		t.Fatalf("busy session starved other results: %+v", result)
	}
	limited := callMCPReadTest(t, a, "activity_search", map[string]any{"query": "project", "limit": 1})
	if len(limited.Items) != 1 || !limited.Truncated || limited.Bounded || limited.Omitted != 1 {
		t.Fatalf("result cap silent: %+v", limited)
	}
}

func TestMCPReadWithheldMetadataDoesNotRevealQueryMatches(t *testing.T) {
	a := newMCPReadTestApp(t)
	seedMCPReadActivity(t, a, "hidden", "Editor", "com.example.editor", "Acquisition Aurora", "", "done")
	if err := a.store.SetUIState(a.ctx, "sensitive_apps", `{"com.example.editor":"Editor"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.CreatePrincipal(a.ctx, storage.PrincipalRow{ID: "person", Kind: storage.PrincipalKindContact, DisplayName: "Acquisition Aurora", CreatedFrom: "activity"}); err != nil {
		t.Fatal(err)
	}
	for _, tool := range []struct{ name, key string }{{"activity_search", "query"}, {"people_lookup", "name"}} {
		present := callMCPReadTest(t, a, tool.name, map[string]any{tool.key: "Acquisition Aurora"})
		absent := callMCPReadTest(t, a, tool.name, map[string]any{tool.key: "no such record"})
		one, _ := json.Marshal(present)
		two, _ := json.Marshal(absent)
		if len(present.Items) != 0 || string(one) != string(two) {
			t.Fatalf("protected query oracle %s: %s vs %s", tool.name, one, two)
		}
	}
}

func TestMCPReadCalendarBudgetDoesNotLoopOffsets(t *testing.T) {
	a := newMCPReadTestApp(t)
	at := seedMCPReadActivity(t, a, "calendar-source", "Editor", "com.example.editor", "Visible", "", "")
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.Local).UnixMilli()
	if err := a.store.UpsertCalendarEvents(a.ctx, []storage.CalendarEventRow{{ID: "event", Source: "eventkit", SessionID: "calendar-source", StartAt: start, EndAt: start + 60000}}, at); err != nil {
		t.Fatal(err)
	}
	p, err := a.mcpReadPrivacy(a.ctx)
	if err != nil {
		t.Fatal(err)
	}
	p.sessionChecks = mcpReadSessionCheckLimit
	result := mcpReadResult{}
	if err := p.calendarRead("calendar_today", mcpReadArguments{Limit: 20}, &result); err != nil {
		t.Fatal(err)
	}
	if !result.Truncated || result.Omitted != 1 || result.Withheld != 0 || result.NextOffset != nil {
		t.Fatalf("privacy budget produced a misleading next page: %+v", result)
	}
}
