package main

import (
	"errors"
	"strings"
	"testing"

	"wails-gui/internal/calendar"
	"wails-gui/internal/pii"
	"wails-gui/internal/platform/activitymon"
)

func TestMeetingFollowupsRedactBeforeModelPass(t *testing.T) {
	isolateActivityPrivacyTest(t)
	app := newBindingsTestApp(t)
	old := meetingFollowupModel
	t.Cleanup(func() { meetingFollowupModel = old })
	called := false
	meetingFollowupModel = func(_ *App, prompt string) (string, error) {
		called = true
		if strings.Contains(prompt, activityPrivacySecret) || !strings.Contains(prompt, "[REDACTED:api_key]") {
			t.Errorf("meeting prompt must redact its title and transcript before the first pass: %s", prompt)
		}
		return "", errors.New("stop before writes")
	}
	_, _ = app.extractMeetingFollowups(t.Context(), ActivitySessionRow{ID: "meeting-public"},
		"## Transcript\n\nMe: use "+activityPrivacySecret, &calendar.Event{Title: activityPrivacySecret}, nil)
	if !called {
		t.Fatal("the public meeting should reach the model")
	}
}

func TestMeetingModelPassesRejectProtectedSources(t *testing.T) {
	isolateActivityPrivacyTest(t)
	app := newBindingsTestApp(t)
	store := newTestStore(t)
	app.activity = &activityMonitorState{store: store}
	old := meetingFollowupModel
	t.Cleanup(func() { meetingFollowupModel = old })
	meetingFollowupModel = func(_ *App, _ string) (string, error) {
		t.Error("protected meeting reached the follow-up model")
		return "", errors.New("unexpected model call")
	}
	for _, tc := range []struct {
		name, status, appName, bundle, title string
	}{
		{name: "protected", status: "protected", appName: "Zoom"},
		{name: "sensitive-name", appName: "Private Notes"},
		{name: "sensitive-bundle", appName: "Zoom", bundle: "com.1password.desktop"},
		{name: "private-window", appName: "Zoom", title: "Private - Google Chrome - Incognito"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := ActivitySessionRow{ID: tc.name, StartTime: 1000, EndTime: 2000, TriageStatus: tc.status, MeetingApp: tc.appName}
			if err := store.SaveSession(session, []activitymon.ActivityEvent{{AppName: tc.appName, AppBundleID: tc.bundle, WindowTitle: tc.title}}, nil); err != nil {
				t.Fatal(err)
			}
			if app.meetingTranscriptAllowed(session, nil) {
				t.Fatal("protected source admitted before transcript cleanup")
			}
			if _, err := app.extractMeetingFollowups(t.Context(), session, "## Transcript\n\nMe: private meeting", nil, nil); err == nil {
				t.Fatal("protected follow-up should be rejected")
			}
		})
	}
}

func TestTranscriptCleanupRedactsBeforeFirstModelPass(t *testing.T) {
	isolateActivityPrivacyTest(t)
	runner := &memoryTranslationFakeRunner{response: "[10:00:00] Me: use [REDACTED:api_key]", ready: true}
	raw := "## Transcript\n\n[10:00:00] Me: use " + activityPrivacySecret
	app := &App{runner: runner}
	app.cleanTranscriptWithAI(raw, "Zoom", 2)
	if runner.calls != 1 {
		t.Fatalf("cleanup calls = %d, want 1", runner.calls)
	}
	if strings.Contains(runner.lastPrompt, activityPrivacySecret) || !strings.Contains(runner.lastPrompt, "[REDACTED:api_key]") {
		t.Errorf("cleanup must receive only redacted speech: %s", runner.lastPrompt)
	}
	pii.AddUserSensitiveApp("us.zoom", "Zoom")
	app.cleanTranscriptWithAI(raw, "Zoom", 2)
	if runner.calls != 1 {
		t.Fatal("new sensitive-app setting must stop cleanup")
	}
}
