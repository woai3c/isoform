package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"wails-gui/internal/openanswer"
	"wails-gui/internal/persistence"
	"wails-gui/storage"
)

const (
	openAnswerAskSendEnglish = "Let me know when it's settled."
	openAnswerAskSendChinese = "有结果时告诉我。"
)

func (a *App) bindOpenAnswerMailReplyTurn(ctx context.Context, sessionID, userID, runID string, candidate openanswer.Candidate, now time.Time) (openAnswerMailSnapshot, openAnswerMailBinding, error) {
	if a.mailSync == nil {
		return openAnswerMailSnapshot{}, openAnswerMailBinding{}, errOpenAnswerMailUnavailable
	}
	snapshot, err := a.mailSync.openAnswerSnapshot(ctx, now)
	if err != nil {
		return openAnswerMailSnapshot{}, openAnswerMailBinding{}, err
	}
	binding, err := bindMailReplyTurn(ctx, a.store, snapshot, sessionID, userID, runID, candidate, now)
	return snapshot, binding, err
}

func (a *App) admitOpenAnswer(sessionID, userID, runID, assistantID string, candidate openanswer.Candidate) {
	if a == nil || a.store == nil || sessionID == "" || userID == "" || runID == "" || assistantID == "" ||
		a.IsClosedEyesModeEnabled() || !openAnswerCandidateTextAllowed(candidate) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	muted, err := a.store.OpenAnswerKindMuted(ctx, candidate.Topic)
	if err != nil || muted {
		return
	}
	user, err := a.store.LoadStreamItem(ctx, userID)
	if err != nil || user == nil || user.SessionLocalID != sessionID || user.Kind != "user" || user.ToolName != "" ||
		user.RunID != runID || user.Content != candidate.Question {
		return
	}
	answer, err := a.store.LoadStreamItem(ctx, assistantID)
	if err != nil || answer == nil || answer.SessionLocalID != sessionID || answer.Kind != "assistant" ||
		answer.ToolName != "" || answer.RunID != runID || strings.TrimSpace(answer.Content) == "" {
		return
	}
	now := time.Now()
	sourceCheckable := false
	var mailSnapshot openAnswerMailSnapshot
	var mailBinding openAnswerMailBinding
	if candidate.Settles == "reply" && strings.EqualFold(candidate.Source, "Mail") {
		mailSnapshot, mailBinding, err = a.bindOpenAnswerMailReplyTurn(ctx, sessionID, userID, runID, candidate, now)
		if err != nil || mailBinding.reply != nil {
			return
		}
		sourceCheckable = true
	} else if (candidate.Settles == "result" || candidate.Settles == "status") &&
		!candidate.WindowStart.IsZero() && !strings.EqualFold(candidate.Source, "Mail") {
		sourceCheckable = a.dailyTopicSearchAllowed()
	}
	open, err := a.store.ListOpenAnswers(ctx)
	if err != nil {
		return
	}
	state := "open"
	switch openanswer.CheckEligibility(candidate, now, sourceCheckable, false, len(open)) {
	case openanswer.Eligible:
	case openanswer.AskAtLimit:
		state = "ask"
	default:
		return
	}
	revision := mailSnapshot.revision
	if !strings.EqualFold(candidate.Source, "Mail") {
		revision, err = a.store.ReadRevision(ctx)
		if err != nil {
			return
		}
	}
	watchedEvent := ""
	anchorID, correspondent := "", ""
	if strings.EqualFold(candidate.Source, "Mail") {
		event, marshalErr := json.Marshal(persistence.OpenAnswerEvent{
			Who:   strings.TrimSpace(mailBinding.person.DisplayName),
			Label: mailBinding.anchor.Subject,
		})
		if marshalErr != nil || strings.TrimSpace(mailBinding.person.DisplayName) == "" {
			return
		}
		watchedEvent = string(event)
		anchorID = mailBinding.anchor.ID
		correspondent = mailBinding.correspondent
	}
	createdAt := now.UnixMilli()
	windowStart, windowEnd := int64(0), int64(0)
	if !candidate.WindowStart.IsZero() {
		windowStart = candidate.WindowStart.UnixMilli()
	}
	if !candidate.WindowEnd.IsZero() {
		windowEnd = candidate.WindowEnd.UnixMilli()
	}
	row := storage.OpenAnswerRow{
		ID: uuid.NewString(), ChatSessionID: sessionID, AssistantMessageID: assistantID,
		Question: candidate.Question, Topic: candidate.Topic, Settles: candidate.Settles,
		Source: candidate.Source, WindowStart: windowStart,
		WindowEnd: windowEnd, WatchedEvent: watchedEvent,
		MailAnchorMessageID: anchorID, MailCorrespondent: correspondent,
		Delivery: "around", ExpiresAt: openanswer.Expiry(candidate, now).UnixMilli(),
		State: state, CreatedAt: createdAt,
	}
	turn := storage.OpenAnswerTurn{
		UserID: user.ID, UserContent: user.Content, AnswerContent: answer.Content, RunID: runID,
		UserAutomationContext: user.AutomationContext, UserImages: user.Images, UserFiles: user.Files,
		AnswerAutomationContext: answer.AutomationContext, AnswerImages: answer.Images, AnswerFiles: answer.Files,
	}
	state, err = a.createOpenAnswerWithAskFallback(ctx, row, turn, revision)
	if err != nil {
		return
	}
	if state == "open" && row.WindowStart > 0 {
		if err := a.ensureOpenAnswerCheck(row, ""); err != nil {
			_ = a.store.CancelOpenAnswer(ctx, row.ID)
			return
		}
	}
	a.emitBrowserEvent("open-answers:changed")
	a.emitOpenAnswerMessageReload(sessionID, assistantID, answer.SortOrder)
}

