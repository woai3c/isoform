package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"wails-gui/internal/pii"
	"wails-gui/storage"
)

// The pointer contains identities and source proofs only; answer text stays in
// the existing Chat rows, so deleting a row also removes its reusable answer.
type panelAskProof struct {
	Ref           TodayChatContextRef   `json:"ref"`
	ItemDigest    string                `json:"itemDigest"`
	Related       []TodayChatContextRef `json:"related,omitempty"`
	RelatedDigest string                `json:"relatedDigest,omitempty"`
}
type panelAnswerPointer struct {
	UserID   string `json:"userId"`
	AnswerID string `json:"answerId"`
	RunID    string `json:"runId"`
	Order    int    `json:"order"`
}
type TodayPanelAnswer struct {
	Question     string `json:"question"`
	Answer       string `json:"answer"`
	UserItemID   string `json:"userItemId"`
	AnswerItemID string `json:"answerItemId"`
	MadeAt       int64  `json:"madeAt"`
}

var panelAnswerMu sync.Mutex

func panelAskCanSave(ref TodayChatContextRef) bool {
	return ref.SourceKind == todayChatBriefCardKind || ref.SourceKind == "brief" && ref.ParagraphDigest != "" || ref.SourceKind == "calendar" && (ref.MeetingPrep || ref.MeetingNote)
}

func panelAnswerKey(ref TodayChatContextRef) string {
	parts := []any{ref.SourceKind, ref.BriefID, ref.SourceID, ref.ParagraphDigest, ref.MeetingPrep}
	if ref.WeekAsk {
		parts = append(parts, "week_ask")
	}
	if ref.MeetingNote {
		parts = append(parts, "meeting_note")
	}
	if ref.BriefDraft {
		parts = append(parts, "brief_draft")
	}
	raw, _ := json.Marshal(parts)
	sum := sha256.Sum256(raw)
	return "today_panel_answer:" + hex.EncodeToString(sum[:])
}

func (a *App) panelAskMetadata(ctx context.Context, existing string, ref TodayChatContextRef, item todayChatItem, result todayChatContextResult, sources todayChatSources) (string, error) {
	proof := panelAskProof{Ref: TodayChatContextRef{SourceKind: ref.SourceKind, SourceID: ref.SourceID, BriefID: ref.BriefID, ParagraphDigest: ref.ParagraphDigest, WeekAsk: ref.WeekAsk, MeetingPrep: ref.MeetingPrep, MeetingNote: ref.MeetingNote, BriefDraft: ref.BriefDraft}, ItemDigest: proactiveVoiceHash(item)}
	seen := map[string]bool{}
	for _, related := range result.topicPackedRefs {
		key := related.SourceKind + ":" + related.SourceID
		if !seen[key] {
			proof.Related = append(proof.Related, related)
			seen[key] = true
		}
	}
	if len(proof.Related) > 0 {
		var err error
		proof.RelatedDigest, err = a.chatTopicFingerprintFromSources(ctx, proof.Related, sources, map[string]topicChatFingerprintInput{})
		if err != nil {
			return "", err
		}
	}
	fields := map[string]json.RawMessage{}
	if existing != "" && json.Unmarshal([]byte(existing), &fields) != nil {
		return "", errTodayChatItemUnavailable
	}
	raw, err := json.Marshal(proof)
	if err != nil {
		return "", err
	}
	fields["panelAsk"] = raw
	metadata, err := json.Marshal(fields)
	return string(metadata), err
}

func panelAskProofFromRow(row *storage.StreamItemRow) (panelAskProof, bool) {
	var fields struct {
		PanelAsk panelAskProof `json:"panelAsk"`
	}
	if row == nil || json.Unmarshal([]byte(row.AutomationContext), &fields) != nil || !panelAskCanSave(fields.PanelAsk.Ref) || fields.PanelAsk.ItemDigest == "" {
		return panelAskProof{}, false
	}
	return fields.PanelAsk, true
}

// A panel Ask registers before its stream starts and remains pending until
// the durable answer row is saved. Reopening waits instead of asking twice.
type pendingPanelAnswerKey struct {
	app *App
	key string
}
type pendingPanelAnswer struct {
	userID string
	done   chan struct{}
	once   sync.Once
}

func (p *pendingPanelAnswer) finish() { p.once.Do(func() { close(p.done) }) }

var pendingPanelAnswersMu sync.Mutex
var pendingPanelAnswers = map[pendingPanelAnswerKey]*pendingPanelAnswer{}

func (a *App) beginPanelAskAnswer(ref TodayChatContextRef, userID string) {
	if !panelAskCanSave(ref) {
		return
	}
	key := pendingPanelAnswerKey{app: a, key: panelAnswerKey(ref)}
	pendingPanelAnswersMu.Lock()
	if previous := pendingPanelAnswers[key]; previous != nil {
		previous.finish()
	}
	pendingPanelAnswers[key] = &pendingPanelAnswer{userID: userID, done: make(chan struct{})}
	pendingPanelAnswersMu.Unlock()
}

