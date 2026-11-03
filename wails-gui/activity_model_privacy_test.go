package main

import (
	"context"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/pii"
	"wails-gui/internal/platform/activitymon"
	"yansu-agent/agent"
)

func TestDailySummarySourcesRedactAndExcludeProtectedSessions(t *testing.T) {
	isolateActivityPrivacyTest(t)
	store := newTimelineTestStore(t)
	app := &App{activity: &activityMonitorState{store: store}}
	var sessions []ActivitySessionRow
	for _, name := range []string{"normal", "sensitive", "protected"} {
		s := ActivitySessionRow{ID: name, StartTime: 1000, EndTime: 2000, AppNames: `["Notes"]`, TriageStatus: "done"}
		bundle, appName := "com.apple.Notes", "Notes"
		text := "Public context token " + activityPrivacySecret
		if name != "normal" {
			text = "excluded-" + name + "-marker-content"
		}
		if name == "sensitive" {
			bundle, appName, s.AppNames = "com.example.private", "Private Notes", `["Private Notes"]`
		}
		if name == "protected" {
			s.TriageStatus = "protected"
		}
		if err := store.SaveSession(s, []activitymon.ActivityEvent{
			{Kind: activitymon.EventSpeech, AppBundleID: bundle, AppName: appName, SpeechText: text},
			{Kind: activitymon.EventClick, AppBundleID: bundle, AppName: appName, ClickLabel: text},
			{Kind: activitymon.EventFocus, AppBundleID: bundle, AppName: appName, WindowTitle: text},
		}, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.Exec(`INSERT INTO activity_ocr_frames (session_id, segment_id, frame_time, text, confidence) VALUES (?, 0, 0, ?, 1)`, s.ID, text); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "transcript.txt")
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		s.IsMeeting, s.TranscriptPath, s.MeetingApp = true, path, appName
		if err := store.UpdateMeetingStatus(s.ID, appName, path); err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, s)
	}
	for name, load := range map[string]func([]ActivitySessionRow) string{
		"transcript": app.loadDailyMeetings,
		"ocr":        app.loadDailyOCRSamples,
		"click":      app.loadDailyClickHighlights,
		"speech":     app.loadDailySpeech,
	} {
		t.Run(name, func(t *testing.T) {
			rows := append([]ActivitySessionRow(nil), sessions...)
			if name == "speech" {
				for i := range rows {
					rows[i].IsMeeting = false
				}
			}
			got := load(rows)
			if strings.Contains(got, activityPrivacySecret) || strings.Contains(got, "excluded-") {
				t.Fatalf("private source reached summary context: %s", got)
			}
			if !strings.Contains(got, "Public context") || !strings.Contains(got, "[REDACTED:") {
				t.Fatalf("expected useful redacted context: %s", got)
			}
		})
	}
	for name, got := range map[string]string{
		"keywords": strings.Join(app.extractTopOCRKeywords(store, 0, 3000), " "),
		"titles":   strings.Join(app.extractTopWindowTitles(store, 0, 3000), " "),
	} {
		if strings.Contains(got, "sk-proj-") || strings.Contains(got, "excluded-") {
			t.Errorf("private %s entered summary: %s", name, got)
		}
	}
}

func TestTimelineSummaryPromptRedactsBeforeTruncationAndFiltersApps(t *testing.T) {
	isolateActivityPrivacyTest(t)
	group := timelineSummaryTestGroup()
	group.Events = []ActivityTimelineEvent{
		{EventID: "ae_safe", ActivityEvent: activitymon.ActivityEvent{Kind: activitymon.EventSpeech, SpeechText: strings.Repeat("x", 220) + " " + activityPrivacySecret}},
		{EventID: "ae_private", ActivityEvent: activitymon.ActivityEvent{Kind: activitymon.EventSpeech, AppBundleID: "com.example.private", SpeechText: "excluded-sensitive"}},
	}
	prompt := buildTimelineGroupSummaryPrompt(group, "OCR "+activityPrivacySecret, "")
	if strings.Contains(prompt, "sk-proj-") || strings.Contains(prompt, "excluded-sensitive") || strings.Contains(prompt, "ae_private") {
		t.Fatalf("unredacted or protected evidence in title prompt: %s", prompt)
	}
	if !strings.Contains(prompt, "ae_safe") || !strings.Contains(prompt, "[REDACTED:") {
		t.Fatalf("missing safe evidence in title prompt: %s", prompt)
	}
	screen := renderTimelineScreenContent([]Observation{{Title: activityPrivacySecret, Body: activityPrivacySecret}, {RealApp: "Private Notes", Body: "excluded-screen"}})
	if strings.Contains(screen, "sk-proj-") || strings.Contains(screen, "excluded-screen") {
		t.Fatalf("private screen observation: %s", screen)
	}
}

