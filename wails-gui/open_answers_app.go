package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"

	persistencesvc "wails-gui/internal/persistence"
	"wails-gui/storage"
)

const openAnswersChangedEvent = "open-answers:changed"

type openAnswerAskSelection struct {
	askID     string
	expiresAt time.Time
}

func openAnswerTime(ms int64) string {
	if ms == 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339Nano)
}

// openAnswerView keeps the UI contract independent of the SQLite schema.
func openAnswerView(row storage.OpenAnswerRow) persistencesvc.OpenAnswerData {
	view := persistencesvc.OpenAnswerData{
		ID: row.ID, SessionID: row.ChatSessionID, MessageID: row.AssistantMessageID,
		Question: row.Question, Topic: row.Topic, Kind: row.Topic, Settles: row.Settles,
		Source: row.Source, WindowStart: openAnswerTime(row.WindowStart), WindowEnd: openAnswerTime(row.WindowEnd),
		Delivery: row.Delivery, ExpiresAt: openAnswerTime(row.ExpiresAt), Posts: row.Posts,
		State: row.State, SawAt: openAnswerTime(row.SawAt), CreatedAt: openAnswerTime(row.CreatedAt), Learned: row.LearnedAt > 0,
	}
	if row.WatchedEvent != "" {
		event := persistencesvc.OpenAnswerEvent{}
		if json.Unmarshal([]byte(row.WatchedEvent), &event) != nil || strings.TrimSpace(event.Label) == "" {
			event = persistencesvc.OpenAnswerEvent{Label: row.WatchedEvent}
		}
		view.WatchedEvent = &event
	}
	return view
}

func (a *App) ListOpenAnswers() ([]persistencesvc.OpenAnswerData, error) {
	if a.store == nil {
		return nil, errors.New("storage unavailable")
	}
	rows, err := a.store.ListOpenAnswers(context.Background())
	if err != nil {
		return nil, err
	}
	views := make([]persistencesvc.OpenAnswerData, 0, len(rows))
	for _, row := range rows {
		views = append(views, openAnswerView(row))
	}
	return views, nil
}

func (a *App) SelectOpenAnswerAsk(id string) error {
	if a == nil || a.store == nil {
		return errors.New("storage unavailable")
	}
	if a.IsClosedEyesModeEnabled() {
		return errors.New("Closed Eyes is enabled")
	}
	row, err := a.store.GetOpenAnswer(context.Background(), id)
	if err != nil {
		return err
	}
	if row.State != "ask" {
		return errors.New("open answer is not an ask")
	}
	a.openAnswerAskSelectionMu.Lock()
	if a.openAnswerAskSelections == nil {
		a.openAnswerAskSelections = make(map[string]openAnswerAskSelection)
	}
	a.openAnswerAskSelections[row.ChatSessionID] = openAnswerAskSelection{
		askID: row.ID, expiresAt: time.Now().Add(2 * time.Minute),
	}
	a.openAnswerAskSelectionMu.Unlock()
	return nil
}

func (a *App) takeOpenAnswerAskSelection(sessionID string) (string, bool) {
	a.openAnswerAskSelectionMu.Lock()
	defer a.openAnswerAskSelectionMu.Unlock()
	selection, ok := a.openAnswerAskSelections[sessionID]
	if ok {
		delete(a.openAnswerAskSelections, sessionID)
	}
	if !ok || !time.Now().Before(selection.expiresAt) {
		return "", false
	}
	return selection.askID, true
}

func (a *App) ChangeOpenAnswer(id, delivery string) (*persistencesvc.OpenAnswerData, error) {
	if a.store == nil {
		return nil, errors.New("storage unavailable")
	}
	ctx := context.Background()
	if err := a.store.ChangeOpenAnswer(ctx, id, delivery); err != nil {
		return nil, err
	}
	row, err := a.store.GetOpenAnswer(ctx, id)
	if err != nil {
		return nil, err
	}
	if row.WindowStart > 0 {
		a.deleteOpenAnswerCheck(id)
		if err := a.ensureOpenAnswerCheck(row, ""); err != nil {
			log.Printf("[open-answer] recreate check for %s: %v", id, err)
		}
	}
	a.emitBrowserEvent(openAnswersChangedEvent)
	view := openAnswerView(row)
	return &view, nil
}

func (a *App) CancelOpenAnswer(id string) (*persistencesvc.OpenAnswerData, error) {
	if a.store == nil {
		return nil, errors.New("storage unavailable")
	}
	ctx := context.Background()
	if err := a.store.CancelOpenAnswer(ctx, id); err != nil {
		return nil, err
	}
	a.deleteOpenAnswerCheck(id)
	a.emitBrowserEvent(openAnswersChangedEvent)
	row, err := a.store.GetOpenAnswer(ctx, id)
	if err != nil {
		return nil, err
	}
	view := openAnswerView(row)
	return &view, nil
}

func (a *App) NotForMeAnswer(messageID string) error {
	if a.store == nil {
		return errors.New("storage unavailable")
	}
	ctx := context.Background()
	row, isPost, err := a.store.OpenAnswerForMessage(ctx, messageID)
	if err != nil {
		return err
	}
	if !isPost {
		return errors.New("message is not an answer post")
	}
	if err := a.store.SetOpenAnswerKindMuted(ctx, row.Topic, true); err != nil {
		return err
	}
	a.emitBrowserEvent(openAnswersChangedEvent)
	return nil
}

func (a *App) UndoNotForMeAnswer(kind string) error {
	if a.store == nil {
		return errors.New("storage unavailable")
	}
	if err := a.store.SetOpenAnswerKindMuted(context.Background(), kind, false); err != nil {
		return err
	}
	a.emitBrowserEvent(openAnswersChangedEvent)
	return nil
}

func (a *App) learnUnopenedOpenAnswerKinds(ctx context.Context, kind string, now time.Time) {
	if a == nil || a.store == nil {
		return
	}
	learned, err := a.store.LearnOpenAnswerKindsAfterUnopenedPosts(ctx, kind, now)
	if err != nil {
		log.Printf("[open-answer] evaluate unopened posts: %v", err)
		return
	}
	for _, post := range learned {
		a.emitBrowserEvent(openAnswersChangedEvent)
		a.emitOpenAnswerMessageReload(post.SessionID, post.MessageID, post.SortOrder)
	}
}

// projectOpenAnswers derives cards from durable associations, never from
// frontend-supplied card metadata. Missing associations are ordinary messages.
func (a *App) projectOpenAnswers(items []StreamItemData) {
	if a.store == nil {
		return
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if item.Kind == "assistant" {
			ids = append(ids, item.ID)
		}
	}
	associations, err := a.store.OpenAnswersForMessages(context.Background(), ids)
	if err != nil {
		return
	}
	for i := range items {
		association, ok := associations[items[i].ID]
		if !ok || items[i].Kind != "assistant" {
			continue
		}
		view := openAnswerView(association.Answer)
		items[i].OpenAnswer = &view
		if association.Post {
			items[i].CardType = "answer_ready"
		}
	}
}
