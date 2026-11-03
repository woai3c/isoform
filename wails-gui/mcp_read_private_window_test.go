package main

import (
	"encoding/json"
	"testing"
	"time"

	"wails-gui/internal/platform/activitymon"
	"wails-gui/storage"
)

func TestMCPReadPrivateWindowFlagGatesDerivedConsumers(t *testing.T) {
	isolateActivityPrivacyTest(t)
	app := newMCPReadTestApp(t)
	now := time.Now()
	calendarAt := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.Local)
	brief := mobileBriefStoredBrief(t, now.Format("2006-01-02"), mobileBriefSlotMorning, 7)
	brief.Evidence = nil
	brief.Sections = MobileBriefSections{}
	for _, name := range []string{"safe", "private"} {
		sessionID := name + "-source"
		const title = "project evidence"
		if activitymon.IsPrivateBrowsing(title) {
			t.Fatal("fixture must rely on the preserved flag, not a title marker")
		}
		at := seedMCPReadActivity(t, app, sessionID, "Safari", "com.apple.Safari", title, "project evidence", "done")
		event, err := json.Marshal(activitymon.ActivityEvent{Timestamp: at, AppName: "Safari", AppBundleID: "com.apple.Safari", WindowTitle: title, PrivateWindow: name == "private"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := app.activity.store.db.Exec(`UPDATE activity_events SET data=? WHERE session_id=?`, string(event), sessionID); err != nil {
			t.Fatal(err)
		}
		writeMCPReadMemory(t, name+"-memory", sessionID, "project evidence")
		person, err := app.store.CreatePrincipal(app.ctx, storage.PrincipalRow{ID: name + "-person", Kind: storage.PrincipalKindContact, DisplayName: "project " + name, CreatedFrom: "activity"})
		if err != nil {
			t.Fatal(err)
		}
		if err := app.store.BumpPrincipalSighting(app.ctx, person.ID, "Safari", sessionID, at, ""); err != nil {
			t.Fatal(err)
		}
		if err := app.store.UpsertCalendarEvents(app.ctx, []storage.CalendarEventRow{{
			ID: name + "-calendar", Source: "web", SessionID: sessionID, Title: "project " + name,
			StartAt: calendarAt.UnixMilli(), EndAt: calendarAt.Add(time.Hour).UnixMilli(),
		}}, at); err != nil {
			t.Fatal(err)
		}
		brief.Evidence = append(brief.Evidence, MobileBriefRef{Ref: name, SourceKind: "memory", SourceID: name + "-memory"})
		brief.Sections.Learned = append(brief.Sections.Learned, MobileBriefItem{ID: name, Title: "project " + name, At: now.Format(time.RFC3339), Refs: []string{name}})
	}
	storeMCPBrief(t, brief)
	for _, tc := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{tool: "activity_search", args: map[string]any{"query": "project"}, want: "safe-source"},
		{tool: "memory_search", args: map[string]any{"query": "project"}, want: "safe-memory"},
		{tool: "memory_list", args: map[string]any{}, want: "safe-memory"},
		{tool: "people_lookup", args: map[string]any{"name": "project"}, want: "safe-person"},
		{tool: "calendar_today", args: map[string]any{}, want: "safe-calendar"},
		{tool: "calendar_range", args: map[string]any{"from": now.Format("2006-01-02"), "to": now.Format("2006-01-02")}, want: "safe-calendar"},
		{tool: "brief_today", args: map[string]any{}, want: "safe-memory"},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			got := callMCPReadTest(t, app, tc.tool, tc.args)
			if len(got.Items) != 1 || got.Items[0].ID.Text != tc.want {
				t.Fatalf("private-window flag ignored or safe control lost: %+v, want only %s", got, tc.want)
			}
		})
	}
}
