package main

import (
	"context"
	"time"
)

// A scoped proof read is only used for an explicitly selected brief card. It
// must still pass the brief's source, withdrawal and revision checks. It is
// never set for general chat, capture, or a user's own typed message.
type pausedBriefReadKey struct{}

func pausedBriefReadAllowed(ctx context.Context) bool {
	allowed, _ := ctx.Value(pausedBriefReadKey{}).(bool)
	return allowed
}

func (a *App) loadPausedTodayBriefCard(ctx context.Context, ref TodayChatContextRef) (todayChatItem, todayChatSources, error) {
	ctx = context.WithValue(ctx, pausedBriefReadKey{}, true)
	now := time.Now()
	sources := a.collectTodayChatSources(ctx, now)
	brief, ok := a.todayChatDeliveredBriefFrom(ctx, ref.BriefID, &sources)
	if !ok || !a.todayChatBriefFrozen(ctx, brief, sources) {
		return todayChatItem{}, todayChatSources{}, errTodayChatItemUnavailable
	}
	card, ok := a.todayChatCardNow(ctx, brief, ref.SourceID, sources)
	if !ok || card.Mode != mobileBriefCardSend || card.Message == "" {
		return todayChatItem{}, todayChatSources{}, errTodayChatItemUnavailable
	}
	if _, err := a.todayChatCardSubject(ctx, brief, card, sources, now); err != nil {
		return todayChatItem{}, todayChatSources{}, err
	}
	item, err := a.todayChatBriefSnapshot(ctx, ref, brief)
	if err != nil {
		return todayChatItem{}, todayChatSources{}, err
	}
	item.CardMessage = card.Message
	item.pausedBrief = true
	// The proof collection is used only to validate the brief; never attach
	// unrelated memory, activity, people, or current source records.
	return item, todayChatSources{Now: now, useEpoch: sources.useEpoch}, ctx.Err()
}

// Only the desktop Today reader uses this projection. The phone and regular
// brief APIs retain their normal pause policy. Unknown startup policy is not
// consent to re-open an observed source.
func (a *App) todayDesktopPausedBrief(ctx context.Context, id string) (MobileBriefV1, bool) {
	if a == nil || !a.IsClosedEyesModeEnabled() || a.closedEyes.policyPending.Load() {
		return MobileBriefV1{}, false
	}
	ctx = context.WithValue(ctx, pausedBriefReadKey{}, true)
	sources := a.collectTodayChatSources(ctx, time.Now())
	sources.skipBriefTopics = true
	brief, ok := a.todayChatDeliveredBriefFrom(ctx, id, &sources)
	if !ok || ctx.Err() != nil || !a.todayChatBriefFrozen(ctx, brief, sources) {
		return MobileBriefV1{}, false
	}
	return brief, true
}
