package main

import (
	"context"
	"strings"

	"wails-gui/internal/pii"
	"wails-gui/storage"
)

// Live text is a projection of the existing chat turn, not a separate stream.
// Recheck the source proof before each publication; the phone never supplies it.
func (bridge *mobileBridge) mobileBriefLiveAnswer(ctx context.Context, chatID string, history []storage.StreamItemRow) *mobileChatLiveAnswer {
	bridge.chat.mu.Lock()
	reply := bridge.chat.replies[chatID]
	if reply == nil || !reply.ask {
		bridge.chat.mu.Unlock()
		return nil
	}
	turnID, order := reply.turnID, reply.userOrder
	var parts []string
	for _, row := range reply.rows {
		if row.Kind == "assistant" && row.Content != "" {
			parts = append(parts, row.Content)
		}
	}
	if text := reply.text.String(); text != "" {
		parts = append(parts, text)
	}
	bridge.chat.mu.Unlock()
	text := strings.Join(parts, "\n\n")
	if text == "" {
		return nil
	}
	var user *storage.StreamItemRow
	for i := range history {
		if history[i].SortOrder == order && history[i].Kind == "user" {
			user = &history[i]
			break
		}
	}
	proof, ok := panelAskProofFromRow(user)
	if !ok {
		return nil
	}
	a := bridge.app
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
			return nil
		}
		defer lock.release()
	}
	release, err := a.lockTodayChatFileServices(ctx, proof.Ref)
	if err != nil {
		return nil
	}
	defer release()
	files, err := a.readTodayChatAdmissionFiles(ctx, proof.Ref)
	if err != nil {
		return nil
	}
	revision, err := a.store.ReadRevision(ctx)
	if err != nil {
		return nil
	}
	var activityRevision storage.ReadRevision
	if a.activity != nil && a.activity.store != nil {
		activityRevision, err = storage.DatabaseRevision(ctx, a.activity.store.db)
		if err != nil {
			return nil
		}
	}
	item, sources, err := a.loadTodayChatContextItem(ctx, proof.Ref)
	if err != nil || proactiveVoiceHash(item) != proof.ItemDigest || todayChatCarriesStaleWords(user.Content, item, proof.Ref) {
		return nil
	}
	if len(proof.Related) > 0 {
		digest, err := a.chatTopicFingerprintFromSources(ctx, proof.Related, sources, map[string]topicChatFingerprintInput{})
		if err != nil || digest != proof.RelatedDigest {
			return nil
		}
	}
	if a.validateTodayChatAdmissionFiles(ctx, proof.Ref, files) != nil {
		return nil
	}
	after, err := a.store.ReadRevision(ctx)
	if err != nil || after != revision {
		return nil
	}
	if a.activity != nil && a.activity.store != nil {
		afterActivity, err := storage.DatabaseRevision(ctx, a.activity.store.db)
		if err != nil || afterActivity != activityRevision {
			return nil
		}
	}
	bridge.chat.mu.Lock()
	current := bridge.chat.replies[chatID]
	active := current != nil && current.turnID == turnID
	bridge.chat.mu.Unlock()
	if !active {
		return nil
	}
	return &mobileChatLiveAnswer{TurnID: turnID, Text: mobileChatBound(text, mobileChatTextMax)}
}
