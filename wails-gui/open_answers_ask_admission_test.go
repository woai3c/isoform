package main

import (
	"context"
	"testing"
	"time"

	"wails-gui/internal/openanswer"
	"wails-gui/storage"
	"yansu-agent/agent"
)

type openAnswerAskPromotionRunner struct {
	upcomingCaptureRunner
}

func (openAnswerAskPromotionRunner) ExecuteTask(_ context.Context, _ *agent.TaskInput, emit agent.StreamCallback) (string, error) {
	const runID = "ask-promotion-send"
	for _, event := range []*agent.StreamEvent{
		{Type: "run_created", RunID: runID},
		{Type: "done", RunID: runID},
	} {
		if err := emit(event); err != nil {
			return "", err
		}
	}
	return runID, nil
}

func addAskTestOpenRows(t *testing.T, app *App, sessionID, prefix string, count int, now time.Time) {
	t.Helper()
	for i := 0; i < count; i++ {
		id := prefix + string(rune('a'+i))
		err := app.store.CreateOpenAnswer(t.Context(), storage.OpenAnswerRow{
			ID: id, ChatSessionID: sessionID, AssistantMessageID: "assistant-" + id,
			Question: "question", Topic: id, Settles: "result", Source: "Today News",
			WindowStart: now.Add(time.Hour).UnixMilli(), WindowEnd: now.Add(2 * time.Hour).UnixMilli(),
			Delivery: "around", ExpiresAt: now.Add(24 * time.Hour).UnixMilli(),
			State: "open", CreatedAt: now.UnixMilli(),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func askTestAnswer(sessionID, assistantID string, now time.Time, state string) storage.OpenAnswerRow {
	return storage.OpenAnswerRow{
		ID: "ask-record", ChatSessionID: sessionID, AssistantMessageID: assistantID,
		Question: "When will we know the result?", Topic: "election", Settles: "result",
		Source: "Today News", WindowStart: now.Add(time.Hour).UnixMilli(),
		WindowEnd: now.Add(2 * time.Hour).UnixMilli(), Delivery: "around",
		ExpiresAt: now.Add(24 * time.Hour).UnixMilli(), State: state, CreatedAt: now.UnixMilli(),
	}
}

func createOpenAnswerAskTestRow(t *testing.T, app *App, sessionID, id string, now time.Time) storage.OpenAnswerRow {
	t.Helper()
	userID := id + "-source-user"
	assistantID := id + "-source-assistant"
	runID := id + "-source-run"
	question := "When will we know the result?"
	const answerText = "I will let you know."
	for _, item := range []storage.StreamItemRow{
		{ID: userID, Kind: "user", Content: question, RunID: runID, CreatedAt: now.UnixMilli()},
		{ID: assistantID, Kind: "assistant", Content: answerText, RunID: runID, CreatedAt: now.Add(time.Second).UnixMilli()},
	} {
		if _, err := app.store.AppendStreamItem(t.Context(), sessionID, item); err != nil {
			t.Fatal(err)
		}
	}
	row := askTestAnswer(sessionID, assistantID, now, "ask")
	row.ID = id
	revision, err := app.store.ReadRevision(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := app.store.CreateOpenAnswerAskForTurnIfCurrent(t.Context(), row, storage.OpenAnswerTurn{
		UserID: userID, UserContent: question, AnswerContent: answerText, RunID: runID,
	}, revision); err != nil {
		t.Fatal(err)
	}
	return row
}

func appendOpenAnswerAskSend(t *testing.T, app *App, sessionID, userID string) {
	t.Helper()
	if _, err := app.store.AppendStreamItem(t.Context(), sessionID, storage.StreamItemRow{
		ID: userID, Kind: "user", Content: openAnswerAskSendEnglish,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAdmitOpenAnswerAtFiveStoresAskWithoutCheckJob(t *testing.T) {
	app := openAnswerAdmissionApp(t)
	now := time.Now()
	candidate := openanswer.Candidate{
		Question: "When will we know the result?", Topic: "election",
		Settles: "result", Source: "Today News", NotYet: true,
		WindowStart: now.Add(time.Hour), WindowEnd: now.Add(2 * time.Hour),
	}
	sessionID, userID, assistantID := appendOpenAnswerAdmissionTurn(t, app, candidate.Question)
	addAskTestOpenRows(t, app, sessionID, "prior-", 5, now)
	app.admitOpenAnswer(sessionID, userID, "open-answer-run", assistantID, candidate)
	rows, err := app.store.ListOpenAnswers(t.Context())
	if err != nil || len(rows) != 5 {
		t.Fatalf("open rows = %d, error = %v", len(rows), err)
	}
	associations, err := app.store.OpenAnswersForMessages(t.Context(), []string{assistantID})
	if err != nil || associations[assistantID].Answer.State != "ask" {
		t.Fatalf("ask association = %+v, error = %v", associations[assistantID], err)
	}
	if view := openAnswerView(associations[assistantID].Answer); view.State != "ask" {
		t.Fatalf("projected state = %q", view.State)
	}
	jobs, err := app.store.ListCronJobs(t.Context())
	if err != nil || len(jobs) != 0 {
		t.Fatalf("ask created check jobs = %+v, error = %v", jobs, err)
	}
}

func TestOpenAnswerLimitRaceFallsBackToAsk(t *testing.T) {
	app := openAnswerAdmissionApp(t)
	now := time.Now()
	sessionID, userID, assistantID := appendOpenAnswerAdmissionTurn(t, app, "When will we know the result?")
	addAskTestOpenRows(t, app, sessionID, "prior-", 5, now)
	row := askTestAnswer(sessionID, assistantID, now, "open")
	turn := storage.OpenAnswerTurn{
		UserID: userID, UserContent: row.Question, AnswerContent: "I will let you know.",
		RunID: "open-answer-run",
	}
	revision, err := app.store.ReadRevision(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	state, err := app.createOpenAnswerWithAskFallback(t.Context(), row, turn, revision)
	if err != nil || state != "ask" {
		t.Fatalf("race fallback state=%q error=%v", state, err)
	}
}

func TestOpenAnswerAskPromotionCreatesCheckAfterCancellation(t *testing.T) {
	app := openAnswerAdmissionApp(t)
	now := time.Now()
	sessionID, userID, assistantID := appendOpenAnswerAdmissionTurn(t, app, "When will we know the result?")
	addAskTestOpenRows(t, app, sessionID, "prior-", 5, now)
	row := askTestAnswer(sessionID, assistantID, now, "ask")
	revision, err := app.store.ReadRevision(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	turn := storage.OpenAnswerTurn{
		UserID: userID, UserContent: row.Question, AnswerContent: "I will let you know.",
		RunID: "open-answer-run",
	}
	if err := app.store.CreateOpenAnswerAskForTurnIfCurrent(t.Context(), row, turn, revision); err != nil {
		t.Fatal(err)
	}
	if err := app.store.CancelOpenAnswer(t.Context(), "prior-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.store.AppendStreamItem(t.Context(), sessionID, storage.StreamItemRow{
		ID: "let-me-know", Kind: "user", Content: openAnswerAskSendEnglish,
	}); err != nil {
		t.Fatal(err)
	}
	app.promoteOpenAnswerAskAfterTurn(sessionID, "let-me-know")
	promoted, err := app.store.GetOpenAnswer(t.Context(), row.ID)
	if err != nil || promoted.State != "open" {
		t.Fatalf("promoted ask = %+v, error = %v", promoted, err)
	}
	jobs, err := app.store.ListCronJobs(t.Context())
	if err != nil || len(jobs) != 1 {
		t.Fatalf("promoted check jobs = %+v, error = %v", jobs, err)
	}
}

func TestOpenAnswerLetMeKnowPromotesTheSelectedOlderAsk(t *testing.T) {
	app := openAnswerAdmissionApp(t)
	session, err := app.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	older := createOpenAnswerAskTestRow(t, app, session.LocalID, "older-ask", now)
	newer := createOpenAnswerAskTestRow(t, app, session.LocalID, "newer-ask", now.Add(2*time.Second))
	appendOpenAnswerAskSend(t, app, session.LocalID, "let-me-know")

	if err := app.SelectOpenAnswerAsk(older.ID); err != nil {
		t.Fatal(err)
	}
	app.promoteOpenAnswerAskAfterTurn(session.LocalID, "let-me-know")

	promoted, err := app.store.GetOpenAnswer(t.Context(), older.ID)
	if err != nil || promoted.State != "open" {
		t.Fatalf("selected older ask = %+v, error = %v", promoted, err)
	}
	untouched, err := app.store.GetOpenAnswer(t.Context(), newer.ID)
	if err != nil || untouched.State != "ask" {
		t.Fatalf("newer ask = %+v, error = %v", untouched, err)
	}
}

func TestOpenAnswerSelectAskRejectsNonAsk(t *testing.T) {
	app := openAnswerAdmissionApp(t)
	session, err := app.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	addAskTestOpenRows(t, app, session.LocalID, "prior-", 1, time.Now())

	if err := app.SelectOpenAnswerAsk("prior-a"); err == nil {
		t.Fatal("selecting an open answer succeeded")
	}
}

func TestOpenAnswerSelectAskRejectsWhenClosedEyesEnabled(t *testing.T) {
	app := openAnswerAdmissionApp(t)
	session, err := app.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	row := createOpenAnswerAskTestRow(t, app, session.LocalID, "ask-record", time.Now())
	app.closedEyes.enabled.Store(true)

	if err := app.SelectOpenAnswerAsk(row.ID); err == nil {
		t.Fatal("selecting an ask while Closed Eyes is enabled succeeded")
	}
}

func TestOpenAnswerTypedLetMeKnowPromotesLatestAsk(t *testing.T) {
	app := openAnswerAdmissionApp(t)
	session, err := app.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	older := createOpenAnswerAskTestRow(t, app, session.LocalID, "older-ask", now)
	newer := createOpenAnswerAskTestRow(t, app, session.LocalID, "newer-ask", now.Add(2*time.Second))
	appendOpenAnswerAskSend(t, app, session.LocalID, "let-me-know")

	app.promoteOpenAnswerAskAfterTurn(session.LocalID, "let-me-know")

	oldRow, err := app.store.GetOpenAnswer(t.Context(), older.ID)
	if err != nil || oldRow.State != "ask" {
		t.Fatalf("older ask = %+v, error = %v", oldRow, err)
	}
	newRow, err := app.store.GetOpenAnswer(t.Context(), newer.ID)
	if err != nil || newRow.State != "open" {
		t.Fatalf("latest ask = %+v, error = %v", newRow, err)
	}
}

func TestOpenAnswerSelectedAskMustPrecedeLetMeKnowTurn(t *testing.T) {
	app := openAnswerAdmissionApp(t)
	session, err := app.ensureMainChat(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	older := createOpenAnswerAskTestRow(t, app, session.LocalID, "older-ask", now)
	appendOpenAnswerAskSend(t, app, session.LocalID, "let-me-know")
	later := createOpenAnswerAskTestRow(t, app, session.LocalID, "later-ask", now.Add(2*time.Second))

	if err := app.SelectOpenAnswerAsk(later.ID); err != nil {
		t.Fatal(err)
	}
	app.promoteOpenAnswerAskAfterTurn(session.LocalID, "let-me-know")

	for _, id := range []string{older.ID, later.ID} {
		row, err := app.store.GetOpenAnswer(t.Context(), id)
		if err != nil || row.State != "ask" {
			t.Fatalf("ask %s = %+v, error = %v", id, row, err)
		}
	}
}

func TestSendPromptPromotesAskOnlyForExactText(t *testing.T) {
	for _, tc := range []struct {
		name, text, wantState string
	}{
		{name: "English", text: openAnswerAskSendEnglish, wantState: "open"},
		{name: "Simplified Chinese", text: openAnswerAskSendChinese, wantState: "open"},
		{name: "nonmatching", text: "Tell me when it settles.", wantState: "ask"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, sessionID := topicChatTestApp(t)
			app.cronScheduler = NewCronScheduler(app.store, func(string, any) {}, func(string, string) {}, nil)
			app.runner = &openAnswerAskPromotionRunner{}

			sourceAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
			sourceUser := storage.StreamItemRow{
				ID: "ask-source-user", Kind: "user", Content: "When will we know the result?",
				RunID: "ask-source-run", CreatedAt: sourceAt.UnixMilli(),
			}
			sourceAssistant := storage.StreamItemRow{
				ID: "ask-source-assistant", Kind: "assistant", Content: "I will let you know.",
				RunID: "ask-source-run", CreatedAt: sourceAt.Add(time.Second).UnixMilli(),
			}
			for _, row := range []storage.StreamItemRow{sourceUser, sourceAssistant} {
				if _, err := app.store.AppendStreamItem(t.Context(), sessionID, row); err != nil {
					t.Fatal(err)
				}
			}
			now := time.Now()
			answer := askTestAnswer(sessionID, sourceAssistant.ID, now, "ask")
			revision, err := app.store.ReadRevision(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			turn := storage.OpenAnswerTurn{
				UserID: sourceUser.ID, UserContent: sourceUser.Content,
				AnswerContent: sourceAssistant.Content, RunID: sourceUser.RunID,
			}
			if err := app.store.CreateOpenAnswerAskForTurnIfCurrent(t.Context(), answer, turn, revision); err != nil {
				t.Fatal(err)
			}

			userID := "ask-promotion-user"
			if _, err := app.store.AppendStreamItem(t.Context(), sessionID, storage.StreamItemRow{
				ID: userID, Kind: "user", Content: tc.text,
			}); err != nil {
				t.Fatal(err)
			}
			if err := app.SendPrompt(tc.text, "", "m", "", sessionID, false, false, "", "", "", "en", ""); err != nil {
				t.Fatal(err)
			}
			waitForTagIdle(t, app, sessionID)

			deadline := time.Now().Add(5 * time.Second)
			for {
				promoted, err := app.store.GetOpenAnswer(t.Context(), answer.ID)
				if err != nil {
					t.Fatal(err)
				}
				if promoted.State == tc.wantState {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("ask state = %q, want %q", promoted.State, tc.wantState)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}
