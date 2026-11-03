package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/platform/activitymon"
	"wails-gui/storage"

	"yansu-agent/agent"
)

type activityMarkdownTranslationFakeRunner struct {
	response   string
	responses  []string
	lastPrompt string
	calls      int
}

func (r *activityMarkdownTranslationFakeRunner) Init(ctx context.Context) error { return nil }
func (r *activityMarkdownTranslationFakeRunner) ExecuteTask(ctx context.Context, input *agent.TaskInput, callback agent.StreamCallback) (string, error) {
	r.calls++
	r.lastPrompt = input.Prompt
	response := r.response
	if len(r.responses) > 0 {
		response = r.responses[0]
		r.responses = r.responses[1:]
	}
	if err := callback(&agent.StreamEvent{Type: "assistant", Content: response}); err != nil {
		return "", err
	}
	return "run-test", nil
}
func (r *activityMarkdownTranslationFakeRunner) CancelRun(runID string) error { return nil }
func (r *activityMarkdownTranslationFakeRunner) RespondToPermission(sessionID, optionID string) error {
	return nil
}
func (r *activityMarkdownTranslationFakeRunner) CancelPermission(sessionID string) {}
func (r *activityMarkdownTranslationFakeRunner) Shutdown() error                   { return nil }
func (r *activityMarkdownTranslationFakeRunner) IsReady() bool                     { return true }

