package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/openanswer"
	"wails-gui/storage"
)

func openAnswerAdmissionApp(t *testing.T) *App {
	t.Helper()
	app := mainChatTestApp(t)
	app.cronScheduler = NewCronScheduler(
		app.store,
		func(string, any) {},
		func(string, string) {},
		nil,
	)
	return app
}

func appendOpenAnswerAdmissionTurn(t *testing.T, app *App, question string) (string, string, string) {
	t.Helper()
	session, err := app.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	const runID = "open-answer-run"
	if _, err := app.store.AppendStreamItem(t.Context(), session.LocalID, storage.StreamItemRow{
		ID: "open-answer-user", Kind: "user", Content: question, RunID: runID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.store.AppendStreamItem(t.Context(), session.LocalID, storage.StreamItemRow{
		ID: "open-answer-assistant", Kind: "assistant", Content: "I will let you know.", RunID: runID,
	}); err != nil {
		t.Fatal(err)
	}
	return session.LocalID, "open-answer-user", "open-answer-assistant"
}

func TestAdmitOpenAnswerWindowEligibility(t *testing.T) {
	now := time.Now()
	base := openanswer.Candidate{
		Question: "When will we know the result?", Topic: "result",
		Settles: "result", Source: "Today News", NotYet: true,
		WindowStart: now.Add(time.Minute), WindowEnd: now.Add(time.Hour),
	}
	for _, tc := range []struct {
		name   string
		change func(*App, *openanswer.Candidate)
		want   bool
	}{
		{name: "named time", want: true},
		{name: "blockless answer", change: func(_ *App, candidate *openanswer.Candidate) {
			candidate.Question = "What is a runoff?"
			candidate.WindowStart = time.Time{}
			candidate.WindowEnd = time.Time{}
		}},
		{name: "window beyond seven days", change: func(_ *App, candidate *openanswer.Candidate) {
			candidate.WindowStart = now.Add(8 * 24 * time.Hour)
			candidate.WindowEnd = candidate.WindowStart.Add(time.Hour)
		}},
		{name: "muted topic", change: func(app *App, candidate *openanswer.Candidate) {
			if err := app.store.SetOpenAnswerKindMuted(t.Context(), candidate.Topic, true); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "Closed Eyes", change: func(app *App, _ *openanswer.Candidate) {
			app.closedEyes.enabled.Store(true)
		}},
		{name: "sensitive source", change: func(_ *App, candidate *openanswer.Candidate) {
			candidate.Source = "com.1password.desktop"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := openAnswerAdmissionApp(t)
			candidate := base
			if tc.change != nil {
				tc.change(app, &candidate)
			}
			sessionID, userID, assistantID := appendOpenAnswerAdmissionTurn(t, app, candidate.Question)
			app.admitOpenAnswer(sessionID, userID, "open-answer-run", assistantID, candidate)
			rows, err := app.store.ListOpenAnswers(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if got := len(rows) == 1; got != tc.want {
				t.Fatalf("admitted %d records, want admitted=%v", len(rows), tc.want)
			}
		})
	}
}

func TestResolveCompletedOpenAnswerAdmissionUsesStreamIdentity(t *testing.T) {
	app := openAnswerAdmissionApp(t)
	question := "When will we know the result?"
	sessionID, userID, assistantID := appendOpenAnswerAdmissionTurn(t, app, question)
	candidate := &openanswer.Candidate{Question: "When will we know the result?", Topic: "election"}
	segment := &finishedSegment{tag: sessionID, id: assistantID}
	got, ok := resolveCompletedOpenAnswerAdmission(userID, "run-id", segment, candidate)
	if !ok {
		t.Fatal("completed open-answer turn was not resolved")
	}
	if got.tag != sessionID || got.userID != userID ||
		got.runID != "run-id" || got.assistantSegmentID != assistantID ||
		got.candidate.Question != candidate.Question {
		t.Fatalf("resolved admission = %+v", got)
	}
}

func TestAdmitOpenAnswerMailBindsHeadersAndRejectsExistingReply(t *testing.T) {
	for _, withReply := range []bool{false, true} {
		name := "sent header"
		if withReply {
			name = "reply already arrived"
		}
		t.Run(name, func(t *testing.T) {
			app := mainChatTestApp(t)
			person, err := app.store.CreatePrincipal(t.Context(), storage.PrincipalRow{
				Kind: storage.PrincipalKindContact, DisplayName: "Priya",
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := app.store.BindAppHandle(t.Context(), person.ID, "email", "priya@example.com", "Priya"); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Second)
			mail := newTestMailService(t, now, map[string]string{"priya@example.com": person.ID})
			mail.store = app.store
			mail.loopCtx = t.Context()
			mail.cancel = func() {}
			mail.lastSync = now
			sent := testHeader{
				ID: "sent-1", From: "Me <me@example.com>", Subject: "Plans",
				At: now.Add(-time.Minute).UnixMilli(), To: []string{"priya@example.com"},
			}
			inbox := []testHeader{}
			if withReply {
				inbox = append(inbox, testHeader{
					ID: "reply-1", From: "Priya <priya@example.com>", Subject: "Re: Plans",
					At: now.Add(-time.Second).UnixMilli(), To: []string{"me@example.com"},
				})
			}
			mail.mail.out = mailSnapshotJSON(t, inbox, []testHeader{sent})
			if _, err := mail.Sync(t.Context(), "initial"); err != nil {
				t.Fatal(err)
			}
			if !withReply {
				if _, err := mail.Sync(t.Context(), "confirm empty inbox"); err != nil {
					t.Fatal(err)
				}
			}
			app.mailSync = mail.mailSyncService

			question := `Has Priya replied to "Plans"?`
			sessionID, userID, assistantID := appendOpenAnswerAdmissionTurn(t, app, question)
			candidate := openanswer.Candidate{
				Question: question, Topic: "Priya", Settles: "reply",
				Source: "Mail", NotYet: true, WatchedEvent: "a reply from Priya to Plans",
			}
			app.admitOpenAnswer(sessionID, userID, "open-answer-run", assistantID, candidate)
			rows, err := app.store.ListOpenAnswers(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if withReply {
				if len(rows) != 0 {
					t.Fatalf("admitted despite existing reply: %+v", rows)
				}
				return
			}
			if len(rows) != 1 {
				t.Fatalf("admitted %d records, want 1", len(rows))
			}
			if rows[0].MailAnchorMessageID != mailMessageKey(storage.MailboxSent, "sent-1") ||
				rows[0].MailCorrespondent != "priya@example.com" {
				t.Fatalf("mail binding = %+v", rows[0])
			}
			var event map[string]string
			if err := json.Unmarshal([]byte(rows[0].WatchedEvent), &event); err != nil {
				t.Fatal(err)
			}
			if event["who"] != "Priya" || event["label"] != "Plans" ||
				strings.Contains(rows[0].WatchedEvent, "body") {
				t.Fatalf("watched event includes unexpected data: %q", rows[0].WatchedEvent)
			}
		})
	}
}
