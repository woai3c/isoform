package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wails-gui/internal/pii"
	"wails-gui/internal/platform/activitymon"
)

func newActivityEvidenceTestStore(t *testing.T) *ActivityStore {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "ie-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	store, err := NewActivityStore(filepath.Join(home, "activity"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func evidenceTokenExists(tokens []ActivityEvidenceToken, kind, value string) bool {
	for _, token := range tokens {
		if token.Kind == kind && token.Value == value {
			return true
		}
	}
	return false
}

func TestActivityEvidenceTokensRedactedIdentifiersOnly(t *testing.T) {
	tokens := extractActivityEvidenceTokens("Looking at ~/.sample-agent/activity, it is 60GB.\nhttps://github.com/example/demo/pull/42\nerror: disk full\npassword=TopSecret2026\nno unrelated free words", 9000)
	for _, want := range []struct{ kind, value string }{
		{"path", "~/.sample-agent/activity"}, {"figure", "60 gb"},
		{"repo", "example/demo"}, {"pr", "example/demo#42"}, {"error", "error"},
	} {
		if !evidenceTokenExists(tokens, want.kind, want.value) {
			t.Errorf("missing %s %q: %+v", want.kind, want.value, tokens)
		}
	}
	for _, token := range tokens {
		if token.Kind == "path" && strings.HasPrefix(token.Value, "/github.com") {
			t.Fatalf("URL was also parsed as filesystem path: %+v", token)
		}
	}
	encoded, _ := json.Marshal(tokens)
	for _, forbidden := range []string{"TopSecret2026", "unrelated", "redacted"} {
		if strings.Contains(strings.ToLower(string(encoded)), strings.ToLower(forbidden)) {
			t.Fatalf("token persisted forbidden content %q: %s", forbidden, encoded)
		}
	}
	for _, token := range tokens {
		if token.LastSeenMs != 9000 {
			t.Fatalf("lost observed time: %+v", token)
		}
	}
	// A partially redacted error cannot be a durable matching identifier.
	if got := extractActivityEvidenceTokens("error: password=TopSecret2026", 1); len(got) != 0 {
		t.Fatalf("redacted error yielded identity: %+v", got)
	}
}

func TestActivityEvidenceTokensBoundedAndKeepLastSeen(t *testing.T) {
	var text strings.Builder
	for i := 0; i < activityEvidenceTokenLimit+20; i++ {
		text.WriteString("/tmp/file-" + strings.Repeat("x", i+1) + "\n")
	}
	tokens := extractActivityEvidenceTokens(text.String(), 1000)
	if len(tokens) != activityEvidenceTokenLimit {
		t.Fatalf("len = %d, want %d", len(tokens), activityEvidenceTokenLimit)
	}
	tokens = mergeActivityEvidenceTokens(tokens, extractActivityEvidenceTokens("/tmp/file-x", 2000))
	if tokens[0].Value != "/tmp/file-x" || tokens[0].LastSeenMs != 2000 {
		t.Fatalf("latest evidence not retained: %+v", tokens[0])
	}
}

func TestActivityEvidenceFigureUnitBoundaries(t *testing.T) {
	for _, test := range []struct {
		text, want string
	}{
		{text: "usage is 60%", want: "60 %"},
		{text: "usage is 60% today", want: "60 %"},
		{text: "usage is 60.3 GB", want: "60.3 gb"},
		{text: "60secondsuffix"},
		{text: "60gibberish"},
	} {
		t.Run(test.text, func(t *testing.T) {
			tokens := extractActivityEvidenceTokens(test.text, 1)
			if test.want == "" {
				if len(tokens) != 0 {
					t.Fatalf("partial unit yielded evidence: %+v", tokens)
				}
				return
			}
			if !evidenceTokenExists(tokens, "figure", test.want) {
				t.Fatalf("missing figure %q: %+v", test.want, tokens)
			}
		})
	}
}

