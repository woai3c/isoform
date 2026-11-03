package main

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"wails-gui/internal/engagement"
)

const engagementReadReceiptParam = "_delivery_read_receipt"
const engagementReadReceiptPrefix = "engagement_delivery_read:"

type engagementReadReceipt struct {
	Version        int
	Moment, Source string
}

func engagementReadMomentHash(m engagement.Moment) string {
	params := map[string]string{}
	for key, value := range m.Params {
		if key != engagementReadReceiptParam && key != engagementChatReportedParam {
			params[key] = value
		}
	}
	return proactiveVoiceHash(struct {
		Kind    engagement.Kind
		Subject string
		Params  map[string]string
	}{m.Kind, m.SubjectID, params})
}

// Capture code-owned source identity at submission, not at the eventual send.
// A replacement source must never lend its permission to an older queued title.
func (a *App) prepareEngagementRead(ctx context.Context, m engagement.Moment) (engagement.Moment, bool) {
	return a.prepareEngagementReadWithID(ctx, m, uuid.NewString())
}

func (a *App) prepareEngagementReadWithID(ctx context.Context, m engagement.Moment, id string) (engagement.Moment, bool) {
	clean, allowed := sanitizeEngagementMoment(m)
	if _, err := uuid.Parse(id); !allowed || err != nil {
		return clean, false
	}
	delete(clean.Params, engagementReadReceiptParam)
	clean.ReadReceipt = ""
	source, allowed := a.engagementSourceFingerprint(ctx, clean)
	if !allowed {
		return clean, false
	}
	record := engagementReadReceipt{Version: 1, Moment: engagementReadMomentHash(clean), Source: source}
	raw, err := json.Marshal(record)
	if err != nil || a.store.SetUIState(ctx, engagementReadReceiptPrefix+id, string(raw)) != nil {
		return clean, false
	}
	clean.ReadReceipt = id
	return clean, true
}

func (a *App) engagementDeliveryAllowsRead(ctx context.Context, m engagement.Moment) bool {
	if a == nil || a.store == nil || a.IsClosedEyesModeEnabled() {
		return false
	}
	id := m.ReadReceipt
	if _, err := uuid.Parse(id); err != nil {
		return false
	}
	raw, err := a.store.GetUIState(ctx, engagementReadReceiptPrefix+id)
	var record engagementReadReceipt
	if err != nil || json.Unmarshal([]byte(raw), &record) != nil || record.Version != 1 || record.Moment != engagementReadMomentHash(m) {
		return false
	}
	source, allowed := a.engagementSourceFingerprint(ctx, m)
	return allowed && source != "" && source == record.Source
}

func (a *App) engagementSourceFingerprint(ctx context.Context, m engagement.Moment) (string, bool) {
	if !a.engagementMomentAllowsRead(ctx, m) {
		return "", false
	}
	now := time.Now()
	if a.engagement != nil {
		now = a.engagement.now()
	}
	switch m.Kind {
	case engagement.KindReminder:
		if m.Params["_list_task"] != "" {
			entry, _, err := a.brainDumpReminderSource(ctx, m)
			if err != nil {
				return "", false
			}
			return proactiveVoiceHash(struct{ ID, Fingerprint, When string }{entry.ID, entry.ListItem.Fingerprint, entry.ListItem.When}), true
		}
		return engagementReadMomentHash(m), true
	case engagement.KindWorkFinished, engagement.KindWorkBlocked:
		items, err := a.handoffEntries(ctx)
		if err != nil {
			return "", false
		}
		for _, item := range items {
			if item.ID == m.SubjectID && m.Params["title"] == engagementTitle(item.Title) {
				return proactiveVoiceHash(item), true
			}
		}
		return "", false
	case engagement.KindMeetingPrep, engagement.KindWaitingOnReply, engagement.KindCommitmentSlipping:
		source, reason := a.loadProactiveVoiceSource(ctx, m, now, a.currentUILanguage())
		return proactiveVoiceHash(source.Provenance), reason == "" && len(source.Provenance) > 0
	case engagement.KindCalendarConflict:
		events, err := a.listCalendarEvents(ctx, now.Add(-24*time.Hour), now.Add(engagementConflictHorizon))
		return proactiveVoiceHash(events), err == nil
	case engagement.KindPlaybookSuggested, engagement.KindPlaybookResult:
		entry, err := a.sopStore.Get(m.SubjectID)
		if err != nil {
			return "", false
		}
		if m.Kind == engagement.KindPlaybookResult {
			if m.Params["title"] != engagementTitle(entry.Title) {
				return "", false
			}
			if a.playbookPlans == nil {
				return "", false
			}
			plan, found, err := a.playbookPlans.Get(m.SubjectID)
			if err != nil || !found || plan.LastRun == nil || plan.LastRun.Result != m.Params["result"] {
				return "", false
			}
			return proactiveVoiceHash(struct {
				Entry any
				Run   any
			}{entry, plan.LastRun}), true
		}
		if a.playbookPlans == nil {
			return "", false
		}
		plan, found, err := a.playbookPlans.Get(m.SubjectID)
		if err != nil || !found || plan.Proposal == nil || m.Params["site"] != plan.Proposal.SiteName || m.Params["time"] != plan.Proposal.Time {
			return "", false
		}
		return proactiveVoiceHash(struct{ Entry, Proposal any }{entry, plan.Proposal}), true
	case engagement.KindGoalCheckIn:
		detail, ok := a.proactiveVoicePurpose(ctx, m.SubjectID, now)
		return proactiveVoiceHash(detail), ok
	case engagement.KindPersonReappeared:
		person, err := a.store.GetPrincipal(ctx, m.SubjectID)
		sightings, sightingsErr := a.principalSessionSightingsForRead(ctx, m.SubjectID)
		handles, handlesErr := a.store.ListAppHandlesByPrincipal(ctx, m.SubjectID)
		return proactiveVoiceHash(struct{ Person, Sightings, Handles any }{person, sightings, handles}), err == nil && sightingsErr == nil && handlesErr == nil
	case engagement.KindScheduleMissed:
		sources := a.collectTodayChatSources(ctx, now)
		item, err := a.resolveTodayChatItem(ctx, TodayChatContextRef{SourceKind: "schedule", SourceID: m.SubjectID}, sources, now)
		return proactiveVoiceHash(item), err == nil
	case engagement.KindTopicFinds:
		state := a.topicsSvc().store.load()
		p, err := a.mcpReadPrivacy(ctx)
		if err != nil {
			return "", false
		}
		items := topicFindsFresh(state, now)
		if len(items) == 0 || m.Params["title"] != engagementTitle(items[0].Title) || m.Params["topic"] != engagementTitle(items[0].Topic) {
			return "", false
		}
		for _, item := range items {
			if _, allowed := p.briefReferenceItem(MobileBriefRef{SourceKind: mobileBriefFindSource, SourceID: item.ID}); !allowed {
				return "", false
			}
		}
		return proactiveVoiceHash(state.Finds), true
	default:
		// Source-free status, typed outcome and journey text have separate
		// live source projections, checked by engagementMomentAllowsRead.
		return engagementReadMomentHash(m), true
	}
}