func newActivityTranslationTestApp(t *testing.T, runner *activityMarkdownTranslationFakeRunner) *App {
	t.Helper()
	store, err := storage.New(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	app := &App{store: store, runner: runner}
	if err := app.SaveUIState("ui.language", "zh-CN"); err != nil {
		t.Fatalf("SaveUIState: %v", err)
	}
	return app
}

func TestShouldEvaluateGroupReason_AllowsSpeechOnlyGroupWithoutApps(t *testing.T) {
	c := &ActivityTriager{}
	group := sessionGroup{
		Sessions: []ActivitySessionRow{
			{
				ID:          "speech-session",
				TriggerKind: "speech_idle_transition",
				AppNames:    "[]",
				EventCount:  53,
				StartTime:   0,
				EndTime:     4 * 60 * 1000,
			},
		},
	}

	if reason := c.shouldEvaluateGroupReason(group); reason != "" {
		t.Fatalf("shouldEvaluateGroupReason() = %q, want empty reason", reason)
	}
}

func TestShouldEvaluateGroupReason_StillRequiresAppsForNonSpeechGroup(t *testing.T) {
	c := &ActivityTriager{}
	// Below the high-activity exemption (events=53 < 60) so the single-app
	// rejection path still fires; we only assert on the stable prefix because
	// the suffix carries diagnostic counts/thresholds that may evolve.
	group := sessionGroup{
		Sessions: []ActivitySessionRow{
			{
				ID:          "interactive-session",
				TriggerKind: "idle_transition",
				AppNames:    "[]",
				EventCount:  53,
				StartTime:   0,
				EndTime:     4 * 60 * 1000,
			},
		},
	}

	const wantPrefix = "too few apps: 0 < 2"
	reason := c.shouldEvaluateGroupReason(group)
	if !strings.HasPrefix(reason, wantPrefix) {
		t.Fatalf("shouldEvaluateGroupReason() = %q, want prefix %q", reason, wantPrefix)
	}
	// The diagnostic suffix should reference the high-activity exemption so
	// the operator immediately understands why the rejection still fires.
	if !strings.Contains(reason, "high-activity exemption") {
		t.Fatalf("shouldEvaluateGroupReason() = %q, want suffix mentioning %q", reason, "high-activity exemption")
	}
}

func TestShouldEvaluateGroupReason_HighActivityExemptionSingleApp(t *testing.T) {
	c := &ActivityTriager{}
	// Single app + non-speech group that *does* clear the high-activity
	// exemption (events >= 60, duration >= 2 minutes). It should pass through
	// to AI triage instead of being rejected for app diversity.
	group := sessionGroup{
		Sessions: []ActivitySessionRow{
			{
				ID:          "deep-work-session",
				TriggerKind: "idle_transition",
				AppNames:    `["cmux"]`,
				EventCount:  120,
				StartTime:   0,
				EndTime:     5 * 60 * 1000,
			},
		},
	}

	if reason := c.shouldEvaluateGroupReason(group); reason != "" {
		t.Fatalf("shouldEvaluateGroupReason() = %q, want empty reason (high-activity exemption)", reason)
	}
}

func TestFormatActivityAppNames_FallsBackToBackgroundAudio(t *testing.T) {
	if got := formatActivityAppNames(nil); got != "Background audio" {
		t.Fatalf("formatActivityAppNames(nil) = %q, want %q", got, "Background audio")
	}
}

// TestClassifyKnowledgeResponse exercises the decision that gates K-P3
// telemetry writeback. The critical case is dedup_skip_empty_points: the
// generation prompt requires {"action":"skip","knowledgePoints":[]} for the
// dedup path, so if we ever check empty-points before the action the telemetry
// collapses "dedup suppressed duplicate" into "AI returned no knowledge
// points", which is exactly the signal K-P3 was added to separate.
func TestClassifyKnowledgeResponse(t *testing.T) {
	tests := []struct {
		name             string
		input            *knowledgeGenerationResponse
		wantAction       string
		wantOutcomeNil   bool
		wantStatus       string
		wantReason       string
		wantTriageEvent  string
		wantTriageDetail string
	}{
		{
			// Regression guard for the bug this helper was extracted to fix:
			// dedup skip must be classified as dedup_skip, NOT as "AI returned
			// no knowledge points", even though knowledgePoints is empty.
			name: "dedup_skip_empty_points_is_dedup_skip_not_empty_response",
			input: &knowledgeGenerationResponse{
				Action:          "skip",
				Title:           "Already known pattern",
				KnowledgePoints: []string{},
			},
			wantAction:       "skip",
			wantOutcomeNil:   false,
			wantStatus:       "skipped",
			wantReason:       `dedup_skip: title_bytes=21`,
			wantTriageEvent:  "dedup_skip",
			wantTriageDetail: `title_bytes=21`,
		},
		{
			// If the model returns action=skip with a title AND some points
			// (prompt non-compliant but possible), we still treat it as dedup
			// because the action field is explicit intent. Points are ignored.
			name: "dedup_skip_with_extraneous_points_still_classified_as_skip",
			input: &knowledgeGenerationResponse{
				Action:          "skip",
				Title:           "Duplicate",
				KnowledgePoints: []string{"this should be ignored"},
			},
			wantAction:      "skip",
			wantOutcomeNil:  false,
			wantStatus:      "skipped",
			wantReason:      `dedup_skip: title_bytes=9`,
			wantTriageEvent: "dedup_skip",
		},
		{
			name: "non_skip_empty_points_is_prompt_noncompliant",
			input: &knowledgeGenerationResponse{
				Action:          "create",
				KnowledgePoints: []string{},
			},
			wantAction:       "create",
			wantOutcomeNil:   false,
			wantStatus:       "skipped",
			wantReason:       "AI returned no knowledge points",
			wantTriageEvent:  "skip",
			wantTriageDetail: "AI returned no knowledge points",
		},
		{
			// Missing action defaults to "create" for backwards compatibility.
			// Empty points under the default must still be classified as the
			// prompt-noncompliant empty path, not as dedup_skip.
			name: "missing_action_defaults_to_create_then_empty_path",
			input: &knowledgeGenerationResponse{
				Action:          "",
				KnowledgePoints: nil,
			},
			wantAction:      "create",
			wantOutcomeNil:  false,
			wantStatus:      "skipped",
			wantReason:      "AI returned no knowledge points",
			wantTriageEvent: "skip",
		},
		{
			name: "create_with_points_is_non_terminal",
			input: &knowledgeGenerationResponse{
				Action:          "create",
				KnowledgePoints: []string{"a point"},
			},
			wantAction:     "create",
			wantOutcomeNil: true,
		},
		{
			name: "update_with_points_is_non_terminal",
			input: &knowledgeGenerationResponse{
				Action:          "update",
				UpdateID:        "activity-abc",
				KnowledgePoints: []string{"refined point"},
			},
			wantAction:     "update",
			wantOutcomeNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			action, outcome := classifyKnowledgeResponse(tc.input)

			if action != tc.wantAction {
				t.Errorf("action = %q, want %q", action, tc.wantAction)
			}
			if tc.wantOutcomeNil {
				if outcome != nil {
					t.Fatalf("outcome = %+v, want nil (non-terminal path)", outcome)
				}
				return
			}
			if outcome == nil {
				t.Fatalf("outcome = nil, want non-nil terminal outcome")
			}
			if outcome.status != tc.wantStatus {
				t.Errorf("outcome.status = %q, want %q", outcome.status, tc.wantStatus)
			}
			if outcome.reason != tc.wantReason {
				t.Errorf("outcome.reason = %q, want %q", outcome.reason, tc.wantReason)
			}
			if outcome.triageEvent != tc.wantTriageEvent {
				t.Errorf("outcome.triageEvent = %q, want %q", outcome.triageEvent, tc.wantTriageEvent)
			}
			if tc.wantTriageDetail != "" && outcome.triageDetail != tc.wantTriageDetail {
				t.Errorf("outcome.triageDetail = %q, want %q", outcome.triageDetail, tc.wantTriageDetail)
			}
		})
	}
}

// TestClassifyKnowledgeResponse_WritebackRoundtrip exercises the full
// writeback path for the dedup_skip + empty-knowledgePoints shape against a
// real ActivityStore. This is the scenario the user flagged: it catches any
// future regression where the classifier and the telemetry writer drift apart
// (e.g. column renames or status-string churn).
func TestClassifyKnowledgeResponse_WritebackRoundtrip(t *testing.T) {
	store := newTestStore(t)

	// StartedAt must be recent: SaveActivityPipelineRun prunes rows older than
	// activityPipelineMaxAge (7 days). A StartedAt of 0 or 1 would immediately
	// delete the row we just inserted.
	runID, err := store.SaveActivityPipelineRun(ActivityPipelineRunRow{
		Kind:      "group",
		StartedAt: time.Now().UnixMilli(),
		GroupID:   "sess-dedup",
	})
	if err != nil {
		t.Fatalf("SaveActivityPipelineRun: %v", err)
	}

	// Simulate exactly what generateAndSaveKnowledge does after the classifier
	// returns a terminal outcome: write status+reason back to the pipeline run.
	_, outcome := classifyKnowledgeResponse(&knowledgeGenerationResponse{
		Action:          "skip",
		Title:           "Duplicate topic",
		KnowledgePoints: []string{},
	})
	if outcome == nil {
		t.Fatalf("classifier returned nil outcome for dedup skip case")
	}
	if err := store.UpdateActivityPipelineRunKnowledgeOutcome(runID, outcome.status, outcome.reason); err != nil {
		t.Fatalf("UpdateActivityPipelineRunKnowledgeOutcome: %v", err)
	}

	row, err := store.GetActivityPipelineRunByID(runID)
	if err != nil {
		t.Fatalf("GetActivityPipelineRunByID: %v", err)
	}
	if row.KnowledgeStatus != "skipped" {
		t.Errorf("row.KnowledgeStatus = %q, want %q", row.KnowledgeStatus, "skipped")
	}
	const wantReason = `dedup_skip: title_bytes=15`
	if row.KnowledgeReason != wantReason {
		t.Errorf("row.KnowledgeReason = %q, want %q (the bug would record 'AI returned no knowledge points' instead)", row.KnowledgeReason, wantReason)
	}
}