func TestActivityEventEvidenceExcludesPasswordFields(t *testing.T) {
	for _, role := range []string{"AXSecureTextField", "password"} {
		for _, event := range []activitymon.ActivityEvent{
			{Kind: activitymon.EventKeyboard, AppBundleID: "com.example.chat", AppName: "Chat", FocusRole: role, TypedText: "/tmp/secret 60GB"},
			{Kind: activitymon.EventClick, AppBundleID: "com.example.chat", AppName: "Chat", ClickRole: role, ClickLabel: "/tmp/secret 60GB"},
		} {
			if tokens := activityEventEvidenceTokens(event); len(tokens) > 0 {
				t.Fatalf("%s %s yielded tokens: %+v", event.Kind, role, tokens)
			}
		}
	}
}

func TestTimelineEvidencePersistsAXAndLateOCR(t *testing.T) {
	store := newActivityEvidenceTestStore(t)
	saveTimelineTestSession(t, store, ActivitySessionRow{ID: "terminal", StartTime: 1000, EndTime: 2000, EventCount: 2, AppNames: `["iTerm2"]`}, []activitymon.ActivityEvent{
		{Timestamp: 1000, Kind: activitymon.EventClick, AppBundleID: "com.googlecode.iterm2", AppName: "iTerm2", ClickLabel: "Inspect"},
		{Timestamp: 2000, Kind: activitymon.EventKeyboard, AppBundleID: "com.googlecode.iterm2", AppName: "iTerm2", TypedText: "mo"},
	})
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSnapshots(tx, []ActivitySnapshotRow{{SessionID: "terminal", Timestamp: 1500, App: "iTerm2", AXText: "~/.sample-agent/activity 60.3 GB"}}); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	runs, err := store.GetLastTimelineRuns(1)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs=%+v err=%v", runs, err)
	}
	run := runs[0]
	if allowed, err := store.RefreshTimelineRunEvidence(&run); err != nil || !allowed {
		t.Fatalf("refresh allowed=%v err=%v", allowed, err)
	}
	if !evidenceTokenExists(run.EvidenceTokens, "path", "~/.sample-agent/activity") || !evidenceTokenExists(run.EvidenceTokens, "figure", "60.3 gb") {
		t.Fatalf("AX evidence missing: %+v", run.EvidenceTokens)
	}
	snaps, _ := store.GetSessionSnapshots("terminal")
	if err := store.SaveOCRFrame(OCRFrameRow{SessionID: "terminal", SegmentID: snaps[0].ID, Text: "error: disk full"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RefreshTimelineRunEvidence(&run); err != nil {
		t.Fatal(err)
	}
	saved, err := store.GetTimelineRunByID(run.ID)
	if err != nil || !evidenceTokenExists(saved.EvidenceTokens, "error", "error") {
		t.Fatalf("late OCR not persisted: run=%+v err=%v", saved, err)
	}
	if _, err := store.db.Exec(`DELETE FROM activity_events WHERE session_id = 'terminal'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DELETE FROM activity_snapshots WHERE session_id = 'terminal'`); err != nil {
		t.Fatal(err)
	}
	if allowed, err := store.RefreshTimelineRunEvidence(saved); err != nil || !allowed || len(saved.EvidenceTokens) == 0 {
		t.Fatalf("raw retention erased persisted identity: allowed=%v tokens=%+v err=%v", allowed, saved.EvidenceTokens, err)
	}
}

func TestTimelineEvidenceProtectedPrivateAndIgnoredRunsContributeNothing(t *testing.T) {
	originalSensitive := pii.GetUserSensitiveApps()
	originalVoice := GetUserVoiceInputApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(originalSensitive); SetUserVoiceInputApps(originalVoice) })
	pii.SetUserSensitiveApps(map[string]string{"com.example.hidden": "Hidden"})
	SetUserVoiceInputApps(map[string]string{"com.example.muted": "Muted"})
	for _, test := range []struct {
		name, bundle, app, title, triage string
		private                          bool
	}{
		{name: "protected app", bundle: "com.1password.1password", app: "1Password"},
		{name: "user ignore", bundle: "com.example.hidden", app: "Hidden"},
		{name: "dont listen", bundle: "com.example.muted", app: "Muted"},
		{name: "private title", bundle: "com.google.Chrome", app: "Chrome", title: "Private Browsing"},
		{name: "private scrub marker", bundle: "com.google.Chrome", app: "Chrome", private: true},
		{name: "protected session", bundle: "com.example.editor", app: "Editor", triage: "protected"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newActivityEvidenceTestStore(t)
			saveTimelineTestSession(t, store, ActivitySessionRow{ID: "excluded", StartTime: 1000, EndTime: 1000, EventCount: 1, AppNames: `[]`, TriageStatus: test.triage}, []activitymon.ActivityEvent{{
				Timestamp: 1000, Kind: activitymon.EventKeyboard, AppBundleID: test.bundle, AppName: test.app, WindowTitle: test.title, PrivateWindow: test.private, TypedText: "/tmp/secret 60GB",
			}})
			runs, err := store.GetLastTimelineRuns(1)
			if err != nil || len(runs) != 1 {
				t.Fatalf("runs=%+v err=%v", runs, err)
			}
			run := runs[0]
			if _, err := store.RefreshTimelineRunEvidence(&run); err != nil {
				t.Fatal(err)
			}
			if !run.EvidenceExcluded || len(run.EvidenceTokens) != 0 {
				t.Fatalf("excluded run persisted evidence: %+v", run)
			}
			if allowed, err := store.RefreshTimelineRunEvidence(&run); err != nil || allowed || len(run.EvidenceTokens) != 0 {
				t.Fatalf("excluded candidate allowed=%v err=%v tokens=%+v", allowed, err, run.EvidenceTokens)
			}
		})
	}
}

