package main

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"wails-gui/internal/platform/activitymon"
)

func TestActivityEvidenceErrorSignaturesRejectProse(t *testing.T) {
	for _, text := range []string{
		"Explain why the sync skipped the folder, then retry",
		"Edit", "Email the private draft tomorrow", "Enable this setting", "Export this conversation",
		"Error-prone prose is still prose", "ENABLED this setting", "ENOENT permission denied",
	} {
		if tokens := extractActivityEvidenceTokens(text, 1); len(tokens) != 0 {
			t.Errorf("prose became evidence: %+v", tokens)
		}
	}
	for _, source := range []string{"ENOENT: missing file; private follow-up sentence", "TypeError: invalid argument. Private follow-up sentence", "error: disk full; private follow-up sentence"} {
		tokens := extractActivityEvidenceTokens(source, 1)
		if len(tokens) != 1 || tokens[0].Kind != "error" || strings.Contains(tokens[0].Value, "follow-up") || utf8.RuneCountInString(tokens[0].Value) > 80 {
			t.Errorf("unbounded or missing error signature: %+v", tokens)
		}
	}
}

func TestActivityEvidenceChatAndAXNeverExtractErrors(t *testing.T) {
	for _, app := range []string{"Claude", "ChatGPT", "Slack", "Chrome", "Messages", "iTerm2"} {
		event := activitymon.ActivityEvent{Timestamp: 1000, Kind: activitymon.EventKeyboard, AppName: app, TypedText: "ENOENT: a private message\nExplain the private incident", ClickLabel: "Edit"}
		if tokens := activityEventEvidenceTokens(event); len(tokens) != 0 {
			t.Errorf("%s message became error evidence: %+v", app, tokens)
		}
	}
	store := newActivityEvidenceTestStore(t)
	saveTimelineTestSession(t, store, ActivitySessionRow{ID: "review-ax", StartTime: 1000, EndTime: 1000, EventCount: 1, AppNames: `[]`}, []activitymon.ActivityEvent{{Timestamp: 1000, Kind: activitymon.EventClick, AppName: "iTerm2", AppBundleID: "com.googlecode.iterm2", ClickLabel: "Edit"}})
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveSnapshots(tx, []ActivitySnapshotRow{{SessionID: "review-ax", Timestamp: 1000, App: "iTerm2", AXText: "ENOENT: private AX prose\nExplain the private incident", ParsedJSON: `{"body":"Error: private parsed prose"}`}}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	runs, _ := store.GetLastTimelineRuns(1)
	if _, err := store.RefreshTimelineRunEvidence(&runs[0]); err != nil {
		t.Fatal(err)
	}
	saved, _ := store.GetTimelineRunByID(runs[0].ID)
	encoded, _ := json.Marshal(saved.EvidenceTokens)
	if len(saved.EvidenceTokens) != 0 {
		t.Fatalf("AX prose persisted: %s", encoded)
	}
}

func TestActivityEvidencePathsRejectSlashCommandsAndRedactedParents(t *testing.T) {
	for _, input := range []string{"/compact", "/clear", "pick either /or both", "ls /tmp", "/tmp/foo/../../", "/Users/person/src/[REDACTED:email]/x.go", "~/src/[REDACTED:email]/x.go"} {
		if tokens := extractActivityIdentifierTokens(input, 1); len(tokens) != 0 {
			t.Errorf("unsafe path from %q: %+v", input, tokens)
		}
	}
}

func TestActivityEvidenceFigureSeparatorsAndLineBoundaries(t *testing.T) {
	if tokens := extractActivityIdentifierTokens("1,200 MB", 1); !evidenceTokenExists(tokens, "figure", "1200 mb") || evidenceTokenExists(tokens, "figure", "200 mb") {
		t.Fatalf("thousands separator misread: %+v", tokens)
	}
	if tokens := extractActivityIdentifierTokens("Item 9\nB)", 1); len(tokens) != 0 {
		t.Fatalf("number crossed line boundary: %+v", tokens)
	}
}

func TestActivityEvidenceCanonicalizesCurrentHome(t *testing.T) {
	t.Setenv("HOME", "/tmp/evidence-home")
	tilde := extractActivityIdentifierTokens("~/.sample-agent/activity", 1)
	absolute := extractActivityIdentifierTokens("/tmp/evidence-home/.sample-agent/activity", 1)
	if !activityEvidenceTokensEqual(tilde, absolute) {
		t.Fatalf("home aliases differ: %+v vs %+v", tilde, absolute)
	}
}

func TestActivityEvidenceMigrationRemovesLegacyProseAcrossProjections(t *testing.T) {
	store := newActivityEvidenceTestStore(t)
	run := outcomeTestRun(t, store, "legacy", "Claude", "com.anthropic.claudefordesktop", 1000, "~/.sample-agent/records", "Send")
	outcomeTestLink(t, store, "legacy-task", run)
	legacy := []ActivityEvidenceToken{
		{Kind: "error", Value: "Explain the confidential private incident", LastSeenMs: 1000},
		{Kind: "error", Value: "ENOENT: a private chat message", LastSeenMs: 1000},
		{Kind: "path", Value: "/compact", LastSeenMs: 1000},
		{Kind: "path", Value: "~/.sample-agent/records", LastSeenMs: 1000},
	}
	if _, err := store.db.Exec(`UPDATE activity_timeline_groups SET evidence_tokens=? WHERE id=?`, composeJSON(legacy), run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE activity_run_links SET link_reason='same_error',link_token_kind='error',link_token_value=? WHERE run_id=?`, legacy[0].Value, run.ID); err != nil {
		t.Fatal(err)
	}
	evidence := []ActivityOutcomeEvidence{{RunID: run.ID, SourceKind: "activity_event", SourceID: "legacy-event", ObservedAt: 1000, Tokens: legacy[:1]}}
	if _, err := store.db.Exec(`INSERT INTO activity_outcomes(id,activity_id,run_id,status,reason,occurred_at,evidence,created_at) VALUES ('legacy-outcome','legacy-task',?,'done','user_completed',1000,?,1000)`, run.ID, composeJSON(evidence)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE activities SET status='settled' WHERE id='legacy-task'`); err != nil {
		t.Fatal(err)
	}
	for i, token := range []ActivityEvidenceToken{{Kind: "path", Value: "~/src/new-desktop"}, {Kind: "path", Value: "docs/backlog.md"}, {Kind: "figure", Value: "80 %"}} {
		evidence[0].Tokens = []ActivityEvidenceToken{token}
		if _, err := store.db.Exec(`INSERT INTO activity_outcomes(id,activity_id,run_id,status,reason,occurred_at,evidence,created_at) VALUES (?,'legacy-task',?,'done','user_completed',1000,?,1000)`, "weak-"+string(rune('a'+i)), run.ID, composeJSON(evidence)); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		tx, err := store.db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := migrateActivityEvidenceIdentifierPolicy(tx); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	var raw string
	if err := store.db.QueryRow(`SELECT evidence_tokens FROM activity_timeline_groups WHERE id=?`, run.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"Explain", "private", "/compact", "ENOENT"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("unsafe legacy token remains persisted: %s", raw)
		}
	}
	if !strings.Contains(raw, "~/.sample-agent/records") {
		t.Fatalf("safe identifier was lost: %s", raw)
	}
	if items, err := store.GetActivityOutcomes("legacy-task"); err != nil || len(items) != 0 {
		t.Fatalf("unsafe outcome survived: %+v %v", items, err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM activity_outcomes WHERE activity_id='legacy-task'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("invalid receipts remain persisted: count=%d err=%v", count, err)
	}
	activity, _ := store.GetActivityByID("legacy-task")
	if activity.Status != activityStatusActive {
		t.Fatalf("unsafe terminal projection survived: %+v", activity)
	}
	reason, err := store.activityRunLinkReason("legacy-task", run.ID)
	if err != nil || reason == nil || reason.Token != "" || reason.ReasonKind != "task_started" {
		t.Fatalf("legacy link exposed prose: %+v %v", reason, err)
	}
}

func TestActivityLinkReasonRejectsUnsafeAndWeakTokens(t *testing.T) {
	store, _, first, second := composeCorrectionFixture(t)
	for _, token := range []ActivityEvidenceToken{
		{Kind: "error", Value: "Explain a private message"},
		{Kind: "path", Value: "/compact"},
		{Kind: "path", Value: "~/src/new-desktop"},
		{Kind: "path", Value: "docs/backlog.md"},
		{Kind: "figure", Value: "80 %"},
	} {
		if _, err := store.db.Exec(`UPDATE activity_run_links SET link_reason='shared_path',link_token_kind=?,link_token_value=? WHERE run_id=?`, token.Kind, token.Value, second.ID); err != nil {
			t.Fatal(err)
		}
		reason, err := store.activityRunLinkReason("act_"+first.ID, second.ID)
		if err != nil || reason.Token != "" || reason.ReasonKind != "related_context" {
			t.Fatalf("unsafe/weak token reached link explanation: %+v %v", reason, err)
		}
	}
}

func TestActivityOutcomeWeakTokensCannotProveCompletion(t *testing.T) {
	for _, text := range []string{"/compact", "~/src/new-desktop", "docs/backlog.md", "80 %", "Explain the private incident"} {
		tokens := extractActivityIdentifierTokens(text, 1000)
		events := []ActivityTimelineEvent{
			{EventID: "draft", ActivityEvent: activitymon.ActivityEvent{Kind: activitymon.EventKeyboard, Timestamp: 1000, AppName: "Claude", TypedText: "I am done: " + text}},
			{EventID: "send", ActivityEvent: activitymon.ActivityEvent{Kind: activitymon.EventClick, Timestamp: 1100, AppName: "Claude", ClickLabel: "Send"}},
		}
		if outcomes := inferActivityMessageOutcomes("task", ActivityTimelineRunRow{ID: "run"}, tokens, events); len(outcomes) != 0 {
			t.Fatalf("weak token %q proved completion: %+v", text, outcomes)
		}
		if sanitizeActivityOutcomeEvidence(&ActivityOutcome{Status: activityOutcomeDone, Evidence: []ActivityOutcomeEvidence{{Tokens: tokens}}}) {
			t.Fatalf("weak stored tokens from %q proved completion", text)
		}
		before := activityOutcomeObservation{evidence: ActivityOutcomeEvidence{ObservedAt: 1000, Tokens: extractActivityIdentifierTokens(text+" 60GB", 1000)}}
		after := activityOutcomeObservation{evidence: ActivityOutcomeEvidence{ObservedAt: 2000, Tokens: extractActivityIdentifierTokens(text+" 4GB", 2000)}}
		if outcomes := inferActivityChangedTarget("task", ActivityTimelineRunRow{ID: "run"}, []activityOutcomeObservation{before}, []activityOutcomeObservation{after}); len(outcomes) != 0 {
			t.Fatalf("weak target %q proved fixed: %+v", text, outcomes)
		}
		if sanitizeActivityOutcomeEvidence(&ActivityOutcome{Status: activityOutcomeFixed, Evidence: []ActivityOutcomeEvidence{before.evidence, after.evidence}}) {
			t.Fatalf("weak stored target %q proved fixed", text)
		}
	}
}

func TestActivityEvidenceTerminalChatOCRNeverExtractsProse(t *testing.T) {
	for _, test := range []struct{ name, title, parsed string }{
		{"window title", "Claude Code", ""},
		{"parsed title", "Terminal", `{"title":"Codex","contentType":"terminal"}`},
		{"parsed conversation", "Terminal", `{"contentType":"conversation"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := newActivityEvidenceTestStore(t)
			saveTimelineTestSession(t, store, ActivitySessionRow{ID: "terminal-chat", StartTime: 1000, EndTime: 1000, EventCount: 1, AppNames: `[]`}, []activitymon.ActivityEvent{{Timestamp: 1000, Kind: activitymon.EventKeyboard, AppName: "iTerm2", AppBundleID: "com.googlecode.iterm2", WindowTitle: test.title, TypedText: "ENOENT: a private message"}})
			tx, err := store.db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SaveSnapshots(tx, []ActivitySnapshotRow{{SessionID: "terminal-chat", Timestamp: 1000, App: "iTerm2", ParsedJSON: test.parsed}}); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			snaps, _ := store.GetSessionSnapshots("terminal-chat")
			if err := store.SaveOCRFrame(OCRFrameRow{SessionID: "terminal-chat", SegmentID: snaps[0].ID, Text: "ENOENT: a private message"}); err != nil {
				t.Fatal(err)
			}
			runs, _ := store.GetLastTimelineRuns(1)
			if _, err := store.RefreshTimelineRunEvidence(&runs[0]); err != nil {
				t.Fatal(err)
			}
			for _, token := range runs[0].EvidenceTokens {
				if token.Kind != "error" || token.Value != "ENOENT" {
					t.Fatalf("terminal chat retained error prose: %+v", runs[0].EvidenceTokens)
				}
				if _, strong := activityEvidenceWeight(token, nil); strong {
					t.Fatalf("bare terminal-chat keyword became strong: %+v", token)
				}
			}
		})
	}
}