func TestDailySummaryDerivedSourcesRespectCurrentPrivacy(t *testing.T) {
	isolateActivityPrivacyTest(t)
	app := &App{activity: &activityMonitorState{store: newTimelineTestStore(t)}}
	for _, directory := range []func() (string, error){memoryDir, knowledgeDir} {
		dir, err := directory()
		if err != nil {
			t.Fatal(err)
		}
		for name, content := range map[string]string{
			"safe.md":    "# Safe context\n\n- **Date**: 2026-04-20\n- **Apps**: Notes\n\nPublic context " + activityPrivacySecret,
			"private.md": "# Private context\n\n- **Date**: 2026-04-20\n- **Apps**: Private Notes\n\nexcluded-private-derived-context",
		} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	for kind, got := range map[string]string{
		"memory":    app.loadDailyMemories(0, time.Now().Add(time.Hour).UnixMilli()),
		"knowledge": app.loadDailyKnowledge("2026-04-20"),
	} {
		if strings.Contains(got, "excluded-") || strings.Contains(got, activityPrivacySecret) || !strings.Contains(got, "Public context") {
			t.Fatalf("private or missing %s context: %s", kind, got)
		}
	}
}

func TestTimelineSummaryNeverAttachesUnmaskedCrops(t *testing.T) {
	isolateActivityPrivacyTest(t)
	app := &App{activity: &activityMonitorState{store: newTimelineTestStore(t)}}
	path := filepath.Join(t.TempDir(), "private.jpg")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(f, image.NewRGBA(image.Rect(0, 0, 100, 100)), nil); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	snap := ActivitySnapshotRow{FilePath: path, FocusRectW: 1, FocusRectH: 1}
	if got := app.timelineGroupSummaryImages([]ActivitySnapshotRow{snap}, 8); len(got) != 0 {
		t.Fatalf("title generation attached %d unmasked screen crops", len(got))
	}
}

func TestComposerPromptRedactsAndExcludesSensitiveCandidates(t *testing.T) {
	isolateActivityPrivacyTest(t)
	candidates := []ActivityRow{
		{ID: "safe", Title: activityPrivacySecret, Description: strings.Repeat("x", 225) + " " + activityPrivacySecret, ActiveMsByApp: `{"Notes":1000}`},
		{ID: "private", Title: "excluded-candidate", ActiveMsByApp: `{"Private Notes":1000}`},
	}
	prompt := buildComposerPrompt(candidates, ActivityTimelineRunRow{AppName: "Notes"}, ActivityTimelineGroupSummary{Title: activityPrivacySecret}, composerTransition{PrevApp: "Private Notes"}, "")
	if strings.Contains(prompt, "sk-proj-") || strings.Contains(prompt, "excluded-candidate") || strings.Contains(prompt, "Private Notes") {
		t.Fatalf("private composer evidence: %s", prompt)
	}
	if !strings.Contains(prompt, "id=safe") || !strings.Contains(prompt, "[REDACTED:") {
		t.Fatalf("missing useful redacted composer evidence: %s", prompt)
	}
	if got := buildComposerPrompt(nil, ActivityTimelineRunRow{AppKey: "com.1password.desktop"}, ActivityTimelineGroupSummary{Title: "protected-run-content"}, composerTransition{}, ""); got != "" {
		t.Fatal("protected run produced a composer prompt")
	}
}

type activityPrivacyTaskRecorder struct {
	activitySummaryI18nFakeRunner
	inputs []agent.TaskInput
}

func (r *activityPrivacyTaskRecorder) ExecuteTask(_ context.Context, input *agent.TaskInput, cb agent.StreamCallback) (string, error) {
	r.inputs = append(r.inputs, *input)
	return "test", cb(&agent.StreamEvent{Type: "assistant", Content: `{}`})
}

func TestActivityComposerAndTitlesUseExpectedBackgroundModels(t *testing.T) {
	isolateActivityPrivacyTest(t)
	runner := &activityPrivacyTaskRecorder{}
	app := &App{runner: runner}
	triager := &ActivityTriager{app: app}
	if _, err := triager.runComposerPrompt(context.Background(), "composer "+activityPrivacySecret); err != nil {
		t.Fatal(err)
	}
	if _, err := app.runTimelineGroupSummaryPrompt(context.Background(), "title "+activityPrivacySecret, []agent.ImageAttachment{{MediaType: "image/jpeg", DataBase64: "raw-screen"}}, app.lightTaskModel()); err != nil {
		t.Fatal(err)
	}
	if len(runner.inputs) != 2 {
		t.Fatalf("model calls = %d, want composer and title", len(runner.inputs))
	}
	for i, input := range runner.inputs {
		wantModel := []string{app.chatTaskModel(), app.lightTaskModel()}[i]
		if input.Priority != agent.PriorityBackground || input.Model != wantModel || strings.Contains(input.Prompt, activityPrivacySecret) || len(input.Images) != 0 {
			t.Fatalf("unsafe activity task input: %+v", input)
		}
	}
}

func TestActivityModelsRecheckHistoricalSessionProtection(t *testing.T) {
	isolateActivityPrivacyTest(t)
	for _, kind := range []string{"timeline", "composer"} {
		t.Run(kind, func(t *testing.T) {
			store := newTimelineTestStore(t)
			runner := &activityPrivacyTaskRecorder{}
			app := &App{runner: runner, activity: &activityMonitorState{store: store}}
			session := ActivitySessionRow{ID: "historical", StartTime: 1000, EndTime: 61_000, AppNames: `["Notes","Private Notes"]`, TriageStatus: "done"}
			if err := store.SaveSession(session, []activitymon.ActivityEvent{{Kind: activitymon.EventSpeech, AppName: "Notes", SpeechText: "protected historical speech", Timestamp: 2000}}, nil); err != nil {
				t.Fatal(err)
			}
			rows, err := store.GetSessionEventRows(session.ID)
			if err != nil || len(rows) != 1 {
				t.Fatalf("load source event: %v, rows=%d", err, len(rows))
			}
			run := testRun("tg_historical", "Notes", 1000, 61_000, 60_000, 1)
			run.SessionIDs = []string{session.ID}
			run.FirstEventRowID, run.LastEventRowID, run.EventCount = rows[0].ID, rows[0].ID, 1
			insertComposableTimelineRun(t, store, run, ActivityTimelineGroupSummary{Title: "historical private summary"})
			switch kind {
			case "timeline":
				_, _ = app.generateTimelineRunSummary(context.Background(), run)
			case "composer":
				triager := &ActivityTriager{app: app}
				_, _ = triager.composeRun(context.Background(), run, 62_000)
			}
			if len(runner.inputs) != 0 {
				t.Fatalf("%s sent historical protected session to model", kind)
			}
		})
	}
}

func TestDailyDigestGeneratesInUILanguageWithoutTranslationPasses(t *testing.T) {
	isolateActivityPrivacyTest(t)
	runner := &activitySummaryI18nFakeRunner{}
	app := newActivitySummaryI18nTestApp(t, runner)
	const date = "2026-04-20"
	seedActivitySummaryI18nSession(t, app.activity.store, "digest-one", date, 9, []string{"Code"})
	seedActivitySummaryI18nSession(t, app.activity.store, "digest-two", date, 10, []string{"Notes"})
	if err := app.GenerateDailySummary(date); err != nil {
		t.Fatal(err)
	}
	_ = waitForCachedMoments(t, app.activity.store, date)
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.calls != 2 {
		t.Fatalf("daily digest made %d calls, want summary and moments once each", runner.calls)
	}
	for _, prompt := range runner.prompts {
		if !strings.Contains(prompt, "Simplified Chinese") {
			t.Fatal("initial generation missed UI language")
		}
	}
}

func TestWeeklySummaryExcludesProtectedDailyCache(t *testing.T) {
	isolateActivityPrivacyTest(t)
	app := &App{activity: &activityMonitorState{store: newTimelineTestStore(t)}}
	const date = "2026-04-20"
	seedActivitySummaryI18nSession(t, app.activity.store, "weekly-safe", date, 9, []string{"Notes"})
	if _, err := app.activity.store.SaveSummary(date, "daily", "historical cached private narrative", "test", false); err != nil {
		t.Fatal(err)
	}
	data, err := app.GetWeeklySummary(date)
	if err != nil || len(data.dailySummaries) != 1 {
		t.Fatalf("safe cached summary unavailable: %v", err)
	}
	pii.AddUserSensitiveApp("com.apple.Notes", "Notes")
	data, err = app.GetWeeklySummary(date)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buildWeeklySummaryPrompt(data, ""), "historical cached private narrative") {
		t.Fatal("weekly model prompt included a now-protected daily cache")
	}
}