func TestTimelineEvidenceRechecksNewPrivacyList(t *testing.T) {
	store := newActivityEvidenceTestStore(t)
	original := GetUserVoiceInputApps()
	t.Cleanup(func() { SetUserVoiceInputApps(original) })
	saveTimelineTestSession(t, store, ActivitySessionRow{ID: "chat", StartTime: 1000, EndTime: 1000, EventCount: 1, AppNames: `[]`}, []activitymon.ActivityEvent{{Timestamp: 1000, Kind: activitymon.EventKeyboard, AppBundleID: "com.example.chat", AppName: "Chat", TypedText: "~/.sample-agent/activity is 60GB"}})
	runs, _ := store.GetLastTimelineRuns(1)
	if len(runs) == 1 {
		if _, err := store.RefreshTimelineRunEvidence(&runs[0]); err != nil {
			t.Fatal(err)
		}
	}
	if len(runs) != 1 || len(runs[0].EvidenceTokens) == 0 {
		t.Fatalf("missing initial tokens: %+v", runs)
	}
	SetUserVoiceInputApps(map[string]string{"com.example.chat": "Chat"})
	if allowed, err := store.RefreshTimelineRunEvidence(&runs[0]); err != nil || allowed || len(runs[0].EvidenceTokens) != 0 {
		t.Fatalf("new don't-listen setting failed: allowed=%v err=%v run=%+v", allowed, err, runs[0])
	}
	saved, _ := store.GetTimelineRunByID(runs[0].ID)
	if !saved.EvidenceExcluded || len(saved.EvidenceTokens) != 0 {
		t.Fatalf("exclusion not persisted: %+v", saved)
	}
}

