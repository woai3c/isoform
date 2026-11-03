package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/platform/activitymon"
	"wails-gui/internal/sop"
	"wails-gui/storage"
)

// Real stores under an isolated, short HOME. No monitor, app or model starts.
func newMCPReadTestApp(t *testing.T) *App {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "mcp-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	t.Setenv("YANSU_MOBILE_STATE_DIR", filepath.Join(home, "mobile"))
	root := filepath.Join(home, configDirName)
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	store, err := storage.New(filepath.Join(root, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	activity, err := NewActivityStore(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { activity.Close() })
	plans, err := sop.NewPlanStore(filepath.Join(root, "sops"))
	if err != nil {
		t.Fatal(err)
	}
	sops, err := sop.NewStore(filepath.Join(root, "sops"))
	if err != nil {
		t.Fatal(err)
	}
	return &App{ctx: context.Background(), store: store, activity: &activityMonitorState{store: activity}, playbookPlans: plans, sopStore: sops}
}

func seedMCPReadActivity(t *testing.T, a *App, id, appName, bundle, title, text, triage string) int64 {
	t.Helper()
	at := time.Now().Add(-time.Hour).UnixMilli()
	apps, _ := json.Marshal([]string{appName})
	if err := a.activity.store.SaveSession(ActivitySessionRow{ID: id, StartTime: at, EndTime: at + 60000, CreatedAt: at, AppNames: string(apps), TriageStatus: triage}, []activitymon.ActivityEvent{{Timestamp: at, AppName: appName, AppBundleID: bundle, WindowTitle: title}}, nil); err != nil {
		t.Fatal(err)
	}
	if text != "" {
		if err := a.activity.store.SaveOCRFrame(OCRFrameRow{SessionID: id, Text: text, FrameTime: 1, Confidence: 1}); err != nil {
			t.Fatal(err)
		}
	}
	return at
}

func callMCPReadTest(t *testing.T, a *App, name string, args any) mcpReadResult {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	value, err := a.callMCPReadTool(context.Background(), name, raw)
	if err != nil {
		t.Fatal(err)
	}
	return value.(mcpReadResult)
}

func TestMCPReadPrivacyGates(t *testing.T) {
	for _, gate := range []string{"protected", "sensitive_default", "sensitive_user", "private", "private_scrubbed", "private_name_only", "missing_bundle", "unknown_app", "malformed_event", "ignored", "past_pause", "unknown_provenance"} {
		t.Run(gate, func(t *testing.T) {
			a := newMCPReadTestApp(t)
			app, bundle, title, triage := "Editor", "com.example.editor", "project evidence", "dismissed"
			switch gate {
			case "protected":
				triage = "protected"
			case "sensitive_default":
				app, bundle = "1Password", "com.1password.1password"
			case "sensitive_user":
				_ = a.store.SetUIState(a.ctx, "sensitive_apps", `{"com.example.editor":"Editor"}`)
			case "missing_bundle":
				app, bundle = "Renamed private app", ""
			case "private":
				app, bundle, title = "Google Chrome", "com.google.Chrome", "project evidence - Incognito"
			case "private_scrubbed":
				app, bundle, title = "Google Chrome", "com.google.Chrome", ""
			case "private_name_only":
				app, bundle, title = "Safari", "", ""
			case "unknown_app":
				app, bundle = "", ""
			case "ignored":
				_ = a.store.SetUIState(a.ctx, voiceInputAppsUIStateKey, `{"com.example.editor":"Editor"}`)
			}
			at := seedMCPReadActivity(t, a, "hidden", app, bundle, title, "project evidence should be withheld", triage)
			if gate == "past_pause" {
				data, _ := json.Marshal([]observationPause{{Start: at - 1, End: at + 70000}})
				_ = a.store.SetUIState(a.ctx, observationPausesKey, string(data))
			}
			if gate == "unknown_provenance" {
				if _, err := a.activity.store.db.Exec("DELETE FROM activity_events WHERE session_id='hidden'"); err != nil {
					t.Fatal(err)
				}
			}
			if gate == "malformed_event" {
				if _, err := a.activity.store.db.Exec(`INSERT INTO activity_events(session_id,timestamp,kind,data,sort_order) VALUES('hidden',?,'test','broken json',2)`, at); err != nil {
					t.Fatal(err)
				}
			}
			if got := callMCPReadTest(t, a, "activity_search", map[string]any{"query": "project"}); len(got.Items) != 0 {
				t.Fatalf("gate leaked: %+v", got)
			}
			writeMCPReadMemory(t, "derived", "hidden", "project private memory")
			if got := callMCPReadTest(t, a, "memory_list", map[string]any{}); len(got.Items) != 0 {
				t.Fatalf("derived memory bypassed gate: %+v", got)
			}
		})
	}
}

func TestMCPReadPauseAndMalformedPrivacyFailClosed(t *testing.T) {
	for _, gate := range []string{"closed_eyes", "pause", "screen_recording", "accessibility", "malformed"} {
		t.Run(gate, func(t *testing.T) {
			a := newMCPReadTestApp(t)
			switch gate {
			case "closed_eyes":
				a.closedEyes.enabled.Store(true)
			case "pause":
				data, _ := json.Marshal([]observationPause{{Start: time.Now().Add(-time.Minute).UnixMilli()}})
				_ = a.store.SetUIState(a.ctx, observationPausesKey, string(data))
			case "malformed":
				_ = a.store.SetUIState(a.ctx, "sensitive_apps", "not json")
			default:
				_ = a.store.SetUIState(a.ctx, permissionUIStateKey(gate), "false")
			}
			for _, name := range []string{"activity_search", "memory_search", "memory_list", "people_lookup", "calendar_today", "calendar_range", "brief_today", "playbook_status", "run_history"} {
				_, err := a.callMCPReadTool(a.ctx, name, json.RawMessage(`{}`))
				if gate == "malformed" {
					if err == nil {
						t.Fatal("malformed privacy allowed")
					}
				} else if !errors.Is(err, errMCPReadPaused) {
					t.Fatalf("%s error=%v", name, err)
				}
			}
		})
	}
}

func writeMCPReadMemory(t *testing.T, id, session, body string) {
	t.Helper()
	dir := filepath.Join(configDirOrFallback(), "memory")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	text := renderOKFDocument(map[string]any{"type": "Isoform Memory", "title": "project memory", "source": "activity", "dimension": "topic_highlight", "activity_sessions": []string{session}, "read_provenance": memoryReadProvenanceJSON(memoryReadProvenance{Version: 1, Kinds: []string{"activity"}, Sessions: []string{session}})}, body)
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestMCPReadSnippetsPIIAndLimits(t *testing.T) {
	a := newMCPReadTestApp(t)
	for i := 0; i < 25; i++ {
		id := fmt.Sprintf("session-%02d", i)
		seedMCPReadActivity(t, a, id, "Editor", "com.example.editor", "project title", strings.Repeat("context ", 80)+"needle alice@example.com 4111 1111 1111 1111", "")
		writeMCPReadMemory(t, id, id, strings.Repeat("context ", 80)+"needle alice@example.com")
	}
	for _, name := range []string{"activity_search", "memory_search", "memory_list"} {
		got := callMCPReadTest(t, a, name, map[string]any{"query": "needle", "limit": 10000})
		if len(got.Items) != 20 {
			t.Fatalf("%s count=%d", name, len(got.Items))
		}
		for _, item := range got.Items {
			if !item.ID.Untrusted || item.Snippet == nil || !item.Snippet.Untrusted || len([]rune(item.Snippet.Text)) > mcpReadSnippetLimit || !strings.Contains(item.Snippet.Text, "needle") || strings.Contains(item.Snippet.Text, "alice@example.com") || strings.Contains(item.Snippet.Text, "4111") {
				t.Fatalf("unsafe snippet: %+v", item)
			}
		}
	}
	got := callMCPReadTest(t, a, "activity_search", map[string]any{"query": "project title", "limit": 1})
	if len(got.Items) != 1 || !strings.Contains(got.Items[0].Snippet.Text, "project title") {
		t.Fatal("window-title fallback unavailable")
	}
	got = callMCPReadTest(t, a, "activity_search", map[string]any{"query": "alice@example.com"})
	if len(got.Items) != 0 {
		t.Fatal("PII search presence leak")
	}
}

func TestMCPReadCalendarPeoplePlaybooksAndRuns(t *testing.T) {
	a := newMCPReadTestApp(t)
	now := time.Now()
	at := now.UnixMilli()
	if err := a.store.UpsertCalendarEvents(a.ctx, []storage.CalendarEventRow{{ID: "calendar", Source: "eventkit", Title: "Review alice@example.com", StartAt: at, EndAt: at + 60000}, {ID: "web-private", Source: "web", Title: "hidden web title", StartAt: at, EndAt: at + 60000}}, at); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.CreatePrincipal(a.ctx, storage.PrincipalRow{ID: "person", Kind: storage.PrincipalKindContact, DisplayName: "Example Person", CreatedFrom: "manual", Notes: "secret transcript"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.sopStore.Save(sop.Entry{ID: "manual", Title: "Review project", Source: sop.SourceManual, Status: sop.StatusConfirmed, Body: "secret steps"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.playbookPlans.Update("manual", func(p *sop.Plan) { p.BodyHash = "approved"; p.Schedule = &sop.Schedule{Enabled: true} }); err != nil {
		t.Fatal(err)
	}
	if err := a.store.SaveCronJob(a.ctx, storage.CronJobRow{ID: "job", Name: "secret job", ScheduleKind: "manual", ScheduleConfig: "{}", TargetKind: "agent", TargetConfig: "{}", RetryPolicy: "{}"}); err != nil {
		t.Fatal(err)
	}
	if err := a.store.InsertCronRun(a.ctx, storage.CronRunRow{ID: "run", JobID: "job", StartedAt: at - 60000, FinishedAt: at, Status: "ok", ResultSummary: "secret transcript", Error: "secret error"}); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string]map[string]any{"calendar_today": {}, "calendar_range": {"from": now.Add(-time.Hour).Format(time.RFC3339), "to": now.Add(time.Hour).Format(time.RFC3339)}, "people_lookup": {"name": "Example"}, "playbook_status": {}, "run_history": {}} {
		got := callMCPReadTest(t, a, name, args)
		if len(got.Items) != 1 {
			t.Fatalf("%s count=%d", name, len(got.Items))
		}
		raw, _ := json.Marshal(got)
		if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "alice@example.com") {
			t.Fatalf("unsafe %s: %s", name, raw)
		}
	}
	_ = a.store.SetUIState(a.ctx, permissionUIStateKey(calendarPermissionKey), "false")
	if got := callMCPReadTest(t, a, "calendar_today", map[string]any{}); len(got.Items) != 0 {
		t.Fatal("calendar permission bypass")
	}
}

func TestMCPReadBriefRevalidatesSourcesAndWithholdsNarrative(t *testing.T) {
	a := newMCPReadTestApp(t)
	seedMCPReadActivity(t, a, "safe", "Editor", "com.example.editor", "project", "", "")
	writeMCPReadMemory(t, "safe-memory", "safe", "project memory")
	now := time.Now()
	brief := mobileBriefStoredBrief(t, now.Format("2006-01-02"), mobileBriefSlotMorning, 7)
	brief.Narrative = []MobileBriefPara{{Text: "secret unreferenced narrative"}}
	brief.Evidence = []MobileBriefRef{{Ref: "r1", SourceKind: "memory", SourceID: "safe-memory"}, {Ref: "r2", SourceKind: "memory", SourceID: "missing"}}
	brief.Sections = MobileBriefSections{Learned: []MobileBriefItem{{ID: "safe", Title: "project brief", At: now.Format(time.RFC3339), Refs: []string{"r1"}}, {ID: "unsafe", Title: "secret old private memory", At: now.Format(time.RFC3339), Refs: []string{"r2"}}}}
	dir, _ := mobileBriefStateDir()
	if err := newMobileBriefStore(dir).save(brief, now); err != nil {
		t.Fatal(err)
	}
	got := callMCPReadTest(t, a, "brief_today", map[string]any{})
	if len(got.Items) != 1 || got.Items[0].ID.Text != "safe-memory" {
		t.Fatalf("brief=%+v", got)
	}
	if got.Items[0].Title.Text != "project memory" {
		t.Fatal("brief served copied title instead of current source")
	}
	_ = a.store.SetUIState(a.ctx, "sensitive_apps", `{"com.example.editor":"Editor"}`)
	if got := callMCPReadTest(t, a, "brief_today", map[string]any{}); len(got.Items) != 0 {
		t.Fatal("cached brief bypassed new privacy exclusion")
	}
}

func TestMCPReadArgumentsAndReadOnly(t *testing.T) {
	a := newMCPReadTestApp(t)
	for _, test := range []struct{ name, args string }{{"activity_search", `{}`}, {"memory_search", `{}`}, {"people_lookup", `{}`}, {"memory_list", `{"kind":"made_up"}`}, {"calendar_range", `{"from":"2020-01-01","to":"2030-01-01"}`}, {"memory_list", `{"write":true}`}, {"memory_write", `{}`}, {"memory_list", `{} {}`}} {
		if _, err := a.callMCPReadTool(a.ctx, test.name, json.RawMessage(test.args)); err == nil {
			t.Fatalf("accepted %s %s", test.name, test.args)
		}
	}
	for _, name := range []string{"memory_list", "brief_today"} {
		callMCPReadTest(t, a, name, map[string]any{})
	}
	if _, err := os.Stat(filepath.Join(configDirOrFallback(), "memory")); !os.IsNotExist(err) {
		t.Fatal("read created memory directory")
	}
}

func TestMCPReadOCRCaptureTimes(t *testing.T) {
	a := newMCPReadTestApp(t)
	at := seedMCPReadActivity(t, a, "timing", "Editor", "com.example.editor", "ordinary title", "", "")
	if err := a.activity.store.SaveOCRFrame(OCRFrameRow{SessionID: "timing", SegmentID: 31, FrameTime: 2, Text: "needle evidence"}); err != nil {
		t.Fatal(err)
	}
	// Without the originating capture, only the session timestamp is known.
	got := callMCPReadTest(t, a, "activity_search", map[string]any{"query": "needle"})
	if len(got.Items) != 1 || got.Items[0].At != at {
		t.Fatalf("unknown frame time: %+v", got)
	}
	_, err := a.activity.store.db.Exec(`INSERT INTO activity_segments(id,session_id,start_time,end_time,file_path,width,height,fps,codec,size_bytes) VALUES(31,'timing',?,?,'',0,0,0,'',0)`, at+20000, at+40000)
	if err != nil {
		t.Fatal(err)
	}
	rangeArgs := func(captured int64) map[string]any {
		return map[string]any{"query": "needle", "from": time.UnixMilli(captured - 1000).Format(time.RFC3339Nano), "to": time.UnixMilli(captured + 1000).Format(time.RFC3339Nano)}
	}
	got = callMCPReadTest(t, a, "activity_search", rangeArgs(at+22000))
	if len(got.Items) != 1 || got.Items[0].At != at+22000 {
		t.Fatalf("legacy segment offset: %+v", got)
	}
	// IDs are overloaded: an exact same-session snapshot wins over segment.
	if _, err := a.activity.store.db.Exec(`INSERT INTO activity_snapshots(id,session_id,timestamp,file_path) VALUES(31,'timing',?,'')`, at+30000); err != nil {
		t.Fatal(err)
	}
	got = callMCPReadTest(t, a, "activity_search", rangeArgs(at+30000))
	if len(got.Items) != 1 || got.Items[0].At != at+30000 {
		t.Fatalf("snapshot precedence: %+v", got)
	}
	if got := callMCPReadTest(t, a, "activity_search", rangeArgs(at+22000)); len(got.Items) != 0 {
		t.Fatal("used segment timestamp despite exact snapshot")
	}
}
