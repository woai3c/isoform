package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"wails-gui/storage"

	"yansu-agent/agent"
)

type activitySummaryI18nFakeRunner struct {
	mu      sync.Mutex
	prompts []string
	calls   int
}

func (r *activitySummaryI18nFakeRunner) Init(ctx context.Context) error { return nil }
func (r *activitySummaryI18nFakeRunner) ExecuteTask(ctx context.Context, input *agent.TaskInput, callback agent.StreamCallback) (string, error) {
	r.mu.Lock()
	r.calls++
	r.prompts = append(r.prompts, input.Prompt)
	r.mu.Unlock()

	prompt := input.Prompt
	if strings.Contains(prompt, "Normalize") || strings.Contains(prompt, "translate") || strings.Contains(prompt, "Translate") {
		return "", errors.New("normalization failed")
	}

	response := "今天主要推进了 Activity 摘要国际化测试。"
	if strings.Contains(prompt, "work week") {
		response = "本周主要围绕 Activity 摘要国际化和缓存边界展开。"
	}
	if strings.Contains(prompt, "STRICT JSON") && strings.Contains(prompt, `"moments"`) {
		response = `{"moments":[{"startIdx":1,"endIdx":2,"title":"实现摘要国际化","subtitle":"覆盖每日和片段摘要","description":"围绕 **Activity Summary** 补充中文输出与缓存边界测试。"}]}`
	}
	if err := callback(&agent.StreamEvent{Type: "assistant", Content: response}); err != nil {
		return "", err
	}
	return "run-test", nil
}
func (r *activitySummaryI18nFakeRunner) CancelRun(runID string) error { return nil }
func (r *activitySummaryI18nFakeRunner) RespondToPermission(sessionID, optionID string) error {
	return nil
}
func (r *activitySummaryI18nFakeRunner) CancelPermission(sessionID string) {}
func (r *activitySummaryI18nFakeRunner) Shutdown() error                   { return nil }
func (r *activitySummaryI18nFakeRunner) IsReady() bool                     { return true }

