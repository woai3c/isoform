package main

import (
	"context"
	"time"
)

// Most directories have no open commitment with a named counterparty. Typed
// commitments only need the Purpose closure; collecting every memory, person
// and captured activity for those reads is both unrelated IO and repeated work.
// Any observed dependency still takes the existing complete source collector.
func (a *App) peoplePurposeLoops(ctx context.Context, now time.Time) []PurposeOpenLoopDetail {
	store, err := a.purposeProductStore()
	if err != nil {
		return nil
	}
	details, err := store.ListDetailsAllContext(ctx)
	if err != nil {
		return nil
	}
	byID := make(map[string]PurposeOpenLoopDetail, len(details))
	for _, detail := range details {
		byID[detail.OpenLoop.ID] = detail
	}
	seen := map[string]bool{}
	var needsSources func(PurposeOpenLoopDetail) bool
	needsSources = func(detail PurposeOpenLoopDetail) bool {
		if seen[detail.OpenLoop.ID] {
			return false // The shared closure filter still rejects cycles.
		}
		seen[detail.OpenLoop.ID] = true
		for _, evidence := range detail.Evidence {
			switch evidence.SourceKind {
			case "explicit_user_statement", "user_statement", userPurposeSourceKind, activityFollowUpSourceKind, "activity_session", "meeting":
			case "purpose", taskContextSourcePurposeOpenLoopKind:
				if parent, exists := byID[evidence.SourceID]; exists && needsSources(parent) {
					return true
				}
			default:
				return true
			}
		}
		return false
	}
	var candidates []PurposeOpenLoopDetail
	for _, detail := range details {
		event := detail.Current
		if detail.OpenLoop.Kind != PurposeKindCommitment || event.Title == "" || event.Counterparty == "" ||
			(event.LifecycleState != PurposeStateActive && event.LifecycleState != PurposeStateWaiting && event.LifecycleState != PurposeStateCandidate) {
			continue
		}
		candidates = append(candidates, detail)
		if needsSources(detail) {
			return a.collectTodayChatSources(ctx, now).Purposes
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	// Reuse the exact field/history/evidence rules, including referenced goals.
	return a.filterTodayChatPurposesContext(ctx, todayChatSources{Purposes: details, Now: now})
}
