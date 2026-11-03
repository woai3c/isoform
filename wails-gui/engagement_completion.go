package main

import (
	"context"
	"log"
	"strings"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/internal/handoff"
	"wails-gui/storage"
)

const engagementActWindow = 2 * time.Hour

// Attribution observes a durable item completion. Opening a card, approving
// work or receiving a finished-work notice does not itself complete an item.
func (s *engagementService) inferCompletedReceipts(ctx context.Context, now time.Time) {
	if s.completion == nil {
		return
	}
	rows, err := s.store.EngagementCompletionCandidates(ctx, now.Add(-engagementStatsWindow), now)
	if err != nil {
		log.Printf("[engagement] reading completion candidates: %v", err)
		return
	}
	for _, row := range rows {
		completed, err := s.completion(ctx, row)
		if err != nil || completed.IsZero() || completed.After(now) {
			continue
		}
		sent := time.UnixMilli(row.SentAt)
		if !completed.After(sent) || completed.After(sent.Add(engagementActWindow)) {
			continue
		}
		if err := s.Receipt(ctx, row.ID, storage.EngagementCompletionChannel, storage.EngagementReceiptActed, storage.EngagementCompletionAction, completed); err != nil {
			log.Printf("[engagement] recording completed item: %v", err)
		}
	}
}

// Reads only the linked item's existing lifecycle, never its private content
// or a new external source. Kinds with no completion lifecycle stay unacted.
func (a *App) engagementItemCompletedAt(ctx context.Context, row storage.EngagementMomentRow) (time.Time, error) {
	id := row.SubjectID
	kind := engagement.Kind(row.Kind)
	if kind == engagement.KindFirstWeek {
		if momentFromRow(row).Params["stage"] != "commitment" {
			return time.Time{}, nil
		}
		kind = engagement.KindCommitmentSlipping
	}
	switch kind {
	case engagement.KindSuggestion:
		if a.activity == nil || a.activity.store == nil {
			return time.Time{}, nil
		}
		item, err := a.activity.store.GetInitiative(id)
		if err != nil {
			return time.Time{}, err
		}
		if !strings.HasPrefix(item.ExecutionRef, "handoff:") {
			return time.Time{}, nil
		}
		id = strings.TrimPrefix(item.ExecutionRef, "handoff:")
	case engagement.KindWorkBlocked, engagement.KindWorkFinished, engagement.KindDecisionDue:
	case engagement.KindCommitmentSlipping, engagement.KindGoalCheckIn:
		store, err := a.purposeProductStore()
		if err != nil {
			return time.Time{}, err
		}
		detail, err := store.GetDetail(id)
		if err != nil {
			return time.Time{}, err
		}
		if detail.Current.LifecycleState == PurposeStateResolved {
			return time.UnixMilli(detail.Current.OccurredAt), nil
		}
		return time.Time{}, nil
	default:
		return time.Time{}, nil
	}
	if a.handoffStore == nil {
		return time.Time{}, nil
	}
	entry, err := a.handoffStore.Get(id)
	if err != nil {
		return time.Time{}, err
	}
	if entry.Status == handoff.StatusCompleted {
		return time.UnixMilli(entry.UpdatedAt), nil
	}
	return time.Time{}, nil
}