func (a *App) cancelPanelAskAnswer(userID string) {
	pendingPanelAnswersMu.Lock()
	defer pendingPanelAnswersMu.Unlock()
	for key, pending := range pendingPanelAnswers {
		if key.app == a && pending.userID == userID {
			delete(pendingPanelAnswers, key)
			pending.finish()
		}
	}
}

func (a *App) schedulePanelAskAnswer(tag, userID, runID, answerID, answerText string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	user, err := a.store.LoadStreamItem(ctx, userID)
	cancel()
	proof, ok := panelAskProofFromRow(user)
	if err != nil || !ok {
		time.AfterFunc(agentSegmentWriteDelay, func() {
			defer a.cancelPanelAskAnswer(userID)
			a.savePanelAskAnswer(tag, userID, runID, answerID, answerText)
		})
		return
	}
	key := pendingPanelAnswerKey{app: a, key: panelAnswerKey(proof.Ref)}
	pendingPanelAnswersMu.Lock()
	pending := pendingPanelAnswers[key]
	if pending == nil {
		pending = &pendingPanelAnswer{userID: userID, done: make(chan struct{})}
		pendingPanelAnswers[key] = pending
	} else if pending.userID != userID {
		// A newer Ask owns this key; the older save must not block it.
		pending = nil
	}
	pendingPanelAnswersMu.Unlock()
	if pending == nil {
		time.AfterFunc(agentSegmentWriteDelay, func() { a.savePanelAskAnswer(tag, userID, runID, answerID, answerText) })
		return
	}
	time.AfterFunc(agentSegmentWriteDelay, func() {
		defer func() {
			pendingPanelAnswersMu.Lock()
			if pendingPanelAnswers[key] == pending {
				delete(pendingPanelAnswers, key)
			}
			pending.finish()
			pendingPanelAnswersMu.Unlock()
		}()
		a.savePanelAskAnswer(tag, userID, runID, answerID, answerText)
	})
}

