package main

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
)

const engagementCardReceiptPrefix = "engagement_card_read:"

type engagementCardReadReceipt struct {
	Version                                        int
	Presentation, MomentID, HandoffID, HandoffHash string
}

func (a *App) saveEngagementCardRead(ctx context.Context, hash, momentID, handoffID, originalHash string) string {
	record := engagementCardReadReceipt{Version: 1, Presentation: hash, MomentID: momentID, HandoffID: handoffID, HandoffHash: originalHash}
	if !a.engagementCardSourceReadable(ctx, record) {
		return ""
	}
	id := uuid.NewString()
	raw, err := json.Marshal(record)
	if err != nil || a.store.SetUIState(ctx, engagementCardReceiptPrefix+id, string(raw)) != nil {
		return ""
	}
	return id
}

func (a *App) engagementCardSourceReadable(ctx context.Context, record engagementCardReadReceipt) bool {
	if a == nil || a.store == nil || a.IsClosedEyesModeEnabled() || ctx.Err() != nil {
		return false
	}
	if memo := mainChatReadMemoFrom(ctx); memo != nil {
		return memo.cardSourceReadable(record, func() bool {
			return a.engagementCardSourceReadableUncached(ctx, record)
		})
	}
	recordMainChatReadMemoLoad("cardSource")
	return a.engagementCardSourceReadableUncached(ctx, record)
}

func (a *App) engagementCardSourceReadableUncached(ctx context.Context, record engagementCardReadReceipt) bool {
	if record.HandoffID == "" {
		return a.storedEngagementAllowsRead(ctx, record.MomentID)
	}
	if record.HandoffHash == "" || !a.mobileBriefHandoffSourceReadable(ctx, record.HandoffID) {
		return false
	}
	items, err := a.handoffEntries(ctx)
	if err != nil {
		return false
	}
	for _, item := range items {
		if item.ID == record.HandoffID {
			return record.HandoffHash == proactiveVoiceHash(item)
		}
	}
	return false
}

func (a *App) engagementCardAllowsRead(ctx context.Context, id, presentation string) bool {
	if a == nil || a.store == nil {
		return false
	}
	if _, err := uuid.Parse(id); err != nil {
		return false
	}
	raw, err := a.store.GetUIState(ctx, engagementCardReceiptPrefix+id)
	var record engagementCardReadReceipt
	return err == nil && json.Unmarshal([]byte(raw), &record) == nil && record.Version == 1 &&
		record.Presentation == presentation && a.engagementCardSourceReadable(ctx, record)
}

func (a *App) bindEngagementCardRead(ctx context.Context, item *mainChatProactive) {
	for i := range item.Items {
		a.bindEngagementCardRead(ctx, &item.Items[i])
	}
	if item.VoiceVersion != "" || item.ActivityOutcomeSource != "" {
		return
	}
	handoffID := ""
	if item.Work != nil {
		handoffID = item.Work.HandoffID
	}
	if item.Permission != nil {
		handoffID = item.Permission.HandoffID
	}
	item.ReadReceipt = ""
	item.ReadReceipt = a.saveEngagementCardRead(ctx, proactiveVoicePresentationHash(*item), item.MomentID, handoffID, item.sourceHash)
}