// Regression guard: activity-path memory writes must include the
// `- **Dimension**:` header, or the Memory page renders them under
// "Uncategorized". See activity_triage.go save/update templates.
func TestActivityMemoryWrites_IncludeDimensionHeader(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	c := &ActivityTriager{app: &App{}}
	session := ActivitySessionRow{
		ID:          "dim-test-session",
		StartTime:   time.Now().UnixMilli(),
		EndTime:     time.Now().UnixMilli() + 1000,
		TriggerKind: "idle_transition", // non-speech so logSpeechMemorySaved is a no-op
		AppNames:    `["Chrome"]`,
		EventCount:  1,
	}
	group := sessionGroup{Sessions: []ActivitySessionRow{session}}

	// --- save path: valid dimension is preserved verbatim ---
	entryID, err := c.saveActivityMemory(session, group, &mcMemoryGenerationResponse{
		Title:        "Dim test",
		MemoryPoints: []string{"point one"},
		Dimension:    "topic_highlight",
	})
	if err != nil {
		t.Fatalf("saveActivityMemory: %v", err)
	}

	dir, err := memoryDir()
	if err != nil {
		t.Fatalf("memoryDir: %v", err)
	}
	saved := mustReadFile(t, filepath.Join(dir, entryID+".md"))
	if !strings.Contains(saved, "- **Dimension**: topic_highlight\n") {
		t.Errorf("saveActivityMemory output missing valid dimension header:\n%s", saved)
	}

	// --- update path: new valid dimension replaces old one ---
	outcome, err := c.updateActivityMemory(context.Background(), entryID, session, group, &mcMemoryGenerationResponse{
		Title:        "Dim test v2",
		MemoryPoints: []string{"point two"},
		Dimension:    "agentic_memory",
	})
	if err != nil {
		t.Fatalf("updateActivityMemory: %v", err)
	}
	if outcome != "updated" {
		t.Fatalf("updateActivityMemory outcome = %q, want %q", outcome, "updated")
	}
	updated := mustReadFile(t, filepath.Join(dir, entryID+".md"))
	if !strings.Contains(updated, "- **Dimension**: agentic_memory\n") {
		t.Errorf("updateActivityMemory output missing updated dimension header:\n%s", updated)
	}

	// --- update path: unknown dimension falls back to user_insight ---
	// Documents the normalizeMemoryDimension contract so a future regression
	// that silently writes the raw invalid value (breaking UI grouping) fails
	// here instead of in the Memory page.
	if _, err := c.updateActivityMemory(context.Background(), entryID, session, group, &mcMemoryGenerationResponse{
		Title:        "Dim test v3",
		MemoryPoints: []string{"point three"},
		Dimension:    "not_a_real_dimension",
	}); err != nil {
		t.Fatalf("updateActivityMemory (fallback): %v", err)
	}
	fallback := mustReadFile(t, filepath.Join(dir, entryID+".md"))
	if !strings.Contains(fallback, "- **Dimension**: user_insight\n") {
		t.Errorf("unknown dimension did not normalize to user_insight:\n%s", fallback)
	}
}