func (a *App) waitForPanelAnswerSave(ctx context.Context, ref TodayChatContextRef) error {
	key := pendingPanelAnswerKey{app: a, key: panelAnswerKey(ref)}
	for {
		pendingPanelAnswersMu.Lock()
		pending := pendingPanelAnswers[key]
		pendingPanelAnswersMu.Unlock()
		if pending == nil {
			return nil
		}
		select {
		case <-pending.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (a *App) savePanelAskAnswer(tag, userID, runID, answerID, answerText string) {
	if a == nil || a.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	user, err := a.store.LoadStreamItem(ctx, userID)
	if err != nil || user == nil || user.SessionLocalID != tag || user.Kind != "user" || user.RunID != runID {
		return
	}
	proof, ok := panelAskProofFromRow(user)
	if !ok {
		return
	}
	// The terminal event schedules its durable row separately. Wait for that
	// writer instead of racing its timer; never reconstruct a deleted row.
	var answer *storage.StreamItemRow
	for attempt := 0; attempt < 20; attempt++ {
		answer, err = a.store.LoadStreamItem(ctx, answerID)
		if err != nil {
			return
		}
		if answer != nil && answer.Content == answerText {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
	if answer == nil || answer.SessionLocalID != tag || answer.Kind != "assistant" || answer.RunID != runID || strings.TrimSpace(answerText) == "" || answer.Content != answerText {
		return
	}
	key := panelAnswerKey(proof.Ref)
	panelAnswerMu.Lock()
	defer panelAnswerMu.Unlock()
	var current panelAnswerPointer
	if raw, err := a.store.GetUIState(ctx, key); err != nil {
		return
	} else {
		_ = json.Unmarshal([]byte(raw), &current)
	}
	if current.Order > user.SortOrder {
		newer, err := a.store.LoadStreamItem(ctx, current.UserID)
		if err != nil {
			return
		}
		if newer != nil && newer.SessionLocalID == tag && newer.SortOrder == current.Order {
			return
		}
	}
	raw, err := json.Marshal(panelAnswerPointer{UserID: userID, AnswerID: answerID, RunID: runID, Order: user.SortOrder})
	if err != nil || a.store.SetUIState(ctx, key, string(raw)) != nil {
		return
	}
	a.emitBrowserEvent("today:answer-saved", map[string]string{"briefId": proof.Ref.BriefID, "cardId": proof.Ref.SourceID})
}

// GetTodayPanelAnswer rechecks the selected card and every packed source
// before returning the original Chat exchange. Missing or withdrawn rows are
// treated as absent; source read errors are not reported as an empty answer.
func (a *App) GetTodayPanelAnswer(ref TodayChatContextRef) (*TodayPanelAnswer, error) {
	if a == nil || a.store == nil || !panelAskCanSave(ref) || !todayChatRefWithinBounds(ref) {
		return nil, errTodayChatItemUnavailable
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.waitForPanelAnswerSave(ctx, ref); err != nil {
		return nil, err
	}
	// A concurrent unrelated write can invalidate a snapshot while it is
	// being assembled. Retry from the beginning with fresh privacy locks.
	for attempts := 0; attempts < 3; attempts++ {
		answer, err := a.getTodayPanelAnswerSnapshot(ctx, ref)
		if !errors.Is(err, errTodayChatItemUnavailable) {
			return answer, err
		}
	}
	return nil, errTodayChatItemUnavailable
}

func (a *App) getTodayPanelAnswerSnapshot(ctx context.Context, ref TodayChatContextRef) (*TodayPanelAnswer, error) {
	for _, lock := range []struct {
		try     func() bool
		release func()
	}{
		{a.closedEyes.mu.TryLock, a.closedEyes.mu.Unlock},
		{memoryMutationMu.TryLock, memoryMutationMu.Unlock},
		{memoryFileLocks.publication.TryLock, memoryFileLocks.publication.Unlock},
		{pii.TryLockSensitiveAppsForPublication, pii.UnlockSensitiveAppsForPublication},
	} {
		if err := lockTodayChatPublication(ctx, lock.try); err != nil {
			return nil, err
		}
		defer lock.release()
	}
	release, err := a.lockTodayChatFileServices(ctx, ref)
	if err != nil {
		return nil, err
	}
	defer release()
	files, err := a.readTodayChatAdmissionFiles(ctx, ref)
	if err != nil {
		return nil, err
	}
	mainRevision, err := a.store.ReadRevision(ctx)
	if err != nil {
		return nil, err
	}
	var activityRevision storage.ReadRevision
	if a.activity != nil && a.activity.store != nil {
		activityRevision, err = storage.DatabaseRevision(ctx, a.activity.store.db)
		if err != nil {
			return nil, err
		}
	}
	raw, err := a.store.GetUIState(ctx, panelAnswerKey(ref))
	if err != nil {
		return nil, err
	}
	if raw == "" {
		return nil, nil
	}
	var pointer panelAnswerPointer
	if json.Unmarshal([]byte(raw), &pointer) != nil || pointer.UserID == "" || pointer.AnswerID == "" || pointer.RunID == "" {
		return nil, nil
	}
	user, err := a.store.LoadStreamItem(ctx, pointer.UserID)
	if err != nil {
		return nil, err
	}
	proof, ok := panelAskProofFromRow(user)
	if !ok || panelAnswerKey(proof.Ref) != panelAnswerKey(ref) || user.Kind != "user" || user.RunID != pointer.RunID {
		return nil, nil
	}
	answer, err := a.store.LoadStreamItem(ctx, pointer.AnswerID)
	if err != nil {
		return nil, err
	}
	if answer == nil || answer.Kind != "assistant" || answer.RunID != pointer.RunID || answer.SessionLocalID != user.SessionLocalID || strings.TrimSpace(answer.Content) == "" {
		return nil, nil
	}
	main, err := a.store.LoadMainSession(ctx)
	if err != nil {
		return nil, err
	}
	if main == nil || main.LocalID != user.SessionLocalID {
		return nil, nil
	}
	item, sources, err := a.loadTodayChatContextItem(ctx, ref)
	if errors.Is(err, errTodayChatItemUnavailable) || errors.Is(err, errTodayChatObservationPaused) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if proactiveVoiceHash(item) != proof.ItemDigest || todayChatCarriesStaleWords(user.Content, item, ref) {
		return nil, nil
	}
	if len(proof.Related) > 0 {
		digest, err := a.chatTopicFingerprintFromSources(ctx, proof.Related, sources, map[string]topicChatFingerprintInput{})
		if err != nil || digest != proof.RelatedDigest {
			return nil, nil
		}
	}
	if err := a.validateTodayChatAdmissionFiles(ctx, ref, files); err != nil {
		return nil, err
	}
	after, err := a.store.ReadRevision(ctx)
	if err != nil || after != mainRevision {
		return nil, errTodayChatItemUnavailable
	}
	if a.activity != nil && a.activity.store != nil {
		after, err := storage.DatabaseRevision(ctx, a.activity.store.db)
		if err != nil || after != activityRevision {
			return nil, errTodayChatItemUnavailable
		}
	}
	return &TodayPanelAnswer{Question: user.Content, Answer: answer.Content, UserItemID: user.ID, AnswerItemID: answer.ID, MadeAt: answer.CreatedAt}, nil
}