func TestTimelineEvidenceReconcilePreservesRetainedTokensAndExclusion(t *testing.T) {
	for _, excluded := range []bool{false, true} {
		name := "tokens"
		if excluded {
			name = "privacy exclusion"
		}
		t.Run(name, func(t *testing.T) {
			store := newActivityEvidenceTestStore(t)
			saveTimelineTestSession(t, store, ActivitySessionRow{ID: "old", StartTime: 1000, EndTime: 2000, EventCount: 2, AppNames: `[]`}, []activitymon.ActivityEvent{
				{Timestamp: 1000, Kind: activitymon.EventKeyboard, AppBundleID: "com.example.editor", AppName: "Editor", TypedText: "inspect"},
				{Timestamp: 2000, Kind: activitymon.EventKeyboard, AppBundleID: "com.example.editor", AppName: "Editor", TypedText: "/tmp/retained 60GB", PrivateWindow: excluded},
			})
			runs, _ := store.GetLastTimelineRuns(1)
			if _, err := store.RefreshTimelineRunEvidence(&runs[0]); err != nil {
				t.Fatal(err)
			}
			originalID := runs[0].ID
			// Keep the first event that defines run identity, but prune the raw
			// observation that contributed the token or private-window marker.
			if _, err := store.db.Exec(`DELETE FROM activity_events WHERE session_id = 'old' AND timestamp = 2000`); err != nil {
				t.Fatal(err)
			}
			saveTimelineTestSession(t, store, ActivitySessionRow{ID: "new", StartTime: 3000, EndTime: 3000, EventCount: 1, AppNames: `[]`}, []activitymon.ActivityEvent{{Timestamp: 3000, Kind: activitymon.EventKeyboard, AppBundleID: "com.example.editor", AppName: "Editor", TypedText: "continue"}})
			run, err := store.GetTimelineRunByID(originalID)
			if err != nil || run == nil {
				t.Fatalf("original run missing: %+v %v", run, err)
			}
			if run.EvidenceExcluded != excluded {
				t.Fatalf("privacy provenance changed: %+v", run)
			}
			if excluded {
				if len(run.EvidenceTokens) != 0 {
					t.Fatalf("excluded run gained tokens: %+v", run.EvidenceTokens)
				}
			} else if !evidenceTokenExists(run.EvidenceTokens, "path", "/tmp/retained") {
				t.Fatalf("retained token disappeared: %+v", run.EvidenceTokens)
			}
		})
	}
}

func TestTimelineEvidenceUsesWindowAndFocusedFieldMetadata(t *testing.T) {
	for _, test := range []struct {
		name, ax, parsed string
		wantExcluded     bool
		wantTokens       bool
	}{
		{name: "private AX prose", ax: "How Private Browsing works\n/tmp/private 60GB", wantTokens: true},
		{name: "private topic title", parsed: `{"title":"How Incognito works","body":"/tmp/private 60GB"}`, wantTokens: true},
		{name: "secure AX prose", ax: "AXSecureTextField documentation\n/tmp/secret 60GB", wantTokens: true},
		{name: "password AX prose", ax: "Password: enter at prompt\n/tmp/usage 60GB", wantTokens: true},
		{name: "private message", parsed: `{"messages":[{"content":"Discuss Incognito: /tmp/usage 60GB"}]}`, wantTokens: true},
		{name: "unrelated metadata", parsed: `{"body":"/tmp/usage 60GB","metadata":{"topic":"password manager"}}`, wantTokens: true},
		{name: "secure parser metadata", parsed: `{"body":"/tmp/secret 60GB","metadata":{"focusRole":"AXSecureTextField"}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newActivityEvidenceTestStore(t)
			saveTimelineTestSession(t, store, ActivitySessionRow{ID: "screen", StartTime: 1000, EndTime: 1000, EventCount: 1, AppNames: `[]`}, []activitymon.ActivityEvent{{Timestamp: 1000, Kind: activitymon.EventKeyboard, AppBundleID: "com.example.browser", AppName: "Browser", TypedText: "inspect"}})
			tx, err := store.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SaveSnapshots(tx, []ActivitySnapshotRow{{SessionID: "screen", Timestamp: 1000, App: "Browser", AXText: test.ax, ParsedJSON: test.parsed}}); err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			snaps, err := store.GetSessionSnapshots("screen")
			if err != nil || len(snaps) != 1 {
				t.Fatalf("snapshots=%+v err=%v", snaps, err)
			}
			if err := store.SaveOCRFrame(OCRFrameRow{SessionID: "screen", SegmentID: snaps[0].ID, Text: "/tmp/secret-ocr 60GB"}); err != nil {
				t.Fatal(err)
			}
			runs, _ := store.GetLastTimelineRuns(1)
			allowed, err := store.RefreshTimelineRunEvidence(&runs[0])
			if err != nil || allowed == test.wantExcluded || (len(runs[0].EvidenceTokens) > 0) != test.wantTokens {
				t.Fatalf("allowed=%v tokens=%+v err=%v", allowed, runs[0].EvidenceTokens, err)
			}
		})
	}
}