func TestActivityMemoryWrites_NormalizeMarkdownToCurrentUILanguage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	runner := &activityMarkdownTranslationFakeRunner{responses: []string{
		"# 活动记忆\n\n- **Source**: should be ignored\n- **Dimension**: should_be_ignored\n\n## Memory Points\n\n- 已翻译的初始要点\n",
		`{"memoryPoints":["old point","new point"]}`,
		"# 活动记忆更新\n\n- **Source**: should be ignored\n- **Dimension**: should_be_ignored\n\n## Memory Points\n\n- 已翻译的旧要点\n- 已翻译的新要点\n",
	}}
	app := newActivityTranslationTestApp(t, runner)
	// Reserve the fixture queue for translation and memory-point rewriting.
	app.memoryCanonicalizer = func(string, string) (string, error) {
		return "", errMemorySemanticUnverified
	}
	c := &ActivityTriager{app: app}
	session := ActivitySessionRow{
		ID:          "activity-memory-translation",
		StartTime:   time.Now().UnixMilli(),
		EndTime:     time.Now().UnixMilli() + 1000,
		TriggerKind: "idle_transition",
		AppNames:    `["Chrome"]`,
		EventCount:  1,
	}
	// The update rewrite now validates the captured source before model use.
	app.activity = &activityMonitorState{store: newTestStore(t)}
	if err := app.activity.store.SaveSession(session, nil, nil); err != nil {
		t.Fatal(err)
	}
	group := sessionGroup{Sessions: []ActivitySessionRow{session}}

	entryID, err := c.saveActivityMemory(session, group, &mcMemoryGenerationResponse{
		Title:        "Activity Memory",
		MemoryPoints: []string{"initial point"},
		Dimension:    "topic_highlight",
	})
	if err != nil {
		t.Fatalf("saveActivityMemory: %v", err)
	}

	dir, err := memoryDir()
	if err != nil {
		t.Fatalf("memoryDir: %v", err)
	}
	saved := mustReadFile(t, filepath.Join(dir, entryID+".md"))
	for _, want := range []string{
		"# 活动记忆",
		"- **Source**: activity",
		"- **Dimension**: topic_highlight",
		"- **Activity Session**:",
		"- 已翻译的初始要点",
	} {
		if !strings.Contains(saved, want) {
			t.Fatalf("saved memory missing %q:\n%s", want, saved)
		}
	}

	if _, err := c.updateActivityMemory(context.Background(), entryID, session, group, &mcMemoryGenerationResponse{
		Title:        "Activity Memory Updated",
		MemoryPoints: []string{"old point", "new point"},
		Dimension:    "agentic_memory",
	}); err != nil {
		t.Fatalf("updateActivityMemory: %v", err)
	}
	updated := mustReadFile(t, filepath.Join(dir, entryID+".md"))
	document, ok := parseOKFDocument(updated)
	if !ok || okfString(document.Frontmatter, "title") != "活动记忆更新" ||
		okfString(document.Frontmatter, "source") != "activity" ||
		okfString(document.Frontmatter, "dimension") != "agentic_memory" ||
		!slices.Equal(okfStringSlice(document.Frontmatter, "activity_sessions"), []string{session.ID}) {
		t.Fatalf("updated memory lost translated OKF metadata:\n%s", updated)
	}
	if provenance := memoryReadProvenanceFromContent(updated); provenance.Withheld || !slices.Equal(provenance.Sessions, []string{session.ID}) {
		t.Fatalf("translation changed source privacy: %+v", provenance)
	}
	for _, want := range []string{
		"- 已翻译的旧要点",
		"- 已翻译的新要点",
	} {
		if !strings.Contains(updated, want) {
			t.Fatalf("updated memory missing %q:\n%s", want, updated)
		}
	}
	if runner.calls != 3 {
		t.Fatalf("runner calls = %d, want 3", runner.calls)
	}
	if !strings.Contains(runner.lastPrompt, "Simplified Chinese") {
		t.Fatalf("last prompt should target current UI language, got:\n%s", runner.lastPrompt)
	}
	if strings.Contains(runner.lastPrompt, "agentic_memory") || strings.Contains(runner.lastPrompt, session.ID) {
		t.Fatalf("last prompt should not include machine-readable metadata, got:\n%s", runner.lastPrompt)
	}
}

func TestActivityKnowledgeWrites_NormalizeMarkdownToCurrentUILanguage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	runner := &activityMarkdownTranslationFakeRunner{responses: []string{
		"# 活动知识\n\n- **Apps**: should be ignored\n\n## Description\n\n已翻译描述\n\n## Key Insights\n\n- 已翻译洞见\n",
		"# 活动知识更新\n\n- **Apps**: should be ignored\n\n## Description\n\n已翻译更新描述\n\n## Key Insights\n\n- 已翻译旧洞见\n- 已翻译新洞见\n",
	}}
	app := newActivityTranslationTestApp(t, runner)
	c := &ActivityTriager{app: app}
	session := ActivitySessionRow{
		ID:          "activity-knowledge-translation",
		StartTime:   time.Now().UnixMilli(),
		EndTime:     time.Now().UnixMilli() + 1000,
		TriggerKind: "idle_transition",
		AppNames:    `["Code","Chrome"]`,
		EventCount:  1,
	}
	group := sessionGroup{Sessions: []ActivitySessionRow{session}}

	entryID, err := c.saveKnowledge(session, group, &knowledgeGenerationResponse{
		Title:           "Activity Knowledge",
		Description:     "English description",
		KnowledgePoints: []string{"initial insight"},
	})
	if err != nil {
		t.Fatalf("saveKnowledge: %v", err)
	}

	dir, err := knowledgeDir()
	if err != nil {
		t.Fatalf("knowledgeDir: %v", err)
	}
	saved := mustReadFile(t, filepath.Join(dir, entryID+".md"))
	for _, want := range []string{
		"title: 活动知识",
		"apps:",
		"- Code",
		"activity_sessions:",
		session.ID,
		"## Description",
		"已翻译描述",
		"- 已翻译洞见",
	} {
		if !strings.Contains(saved, want) {
			t.Fatalf("saved knowledge missing %q:\n%s", want, saved)
		}
	}

	_, err = c.updateKnowledge(session, group, &knowledgeGenerationResponse{
		UpdateID:        entryID,
		Title:           "Activity Knowledge Updated",
		Description:     "English update description",
		KnowledgePoints: []string{"old insight", "new insight"},
	})
	if err != nil {
		t.Fatalf("updateKnowledge: %v", err)
	}
	updated := mustReadFile(t, filepath.Join(dir, entryID+".md"))
	for _, want := range []string{
		"title: 活动知识更新",
		"apps:",
		"- Chrome",
		"activity_sessions:",
		session.ID,
		"## Description",
		"已翻译更新描述",
		"- 已翻译旧洞见",
		"- 已翻译新洞见",
	} {
		if !strings.Contains(updated, want) {
			t.Fatalf("updated knowledge missing %q:\n%s", want, updated)
		}
	}
	if runner.calls != 2 {
		t.Fatalf("runner calls = %d, want 2", runner.calls)
	}
	if !strings.Contains(runner.lastPrompt, "Simplified Chinese") {
		t.Fatalf("last prompt should target current UI language, got:\n%s", runner.lastPrompt)
	}
	if strings.Contains(runner.lastPrompt, session.ID) || strings.Contains(runner.lastPrompt, "Code, Chrome") {
		t.Fatalf("last prompt should not include machine-readable metadata, got:\n%s", runner.lastPrompt)
	}
}

func mustReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestBuildGroupSummary_SpeechRendering(t *testing.T) {
	now := time.Now().UnixMilli()
	mkSession := func(isMeeting bool) ActivitySessionRow {
		return ActivitySessionRow{
			ID:          "sess",
			StartTime:   now,
			EndTime:     now + 60_000,
			TriggerKind: "speech_idle",
			AppNames:    `["Zoom"]`,
			EventCount:  1,
			IsMeeting:   isMeeting,
		}
	}
	mkSpeechEvt := func(source, text, app string) activitymon.ActivityEvent {
		return activitymon.ActivityEvent{
			Timestamp:    now,
			Kind:         activitymon.EventSpeech,
			AppName:      app,
			AppBundleID:  "us.zoom.xos",
			WindowTitle:  "Zoom Meeting",
			SpeechSource: source,
			SpeechText:   text,
		}
	}

	c := &ActivityTriager{app: &App{}}

	tests := []struct {
		name       string
		group      sessionGroup
		events     []activitymon.ActivityEvent
		wantSubstr string
		notWantSub string
	}{
		{
			// P1 regression: remote participant audio inside a meeting must
			// NOT render as "Spoke (in Zoom)" — it is by definition not the
			// local user, and crediting their words to the user is the same
			// attribution hallucination class as the WeChat-video case.
			name:       "meeting_system_audio_renders_as_other_participant",
			group:      sessionGroup{Sessions: []ActivitySessionRow{mkSession(true)}},
			events:     []activitymon.ActivityEvent{mkSpeechEvt("system_audio", "We should ship by Friday.", "Zoom")},
			wantSubstr: "Other meeting participant audio",
			notWantSub: "Spoke (in Zoom)",
		},
		{
			// Mic capture inside a meeting is the local user — keep "Spoke".
			name:       "meeting_microphone_still_renders_as_spoke",
			group:      sessionGroup{Sessions: []ActivitySessionRow{mkSession(true)}},
			events:     []activitymon.ActivityEvent{mkSpeechEvt("microphone", "Sounds good to me.", "Zoom")},
			wantSubstr: "Spoke (in Zoom)",
			notWantSub: "Other meeting participant audio",
		},
		{
			// Outside meetings, system_audio still flows through the ambient
			// branch (isLikelyNonUserSpeech catches it).
			name:       "non_meeting_system_audio_renders_as_ambient",
			group:      sessionGroup{Sessions: []ActivitySessionRow{mkSession(false)}},
			events:     []activitymon.ActivityEvent{mkSpeechEvt("system_audio", "playback content", "Bilibili")},
			wantSubstr: "Ambient audio heard near Bilibili",
			notWantSub: "Other meeting participant audio",
		},
		{
			// Dictation into Claude while Zoom sits idle in front: the
			// user's words to Claude, not a call or a conversation.
			name:  "dictation_renders_as_dictated_to_app",
			group: sessionGroup{Sessions: []ActivitySessionRow{mkSession(false)}},
			events: func() []activitymon.ActivityEvent {
				evt := mkSpeechEvt("microphone", "Add text messages to the backlog.", "zoom.us")
				evt.SpeechDictationApp = "Claude"
				return []activitymon.ActivityEvent{evt}
			}(),
			wantSubstr: "Dictated to Claude",
			notWantSub: "Spoke (in zoom.us)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _, _ := c.buildGroupSummary(tt.group, tt.events, ProfileGroup(tt.group, tt.events))
			if !strings.Contains(got, tt.wantSubstr) {
				t.Errorf("buildGroupSummary output missing %q\n--- got ---\n%s", tt.wantSubstr, got)
			}
			if tt.notWantSub != "" && strings.Contains(got, tt.notWantSub) {
				t.Errorf("buildGroupSummary output contains forbidden %q\n--- got ---\n%s", tt.notWantSub, got)
			}
		})
	}
}