func (r *activitySummaryI18nFakeRunner) hasPromptContaining(parts ...string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, prompt := range r.prompts {
		ok := true
		for _, part := range parts {
			if !strings.Contains(prompt, part) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func newActivitySummaryI18nTestApp(t *testing.T, runner *activitySummaryI18nFakeRunner) *App {
	t.Helper()
	stateStore, err := storage.New(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })
	app := &App{
		store:    stateStore,
		runner:   runner,
		activity: &activityMonitorState{store: newTestStore(t)},
	}
	if err := app.SaveUIState(uiLanguageStateKey, "zh-CN"); err != nil {
		t.Fatalf("SaveUIState: %v", err)
	}
	return app
}

func seedActivitySummaryI18nSession(t *testing.T, s *ActivityStore, id, date string, hour int, apps []string) {
	t.Helper()
	day, err := time.ParseInLocation("2006-01-02", date, time.Local)
	if err != nil {
		t.Fatalf("parse date %q: %v", date, err)
	}
	appJSON, err := json.Marshal(apps)
	if err != nil {
		t.Fatalf("marshal apps: %v", err)
	}
	startMs := day.Add(time.Duration(hour) * time.Hour).UnixMilli()
	endMs := startMs + 45*time.Minute.Milliseconds()
	_, err = s.db.Exec(`
		INSERT INTO activity_sessions (
			id, start_time, end_time, trigger_kind, app_names,
			event_count, segment_count, snapshot_count, created_at
		) VALUES (?, ?, ?, 'test', ?, 12, 0, 3, ?)`,
		id, startMs, endMs, string(appJSON), time.Now().UnixMilli(),
	)
	if err != nil {
		t.Fatalf("insert session %s: %v", id, err)
	}
}

func readActivitySummaryLanguage(t *testing.T, s *ActivityStore, date, summaryType string) string {
	t.Helper()
	var language string
	err := s.db.QueryRow(
		`SELECT language FROM activity_summaries WHERE date = ? AND type = ? AND language != ''`,
		date, summaryType,
	).Scan(&language)
	if err != nil {
		t.Fatalf("read cached summary language for %s/%s: %v", date, summaryType, err)
	}
	return language
}

func waitForCachedMoments(t *testing.T, s *ActivityStore, date string) []MomentData {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		raw := s.GetCachedMomentsForLanguage(date, "zh-CN")
		if raw != "" {
			var moments []MomentData
			if err := json.Unmarshal([]byte(raw), &moments); err != nil {
				t.Fatalf("unmarshal cached moments: %v; raw=%s", err, raw)
			}
			return moments
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for cached moments for %s", date)
	return nil
}

func TestGenerateDailySummary_UsesCurrentUILanguageAndCachesLanguage(t *testing.T) {
	runner := &activitySummaryI18nFakeRunner{}
	app := newActivitySummaryI18nTestApp(t, runner)
	const date = "2026-04-20"
	seedActivitySummaryI18nSession(t, app.activity.store, "daily-i18n-1", date, 10, []string{"Code"})

	if err := app.GenerateDailySummary(date); err != nil {
		t.Fatalf("GenerateDailySummary: %v", err)
	}

	if !runner.hasPromptContaining("daily activity digest", "Simplified Chinese") {
		t.Fatalf("daily summary prompt should include current UI language; prompts=%q", runner.prompts)
	}
	if got := readActivitySummaryLanguage(t, app.activity.store, date, "daily"); got != "zh-CN" {
		t.Fatalf("cached daily language = %q, want zh-CN", got)
	}
	data, err := app.GetDailySummary(date)
	if err != nil {
		t.Fatalf("GetDailySummary: %v", err)
	}
	if data.AISummaryStatus != "done" || !strings.Contains(data.AISummary, "国际化") {
		t.Fatalf("GetDailySummary status/summary = %q/%q, want cached Chinese summary", data.AISummaryStatus, data.AISummary)
	}
}

func TestGenerateWeeklySummary_UsesCurrentUILanguageAndCachesLanguage(t *testing.T) {
	runner := &activitySummaryI18nFakeRunner{}
	app := newActivitySummaryI18nTestApp(t, runner)
	const date = "2026-04-22"
	seedActivitySummaryI18nSession(t, app.activity.store, "weekly-i18n-1", date, 11, []string{"Code"})

	if err := app.GenerateWeeklySummary(date); err != nil {
		t.Fatalf("GenerateWeeklySummary: %v", err)
	}

	if !runner.hasPromptContaining("work week", "Simplified Chinese") {
		t.Fatalf("weekly summary prompt should include current UI language; prompts=%q", runner.prompts)
	}
	data, err := app.GetWeeklySummary(date)
	if err != nil {
		t.Fatalf("GetWeeklySummary: %v", err)
	}
	weekKey := data.WeekStart + "/week"
	if got := readActivitySummaryLanguage(t, app.activity.store, weekKey, "weekly"); got != "zh-CN" {
		t.Fatalf("cached weekly language = %q, want zh-CN", got)
	}
	if data.AISummaryStatus != "done" || !strings.Contains(data.AISummary, "本周") {
		t.Fatalf("GetWeeklySummary status/summary = %q/%q, want cached Chinese summary", data.AISummaryStatus, data.AISummary)
	}
}

func TestMoments_UseCurrentUILanguageAndPreserveMachineFields(t *testing.T) {
	runner := &activitySummaryI18nFakeRunner{}
	app := newActivitySummaryI18nTestApp(t, runner)
	const date = "2026-04-20"
	seedActivitySummaryI18nSession(t, app.activity.store, "moment-i18n-1", date, 9, []string{"Code", "Chrome"})
	seedActivitySummaryI18nSession(t, app.activity.store, "moment-i18n-2", date, 10, []string{"Slack"})

	if err := app.GenerateDailySummary(date); err != nil {
		t.Fatalf("GenerateDailySummary: %v", err)
	}
	// Daily summary returns before the independent moments worker finishes.
	// Wait for its persisted result before inspecting the worker's prompt.
	moments := waitForCachedMoments(t, app.activity.store, date)
	if !runner.hasPromptContaining("STRICT JSON", "Simplified Chinese") {
		t.Fatal("moments prompt should include current UI language")
	}
	if len(moments) != 1 {
		t.Fatalf("cached moments len = %d, want 1 (%+v)", len(moments), moments)
	}
	got := moments[0]
	if got.Title != "实现摘要国际化" || got.Subtitle != "覆盖每日和片段摘要" || !strings.Contains(got.Description, "中文输出") {
		t.Fatalf("cached moment text = title %q subtitle %q description %q, want Chinese fields", got.Title, got.Subtitle, got.Description)
	}
	if got.StartMs == 0 || got.EndMs == 0 || got.EndMs <= got.StartMs {
		t.Fatalf("cached moment time fields not preserved: start=%d end=%d", got.StartMs, got.EndMs)
	}
	if strings.Join(got.SessionIDs, ",") != "moment-i18n-1,moment-i18n-2" {
		t.Fatalf("cached moment session IDs = %v, want source session IDs preserved", got.SessionIDs)
	}
	if strings.Join(got.Apps, ",") != "Code,Chrome,Slack" {
		t.Fatalf("cached moment apps = %v, want source apps preserved", got.Apps)
	}

	data, err := app.GetDailySummary(date)
	if err != nil {
		t.Fatalf("GetDailySummary: %v", err)
	}
	if len(data.Moments) != 1 || data.Moments[0].Title != "实现摘要国际化" {
		t.Fatalf("GetDailySummary moments = %+v, want cached Chinese moment returned", data.Moments)
	}
}

// TestSummaryCacheLegacyRowFallsBackForAnyUILanguage pins the new policy
// (replaces the old "legacy = stale" behavior): rows written before the
// language column existed (language=”) must be returned as-is under any UI
// language. Showing the original wording costs nothing; silently retiring it
// and forcing a regen would burn LLM quota without consent every time the
// user switches UI language. Users can still hit Regenerate manually to get
// a localized rewrite.
func TestSummaryCacheLegacyRowFallsBackForAnyUILanguage(t *testing.T) {
	runner := &activitySummaryI18nFakeRunner{}
	app := newActivitySummaryI18nTestApp(t, runner)
	const date = "2026-04-20"
	seedActivitySummaryI18nSession(t, app.activity.store, "daily-cache-lang-1", date, 10, []string{"Code"})

	if _, err := app.activity.store.SaveSummary(date, "daily", "English cached daily summary", "haiku", false); err != nil {
		t.Fatalf("SaveSummary daily: %v", err)
	}
	data, err := app.GetDailySummary(date)
	if err != nil {
		t.Fatalf("GetDailySummary: %v", err)
	}
	if data.AISummaryStatus != "done" || data.AISummary != "English cached daily summary" {
		t.Fatalf("GetDailySummary status/summary = %q/%q, want legacy English cache returned under zh-CN UI",
			data.AISummaryStatus, data.AISummary)
	}

	if _, err := app.activity.store.SaveSummary("2026-04-20/week", "weekly", "English cached weekly summary", "haiku", false); err != nil {
		t.Fatalf("SaveSummary weekly: %v", err)
	}
	weekly, err := app.GetWeeklySummary(date)
	if err != nil {
		t.Fatalf("GetWeeklySummary: %v", err)
	}
	if weekly.AISummaryStatus != "done" || weekly.AISummary != "English cached weekly summary" {
		t.Fatalf("GetWeeklySummary status/summary = %q/%q, want legacy English cache returned under zh-CN UI",
			weekly.AISummaryStatus, weekly.AISummary)
	}
}

// TestListPendingSummaryDates_LegacyRowDoesNotTriggerRegen guards the second
// half of the same policy: checkPendingSummaries must not treat a legacy
// (language=”) row as pending just because the active UI language differs.
// Without this guard, every wails-gui startup under zh-CN would re-spend LLM
// quota on the last 7 days of historical English summaries.
func TestListPendingSummaryDates_LegacyRowDoesNotTriggerRegen(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	d := now.AddDate(0, 0, -1).Format("2006-01-02")
	seedActivityForDate(t, s, d)

	// SaveSummary writes language='' — exactly the legacy shape that pre-PR
	// #1736 rows have.
	if _, err := s.SaveSummary(d, "daily", "English legacy summary", "haiku", false); err != nil {
		t.Fatalf("SaveSummary: %v", err)
	}
	// Mimic Moments having succeeded on the original run so moments_partial=0.
	_, gen, ok := s.GetMomentsPartial(d)
	if !ok {
		t.Fatalf("GetMomentsPartial: row missing right after SaveSummary")
	}
	if err := s.SaveMoments(d, `[{"title":"legacy"}]`, gen); err != nil {
		t.Fatalf("SaveMoments: %v", err)
	}

	pending := s.ListPendingSummaryDatesForLanguage(7, "zh-CN")
	for _, got := range pending {
		if got == d {
			t.Fatalf("legacy row should not be pending under zh-CN UI; got %v", pending)
		}
	}
}

// TestSummaryCacheFallsBackToOtherLanguageRow extends the no-silent-regen
// policy past legacy ”-language rows: a fully tagged row in some OTHER
// language (e.g. an English summary written after the language column existed)
// must also be returned under a different UI language. The alternative is
// startup regen on every language switch even for non-legacy rows.
func TestSummaryCacheFallsBackToOtherLanguageRow(t *testing.T) {
	runner := &activitySummaryI18nFakeRunner{}
	app := newActivitySummaryI18nTestApp(t, runner)
	const date = "2026-04-20"
	seedActivitySummaryI18nSession(t, app.activity.store, "daily-cross-lang-1", date, 10, []string{"Code"})

	if _, err := app.activity.store.SaveSummaryWithLanguage(date, "daily", "English tagged daily summary", "haiku", false, "en"); err != nil {
		t.Fatalf("SaveSummaryWithLanguage en: %v", err)
	}
	data, err := app.GetDailySummary(date)
	if err != nil {
		t.Fatalf("GetDailySummary: %v", err)
	}
	if data.AISummaryStatus != "done" || data.AISummary != "English tagged daily summary" {
		t.Fatalf("GetDailySummary status/summary = %q/%q, want English tagged row returned under zh-CN UI",
			data.AISummaryStatus, data.AISummary)
	}
	if data.AISummaryLanguage != "en" {
		t.Fatalf("GetDailySummary AISummaryLanguage = %q, want %q so the UI can show a localized-rewrite hint",
			data.AISummaryLanguage, "en")
	}

	if _, err := app.activity.store.SaveSummaryWithLanguage("2026-04-20/week", "weekly", "English tagged weekly summary", "haiku", false, "en"); err != nil {
		t.Fatalf("SaveSummaryWithLanguage weekly en: %v", err)
	}
	weekly, err := app.GetWeeklySummary(date)
	if err != nil {
		t.Fatalf("GetWeeklySummary: %v", err)
	}
	if weekly.AISummaryStatus != "done" || weekly.AISummary != "English tagged weekly summary" {
		t.Fatalf("GetWeeklySummary status/summary = %q/%q, want English tagged weekly returned under zh-CN UI",
			weekly.AISummaryStatus, weekly.AISummary)
	}
	if weekly.AISummaryLanguage != "en" {
		t.Fatalf("GetWeeklySummary AISummaryLanguage = %q, want %q so the UI can show a localized-rewrite hint",
			weekly.AISummaryLanguage, "en")
	}
}

// TestGetDailySummary_LegacyRowSurfacesEmptyLanguage pins the contract for the
// other side of the hint: legacy rows (language=”) must surface that empty
// string up through DailySummaryData so the frontend can treat them as English
// (its practical reality) without us overstating what we actually know about
// the row.
func TestGetDailySummary_LegacyRowSurfacesEmptyLanguage(t *testing.T) {
	runner := &activitySummaryI18nFakeRunner{}
	app := newActivitySummaryI18nTestApp(t, runner)
	const date = "2026-04-20"
	seedActivitySummaryI18nSession(t, app.activity.store, "legacy-lang-1", date, 10, []string{"Code"})

	// SaveSummary (no language variant) writes language='' — the legacy shape.
	if _, err := app.activity.store.SaveSummary(date, "daily", "Legacy daily summary", "haiku", false); err != nil {
		t.Fatalf("SaveSummary: %v", err)
	}
	data, err := app.GetDailySummary(date)
	if err != nil {
		t.Fatalf("GetDailySummary: %v", err)
	}
	if data.AISummaryStatus != "done" || data.AISummary != "Legacy daily summary" {
		t.Fatalf("GetDailySummary status/summary = %q/%q, want legacy row returned",
			data.AISummaryStatus, data.AISummary)
	}
	if data.AISummaryLanguage != "" {
		t.Fatalf("GetDailySummary AISummaryLanguage = %q, want empty string so the UI knows the row predates the language column",
			data.AISummaryLanguage)
	}
}

// TestListPendingSummaryDates_OtherLanguageRowDoesNotTriggerRegen pins the
// pending-scan half of the cross-language policy: a final row in some other
// language must not mark the date as pending. This is the case that triggered
// the original bug — startup under zh-CN re-spent quota on days whose en
// summaries were already complete.
func TestListPendingSummaryDates_OtherLanguageRowDoesNotTriggerRegen(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	d := now.AddDate(0, 0, -1).Format("2006-01-02")
	seedActivityForDate(t, s, d)

	gen, err := s.SaveSummaryWithLanguage(d, "daily", "English tagged summary", "haiku", false, "en")
	if err != nil {
		t.Fatalf("SaveSummaryWithLanguage: %v", err)
	}
	if err := s.SaveMomentsWithLanguage(d, `[{"title":"English moment"}]`, gen, "en"); err != nil {
		t.Fatalf("SaveMomentsWithLanguage: %v", err)
	}

	pending := s.ListPendingSummaryDatesForLanguage(7, "zh-CN")
	for _, got := range pending {
		if got == d {
			t.Fatalf("date with final English row should not be pending under zh-CN UI; got %v", pending)
		}
	}
}

func TestSummaryCacheStoresLanguagesIndependently(t *testing.T) {
	runner := &activitySummaryI18nFakeRunner{}
	app := newActivitySummaryI18nTestApp(t, runner)
	const date = "2026-04-20"
	seedActivitySummaryI18nSession(t, app.activity.store, "multi-lang-cache-1", date, 10, []string{"Code"})

	enGen, err := app.activity.store.SaveSummaryWithLanguage(date, "daily", "English cached daily summary", "haiku", false, "en")
	if err != nil {
		t.Fatalf("SaveSummaryWithLanguage en: %v", err)
	}
	if err := app.activity.store.SaveMomentsWithLanguage(date, `[{"title":"English moment"}]`, enGen, "en"); err != nil {
		t.Fatalf("SaveMomentsWithLanguage en: %v", err)
	}
	zhGen, err := app.activity.store.SaveSummaryWithLanguage(date, "daily", "中文每日摘要", "haiku", false, "zh-CN")
	if err != nil {
		t.Fatalf("SaveSummaryWithLanguage zh-CN: %v", err)
	}
	if err := app.activity.store.SaveMomentsWithLanguage(date, `[{"title":"中文片段"}]`, zhGen, "zh-CN"); err != nil {
		t.Fatalf("SaveMomentsWithLanguage zh-CN: %v", err)
	}

	if got, _, _, lang, found := app.activity.store.GetCachedSummaryForLanguage(date, "daily", "zh-CN"); !found || got != "中文每日摘要" || lang != "zh-CN" {
		t.Fatalf("Chinese cache = found %t summary %q language %q, want Chinese row", found, got, lang)
	}
	if got, _, _, lang, found := app.activity.store.GetCachedSummaryForLanguage(date, "daily", "en"); !found || got != "English cached daily summary" || lang != "en" {
		t.Fatalf("English cache = found %t summary %q language %q, want English row", found, got, lang)
	}
	if raw := app.activity.store.GetCachedMomentsForLanguage(date, "zh-CN"); !strings.Contains(raw, "中文片段") || strings.Contains(raw, "English moment") {
		t.Fatalf("Chinese moments cache = %q, want only Chinese moment", raw)
	}
	if raw := app.activity.store.GetCachedMomentsForLanguage(date, "en"); !strings.Contains(raw, "English moment") || strings.Contains(raw, "中文片段") {
		t.Fatalf("English moments cache = %q, want only English moment", raw)
	}
}

func TestSummaryI18nNormalizationFailureFailOpen(t *testing.T) {
	runner := &activitySummaryI18nFakeRunner{}
	app := newActivitySummaryI18nTestApp(t, runner)
	const date = "2026-04-20"
	seedActivitySummaryI18nSession(t, app.activity.store, "fail-open-1", date, 9, []string{"Code"})
	seedActivitySummaryI18nSession(t, app.activity.store, "fail-open-2", date, 10, []string{"Chrome"})

	if err := app.GenerateDailySummary(date); err != nil {
		t.Fatalf("GenerateDailySummary should fail open on normalization errors: %v", err)
	}
	data, err := app.GetDailySummary(date)
	if err != nil {
		t.Fatalf("GetDailySummary: %v", err)
	}
	if data.AISummaryStatus != "done" || strings.TrimSpace(data.AISummary) == "" {
		t.Fatalf("daily summary was not saved after normalization failure: status=%q summary=%q", data.AISummaryStatus, data.AISummary)
	}
	moments := waitForCachedMoments(t, app.activity.store, date)
	if len(moments) != 1 || strings.TrimSpace(moments[0].Title) == "" {
		t.Fatalf("moments were not saved after normalization failure: %+v", moments)
	}
}
