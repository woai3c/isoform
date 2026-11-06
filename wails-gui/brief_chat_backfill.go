package main

import (
	"context"

	"wails-gui/internal/engagement"
	"wails-gui/storage"
)

// briefChatBackfillKey records that every held-back brief has its Chat card (R36).
const briefChatBackfillKey = "brief-chat-cards-backfill-v1"

// firstWeekDiscoveryReason is the reason the first-week rule gave the brief_ready
// moments it held back. Those briefs never reached Chat.
const firstWeekDiscoveryReason = "first week discovery"

// backfillBriefChatCards gives every brief the first-week rule held back its
// Chat card, once. It posts the card only: it never pushes, never buzzes and
// never changes the moment's state. Each card keeps the time its brief was ready.
func (a *App) backfillBriefChatCards(ctx context.Context) {
	svc, ok := a.mobileBriefSource().(*mobileBriefService)
	if !ok || svc == nil || svc.store == nil {
		return
	}
	a.runBriefChatCardBackfill(ctx, svc.store.load)
}

// runBriefChatCardBackfill posts the missing cards and records completion only
// when every card is posted or has nothing to show. A failed card is retried on
// the next launch; posting is idempotent, so a card is never posted twice.
func (a *App) runBriefChatCardBackfill(ctx context.Context, briefFor func(string) (MobileBriefV1, bool)) bool {
	if a == nil || a.store == nil || a.engagement == nil {
		return false
	}
	done, err := a.store.GetUIState(ctx, briefChatBackfillKey)
	if err != nil {
		return false
	}
	if done == "true" {
		return true
	}
	rows, err := a.store.ListEngagementMoments(ctx, storage.EngagementStateSuppressed, 1000)
	if err != nil {
		return false
	}
	complete := true
	for _, row := range rows {
		if row.Kind != string(engagement.KindBriefReady) || row.Reason != firstWeekDiscoveryReason {
			continue
		}
		if !a.postBriefChatCard(momentFromRow(row), briefFor) {
			complete = false
		}
	}
	if complete {
		_ = a.store.SetUIState(ctx, briefChatBackfillKey, "true")
	}
	return complete
}

// postBriefChatCard posts one brief's Chat card at its ready time. The card holds
// only the moment's generic copy, never the brief's words. A brief with nothing
// written (quiet) or one no longer stored has no card. It reports whether the
// card is now posted or has nothing to show.
func (a *App) postBriefChatCard(m engagement.Moment, briefFor func(string) (MobileBriefV1, bool)) bool {
	brief, found := briefFor(m.SubjectID)
	if !found || brief.NarrativeStatus == "quiet" {
		return true
	}
	spec, known := engagement.Spec(m.Kind)
	if !known {
		return true
	}
	if engagementChatKindMuted(spec, a.engagement.Prefs(), a.engagement.Paused(m.Kind), a.engagement.now()) {
		return true
	}
	item, ok := a.proactiveItem(m, a.engagement.render(spec, m))
	if !ok {
		return false
	}
	item.postedAt = m.CreatedAt
	return a.postProactiveItem(item, engagementReportKey(m, m.CreatedAt)) != ""
}