// TestBuildGroupSummary_SourceAttribution verifies the two prompt-level
// affordances that scope OCR signal by source: the up-front "Source
// attribution" paragraph, and the per-OCR-line [app=X] tag. Together they
// give the triage LLM a reason to treat content from different apps as
// independent contexts instead of correlating temporally-adjacent material
// across unrelated workstreams (rizzotho's "笔记 app 侧边栏 vs 主编辑区"
// scenario and any cross-app variant).
func TestBuildGroupSummary_SourceAttribution(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UnixMilli()
	session := ActivitySessionRow{
		ID:          "src-tag-sess",
		StartTime:   now,
		EndTime:     now + 10_000,
		TriggerKind: "idle_transition",
		AppNames:    `["Slack"]`,
		EventCount:  1,
	}
	if err := s.SaveSession(session, nil, nil); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	// One snapshot tagged with parser app "slack". The triager's OCR
	// path resolves OCRFrameRow.SegmentID against activity_snapshots.id
	// first (snapshot-based producer), so SegmentID = snapshot.id here.
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	snap := ActivitySnapshotRow{
		SessionID:   session.ID,
		Timestamp:   now,
		FilePath:    "fake.jpg",
		Width:       100,
		Height:      100,
		SizeBytes:   100,
		Trigger:     "test",
		StorageTier: "hot",
		App:         "slack",
	}
	if err := s.SaveSnapshots(tx, []ActivitySnapshotRow{snap}); err != nil {
		tx.Rollback()
		t.Fatalf("SaveSnapshots: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	saved, err := s.GetSessionSnapshots(session.ID)
	if err != nil || len(saved) != 1 {
		t.Fatalf("GetSessionSnapshots: rows=%d err=%v", len(saved), err)
	}
	if err := s.SaveOCRFrame(OCRFrameRow{
		SessionID:  session.ID,
		SegmentID:  saved[0].ID,
		FrameTime:  1.5,
		Text:       "Pull request review: ship the migration",
		Confidence: 0.9,
	}); err != nil {
		t.Fatalf("SaveOCRFrame: %v", err)
	}

	c := &ActivityTriager{app: &App{activity: &activityMonitorState{store: s}}}
	out, _, _ := c.buildGroupSummary(sessionGroup{Sessions: []ActivitySessionRow{session}}, nil, SessionContentProfile{})

	if !strings.Contains(out, "Source attribution:") {
		t.Errorf("missing Source attribution preamble\n--- got ---\n%s", out)
	}
	if !strings.Contains(out, "[app=slack]") {
		t.Errorf("OCR line missing [app=slack] tag\n--- got ---\n%s", out)
	}
}

// TestBuildGroupSummary_SourceAttribution_UnknownAppFallback verifies the
// fallback for snapshots whose app key is empty (Terminal / Finder / IDE /
// any non-parsed surface). We deliberately do NOT borrow from the session's
// app set: a 2-app group would attribute half its frames to the wrong app
// and silently lie. "unknown" is honest.
func TestBuildGroupSummary_SourceAttribution_UnknownAppFallback(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UnixMilli()
	session := ActivitySessionRow{
		ID:          "src-tag-unknown",
		StartTime:   now,
		EndTime:     now + 10_000,
		TriggerKind: "idle_transition",
		AppNames:    `["Terminal"]`,
		EventCount:  1,
	}
	if err := s.SaveSession(session, nil, nil); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	if err := s.SaveSnapshots(tx, []ActivitySnapshotRow{{
		SessionID:   session.ID,
		Timestamp:   now,
		FilePath:    "fake.jpg",
		Width:       100,
		Height:      100,
		SizeBytes:   100,
		Trigger:     "test",
		StorageTier: "hot",
		App:         "", // parser miss
	}}); err != nil {
		tx.Rollback()
		t.Fatalf("SaveSnapshots: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	saved, _ := s.GetSessionSnapshots(session.ID)
	if err := s.SaveOCRFrame(OCRFrameRow{
		SessionID:  session.ID,
		SegmentID:  saved[0].ID,
		FrameTime:  2.0,
		Text:       "$ go test ./... PASS",
		Confidence: 0.9,
	}); err != nil {
		t.Fatalf("SaveOCRFrame: %v", err)
	}

	c := &ActivityTriager{app: &App{activity: &activityMonitorState{store: s}}}
	out, _, _ := c.buildGroupSummary(sessionGroup{Sessions: []ActivitySessionRow{session}}, nil, SessionContentProfile{})

	if !strings.Contains(out, "[app=unknown]") {
		t.Errorf("OCR line for unparsed snapshot should fall back to [app=unknown]\n--- got ---\n%s", out)
	}
	if strings.Contains(out, "[app=Terminal]") {
		t.Errorf("must not borrow session-level app name; that lies on multi-app sessions\n--- got ---\n%s", out)
	}
}

// TestBuildGroupSummary_SourceAttribution_MultilineFlattened guards against a
// regression where Vision's '\n'-joined observations leak past the [app=X]
// prefix. Without flattening, the prompt would render one tagged bullet
// followed by 2-3 naked lines that carry the OCR signal but NOT the source
// tag — silently defeating the attribution. Collapse to "; " so distinct
// observations stay visually separated within the single bullet.
func TestBuildGroupSummary_SourceAttribution_MultilineFlattened(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UnixMilli()
	session := ActivitySessionRow{
		ID:          "src-tag-multiline",
		StartTime:   now,
		EndTime:     now + 10_000,
		TriggerKind: "idle_transition",
		AppNames:    `["Slack"]`,
		EventCount:  1,
	}
	if err := s.SaveSession(session, nil, nil); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	tx, _ := s.db.Begin()
	if err := s.SaveSnapshots(tx, []ActivitySnapshotRow{{
		SessionID:   session.ID,
		Timestamp:   now,
		FilePath:    "fake.jpg",
		Width:       100,
		Height:      100,
		SizeBytes:   100,
		Trigger:     "test",
		StorageTier: "hot",
		App:         "slack",
	}}); err != nil {
		tx.Rollback()
		t.Fatalf("SaveSnapshots: %v", err)
	}
	tx.Commit()
	saved, _ := s.GetSessionSnapshots(session.ID)
	if err := s.SaveOCRFrame(OCRFrameRow{
		SessionID:  session.ID,
		SegmentID:  saved[0].ID,
		FrameTime:  1.5,
		Text:       "first observation\nsecond observation\nthird observation",
		Confidence: 0.9,
	}); err != nil {
		t.Fatalf("SaveOCRFrame: %v", err)
	}

	c := &ActivityTriager{app: &App{activity: &activityMonitorState{store: s}}}
	out, _, _ := c.buildGroupSummary(sessionGroup{Sessions: []ActivitySessionRow{session}}, nil, SessionContentProfile{})

	// Find the OCR samples block and check the bullet for our frame.
	idx := strings.Index(out, "Screen text samples")
	if idx < 0 {
		t.Fatalf("missing OCR samples section\n--- got ---\n%s", out)
	}
	ocrBlock := out[idx:]
	// The bullet must contain all three observations on the SAME bullet
	// line — no bare '\n' separators that would orphan continuation lines
	// past the [app=slack] tag.
	wantBullet := "- [1.5s] [app=slack] first observation; second observation; third observation"
	if !strings.Contains(ocrBlock, wantBullet) {
		t.Errorf("expected flattened multiline OCR with '; ' separators in:\n%s\n--- looking for ---\n%s", ocrBlock, wantBullet)
	}
	// And the substring "observation\n" must not appear inside the
	// bullet's body (between [app=slack] and the next "- "/end).
	bulletStart := strings.Index(ocrBlock, "- [1.5s]")
	if bulletStart >= 0 {
		// End of bullet is either the next bullet or the section end.
		rest := ocrBlock[bulletStart:]
		end := strings.Index(rest[1:], "\n- ")
		var body string
		if end >= 0 {
			body = rest[:end+1]
		} else {
			body = rest
		}
		// Allow exactly one trailing newline (the bullet terminator).
		inner := strings.TrimSuffix(body, "\n")
		if strings.Contains(inner, "\n") {
			t.Errorf("bullet body still contains internal newlines (lost attribution for continuation lines):\n%q", inner)
		}
	}
}

// TestBuildGroupSummary_SourceAttribution_DedupScopedByApp guards against the
// global-prefix-dedup that would drop the second source when the same text
// shows up in two apps (e.g. user copy-pastes a snippet across apps).
// Surviving both lines preserves the "saw this in two contexts" signal —
// exactly the kind of fact source attribution exists to surface.
func TestBuildGroupSummary_SourceAttribution_DedupScopedByApp(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UnixMilli()
	mkSess := func(id, app string) ActivitySessionRow {
		return ActivitySessionRow{
			ID:          id,
			StartTime:   now,
			EndTime:     now + 10_000,
			TriggerKind: "idle_transition",
			AppNames:    `["` + app + `"]`,
			EventCount:  1,
		}
	}
	sessA := mkSess("dedup-src-a", "Slack")
	sessB := mkSess("dedup-src-b", "Notion")
	if err := s.SaveSession(sessA, nil, nil); err != nil {
		t.Fatalf("SaveSession A: %v", err)
	}
	if err := s.SaveSession(sessB, nil, nil); err != nil {
		t.Fatalf("SaveSession B: %v", err)
	}
	tx, _ := s.db.Begin()
	if err := s.SaveSnapshots(tx, []ActivitySnapshotRow{
		{SessionID: sessA.ID, Timestamp: now, FilePath: "a.jpg", Width: 100, Height: 100, SizeBytes: 100, Trigger: "test", StorageTier: "hot", App: "slack"},
		{SessionID: sessB.ID, Timestamp: now + 1_000, FilePath: "b.jpg", Width: 100, Height: 100, SizeBytes: 100, Trigger: "test", StorageTier: "hot", App: "notion"},
	}); err != nil {
		tx.Rollback()
		t.Fatalf("SaveSnapshots: %v", err)
	}
	tx.Commit()
	savedA, _ := s.GetSessionSnapshots(sessA.ID)
	savedB, _ := s.GetSessionSnapshots(sessB.ID)
	// Same OCR text, two apps — the global prefix dedup would have
	// suppressed the second one. The scoped dedup keeps both.
	dupText := "Pull request review: ship the migration tomorrow morning"
	for _, x := range []struct {
		sess string
		snap int64
		ft   float64
	}{
		{sessA.ID, savedA[0].ID, 1.0},
		{sessB.ID, savedB[0].ID, 2.0},
	} {
		if err := s.SaveOCRFrame(OCRFrameRow{
			SessionID: x.sess, SegmentID: x.snap, FrameTime: x.ft,
			Text: dupText, Confidence: 0.9,
		}); err != nil {
			t.Fatalf("SaveOCRFrame: %v", err)
		}
	}

	c := &ActivityTriager{app: &App{activity: &activityMonitorState{store: s}}}
	out, _, _ := c.buildGroupSummary(sessionGroup{Sessions: []ActivitySessionRow{sessA, sessB}}, nil, SessionContentProfile{})

	if !strings.Contains(out, "[app=slack]") {
		t.Errorf("missing slack OCR sample\n--- got ---\n%s", out)
	}
	if !strings.Contains(out, "[app=notion]") {
		t.Errorf("missing notion OCR sample — dedup wrongly folded cross-source duplicate\n--- got ---\n%s", out)
	}
}

func TestIsLikelyNonUserSpeech(t *testing.T) {
	tests := []struct {
		name string
		evt  activitymon.ActivityEvent
		want bool
	}{
		{
			name: "system_audio_source_always_non_user",
			evt:  activitymon.ActivityEvent{SpeechSource: "system_audio", AppBundleID: "com.apple.dt.Xcode"},
			want: true,
		},
		{
			name: "likely_echo_flag_always_non_user",
			evt:  activitymon.ActivityEvent{SpeechSource: "microphone", SpeechLikelyEcho: true, AppBundleID: "com.apple.dt.Xcode"},
			want: true,
		},
		{
			name: "mic_in_wechat_media_window_is_non_user",
			evt:  activitymon.ActivityEvent{SpeechSource: "microphone", AppBundleID: "com.tencent.xinWeChat", WindowTitle: "图片和视频"},
			want: true,
		},
		{
			name: "mic_in_wechat_chat_window_is_user_speech",
			evt:  activitymon.ActivityEvent{SpeechSource: "microphone", AppBundleID: "com.tencent.xinWeChat", WindowTitle: "聊天"},
			want: false,
		},
		{
			name: "mic_in_known_media_player_is_non_user",
			evt:  activitymon.ActivityEvent{SpeechSource: "microphone", AppBundleID: "com.apple.QuickTimePlayerX"},
			want: true,
		},
		{
			name: "mic_in_editor_is_user_speech",
			evt:  activitymon.ActivityEvent{SpeechSource: "microphone", AppBundleID: "com.microsoft.VSCode"},
			want: false,
		},
		{
			name: "empty_source_falls_back_to_heuristic",
			evt:  activitymon.ActivityEvent{AppBundleID: "com.apple.QuickTimePlayerX"},
			want: true,
		},
		{
			name: "future_unknown_source_skips_heuristic",
			evt:  activitymon.ActivityEvent{SpeechSource: "future_diarization_source", AppBundleID: "com.apple.QuickTimePlayerX"},
			want: false,
		},
		{
			name: "bilibili_app_is_non_user",
			evt:  activitymon.ActivityEvent{SpeechSource: "microphone", AppBundleID: "tv.danmaku.bili.client"},
			want: true,
		},
		{
			name: "apple_podcasts_is_non_user",
			evt:  activitymon.ActivityEvent{SpeechSource: "microphone", AppBundleID: "com.apple.podcasts"},
			want: true,
		},
		{
			name: "apple_tv_is_non_user",
			evt:  activitymon.ActivityEvent{SpeechSource: "microphone", AppBundleID: "com.apple.TV"},
			want: true,
		},
		{
			name: "apple_music_is_non_user",
			evt:  activitymon.ActivityEvent{SpeechSource: "microphone", AppBundleID: "com.apple.Music"},
			want: true,
		},
		{
			name: "chrome_youtube_is_non_user",
			evt:  activitymon.ActivityEvent{SpeechSource: "microphone", AppBundleID: "com.google.Chrome", WindowTitle: "Some video title - YouTube"},
			want: true,
		},
		{
			name: "chrome_bilibili_web_is_non_user",
			evt:  activitymon.ActivityEvent{SpeechSource: "microphone", AppBundleID: "com.google.Chrome", WindowTitle: "某视频标题_哔哩哔哩_bilibili"},
			want: true,
		},
		{
			name: "safari_netflix_is_non_user",
			evt:  activitymon.ActivityEvent{SpeechSource: "microphone", AppBundleID: "com.apple.Safari", WindowTitle: "Watch Now - Netflix"},
			want: true,
		},
		{
			name: "arc_twitch_is_non_user",
			evt:  activitymon.ActivityEvent{SpeechSource: "microphone", AppBundleID: "company.thebrowser.Browser", WindowTitle: "channelname - Twitch"},
			want: true,
		},
		{
			name: "edge_tencent_video_is_non_user",
			evt:  activitymon.ActivityEvent{SpeechSource: "microphone", AppBundleID: "com.microsoft.edgemac", WindowTitle: "电影 - 腾讯视频"},
			want: true,
		},
		{
			name: "chrome_stackoverflow_is_user_speech",
			evt:  activitymon.ActivityEvent{SpeechSource: "microphone", AppBundleID: "com.google.Chrome", WindowTitle: "How to use goroutines - Stack Overflow"},
			want: false,
		},
		{
			name: "chrome_gmail_is_user_speech",
			evt:  activitymon.ActivityEvent{SpeechSource: "microphone", AppBundleID: "com.google.Chrome", WindowTitle: "Inbox (5) - user@example.com - Gmail"},
			want: false,
		},
		{
			// Non-browser app with "YouTube" in window title (e.g. user is
			// taking notes about YouTube content in a native editor) must
			// NOT trigger the browser branch — the bundle-id browser check
			// is what gates this match.
			name: "non_browser_with_video_site_in_title_is_user_speech",
			evt:  activitymon.ActivityEvent{SpeechSource: "microphone", AppBundleID: "com.microsoft.VSCode", WindowTitle: "youtube-notes.md — wails-gui"},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isLikelyNonUserSpeech(tt.evt); got != tt.want {
				t.Fatalf("isLikelyNonUserSpeech(%+v) = %v, want %v", tt.evt, got, tt.want)
			}
		})
	}
}