func TestRecentActivityCacheRechecksPrivacyBeforePromptInjection(t *testing.T) {
	isolateActivityPrivacyTest(t)
	store := newTimelineTestStore(t)
	app := &App{activity: &activityMonitorState{store: store}}
	now := time.Now()
	session := ActivitySessionRow{ID: "cached-source", StartTime: now.Add(-time.Hour).UnixMilli(), EndTime: now.UnixMilli(), AppNames: `["Notes"]`, TriageStatus: "done"}
	if err := store.SaveSession(session, nil, nil); err != nil {
		t.Fatal(err)
	}
	cache := newActivityContextCache()
	cache.summary, cache.generatedAt = "historical cached note "+activityPrivacySecret, now
	cache.privacy, _ = app.activityContextPrivacy()
	got := cache.getSummary(app)
	if !strings.Contains(got, "historical cached note") || strings.Contains(got, activityPrivacySecret) {
		t.Fatalf("safe cached note missing or unredacted: %s", got)
	}
	cache.mu.Lock()
	cache.generating = true
	cache.mu.Unlock()
	pii.AddUserSensitiveApp("com.apple.Notes", "Notes")
	if got := cache.getSummary(app); got != "" {
		t.Fatalf("now-protected cached note reached chat: %s", got)
	}
	pii.RemoveUserSensitiveApp("com.apple.Notes")
	app.closedEyes.enabled.Store(true)
	if got := cache.getSummary(app); got != "" {
		t.Fatalf("closed-eyes cached note reached chat: %s", got)
	}
}