func (a *App) createOpenAnswerWithAskFallback(ctx context.Context, row storage.OpenAnswerRow, turn storage.OpenAnswerTurn, revision storage.ReadRevision) (string, error) {
	if row.State == "ask" {
		return row.State, a.store.CreateOpenAnswerAskForTurnIfCurrent(ctx, row, turn, revision)
	}
	err := a.store.CreateOpenAnswerForTurnIfCurrent(ctx, row, turn, revision)
	if !errors.Is(err, storage.ErrOpenAnswerLimit) || row.State != "open" {
		return row.State, err
	}
	row.State = "ask"
	return row.State, a.store.CreateOpenAnswerAskForTurnIfCurrent(ctx, row, turn, revision)
}

func (a *App) emitOpenAnswerMessageReload(sessionID, messageID string, sortOrder int) {
	a.emitBrowserEvent(mainChatItemsAppendedEvent, map[string]any{
		"sessionId": sessionID, "itemId": messageID, "sortOrder": sortOrder,
		"kind": "assistant", "source": "open_answer",
	})
}

func (a *App) scheduleOpenAnswerAskPromotion(sessionID, userID, text string) {
	if isOpenAnswerAskSend(text) {
		go a.promoteOpenAnswerAskAfterTurn(sessionID, userID)
	}
}

func isOpenAnswerAskSend(text string) bool {
	switch strings.TrimSpace(text) {
	case openAnswerAskSendEnglish, openAnswerAskSendChinese:
		return true
	default:
		return false
	}
}

func (a *App) promoteOpenAnswerAskAfterTurn(sessionID, userID string) {
	if a == nil {
		return
	}
	askID, selected := a.takeOpenAnswerAskSelection(sessionID)
	if a.store == nil || sessionID == "" || userID == "" || a.IsClosedEyesModeEnabled() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var answer storage.OpenAnswerRow
	var found bool
	var err error
	if selected {
		answer, found, err = a.store.OpenAnswerAskForTurnByID(ctx, sessionID, userID, askID)
	} else {
		answer, found, err = a.store.OpenAnswerAskForTurn(ctx, sessionID, userID)
	}
	if err != nil || !found || a.IsClosedEyesModeEnabled() {
		return
	}
	candidate := openanswer.Candidate{
		Question: answer.Question, Topic: answer.Topic, Settles: answer.Settles,
		Source: answer.Source, WatchedEvent: answer.WatchedEvent,
	}
	if !openAnswerCandidateTextAllowed(candidate) {
		return
	}
	var revisions []storage.ReadRevision
	if strings.EqualFold(answer.Source, "Mail") {
		sourceSession, sourceUser, sourceRun, ok, err := a.store.OpenAnswerSourceTurn(ctx, answer.ID)
		if err != nil || !ok || sourceSession != sessionID {
			return
		}
		snapshot, binding, err := a.bindOpenAnswerMailReplyTurn(ctx, sourceSession, sourceUser, sourceRun, candidate, time.Now())
		if err != nil || binding.reply != nil {
			return
		}
		revisions = append(revisions, snapshot.revision)
	}
	now := time.Now()
	if err := a.store.PromoteOpenAnswerAsk(ctx, answer.ID, now, revisions...); err != nil {
		return
	}
	answer.State = "open"
	if answer.WindowStart > 0 {
		if err := a.ensureOpenAnswerCheck(answer, ""); err != nil {
			_ = a.store.CancelOpenAnswer(ctx, answer.ID)
			return
		}
	}
	message, err := a.store.LoadStreamItem(ctx, answer.AssistantMessageID)
	if err != nil || message == nil {
		return
	}
	a.emitBrowserEvent("open-answers:changed")
	a.emitOpenAnswerMessageReload(answer.ChatSessionID, answer.AssistantMessageID, message.SortOrder)
}