type activityDeletingSummaryRunner struct {
	activitySummaryI18nFakeRunner
	store       *ActivityStore
	sessionID   string
	momentsDone chan struct{}
	deleteErr   error
}

func (r *activityDeletingSummaryRunner) ExecuteTask(ctx context.Context, input *agent.TaskInput, cb agent.StreamCallback) (string, error) {
	if strings.Contains(input.Prompt, "STRICT JSON") && strings.Contains(input.Prompt, `"moments"`) {
		id, err := r.activitySummaryI18nFakeRunner.ExecuteTask(ctx, input, cb)
		close(r.momentsDone)
		return id, err
	}
	if strings.Contains(input.Prompt, "daily activity digest") || strings.Contains(input.Prompt, "work week") {
		if r.momentsDone != nil {
			select {
			case <-r.momentsDone:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		r.deleteErr = r.store.DeleteSession(r.sessionID)
		if r.deleteErr != nil {
			return "", r.deleteErr
		}
	}
	return r.activitySummaryI18nFakeRunner.ExecuteTask(ctx, input, cb)
}

func TestDigestGenerationDoesNotRestoreDeletedSourceCache(t *testing.T) {
	isolateActivityPrivacyTest(t)
	for _, kind := range []string{"daily", "weekly"} {
		t.Run(kind, func(t *testing.T) {
			const date = "2026-04-20"
			runner := &activityDeletingSummaryRunner{sessionID: "deleted-source"}
			app := newActivitySummaryI18nTestApp(t, &runner.activitySummaryI18nFakeRunner)
			app.runner = runner
			runner.store = app.activity.store
			seedActivitySummaryI18nSession(t, runner.store, runner.sessionID, date, 9, []string{"Notes"})
			seedActivitySummaryI18nSession(t, runner.store, "remaining-source", date, 10, []string{"Code"})
			var err error
			cacheKey := date
			switch kind {
			case "daily":
				runner.momentsDone = make(chan struct{})
				err = app.GenerateDailySummary(date)
			case "weekly":
				cacheKey += "/week"
				err = app.GenerateWeeklySummary(date)
			}
			if runner.deleteErr != nil {
				t.Fatal(runner.deleteErr)
			}
			if err == nil || !strings.Contains(err.Error(), "activity source changed") {
				t.Fatalf("generation did not reject changed source: %v", err)
			}
			if _, _, _, _, found := runner.store.GetCachedSummaryForLanguage(cacheKey, kind, "zh-CN"); found {
				t.Fatal("in-flight generation restored deleted source summary")
			}
		})
	}
}

func TestLateMomentsCannotWriteAfterSourceDeletionAndRegeneration(t *testing.T) {
	isolateActivityPrivacyTest(t)
	const date = "2026-04-20"
	store := newTimelineTestStore(t)
	app := &App{activity: &activityMonitorState{store: store}}
	seedActivitySummaryI18nSession(t, store, "deleted-source", date, 9, []string{"Notes"})
	sources, err := app.captureSummarySources(date, "daily")
	if err != nil {
		t.Fatal(err)
	}
	oldGeneration, err := store.SaveSummaryWithLanguage(date, "daily", "Original summary", "test", false, "en")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteSession("deleted-source"); err != nil {
		t.Fatal(err)
	}
	newGeneration, err := store.SaveSummaryWithLanguage(date, "daily", "Replacement summary", "test", false, "en")
	if err != nil {
		t.Fatal(err)
	}
	if newGeneration != oldGeneration {
		t.Fatal("fixture does not exercise generation reuse after row deletion")
	}
	ch := make(chan []MomentData, 1)
	ch <- []MomentData{{Title: "Deleted source moment", SessionIDs: []string{"deleted-source"}}}
	app.persistMomentsWithSources(date, oldGeneration, "en", ch, sources)
	if got := store.GetCachedMomentsForLanguage(date, "en"); got != "" {
		t.Fatalf("stale moments contaminated replacement summary: %s", got)
	}
}
