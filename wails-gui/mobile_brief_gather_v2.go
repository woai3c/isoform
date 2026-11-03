package main

import (
	"sort"
	"time"
)

type morningBriefPrior struct {
	item MobileBriefItem
	at   time.Time
}

// morningBriefGatherCandidates computes source-backed attributes before the
// legacy section and pick caps. Its output is not model-admissible on its own:
// the delivery read gate and the complete source binding must still pass.
func morningBriefGatherCandidates(in mobileBriefInput) []morningBriefCandidate {
	in = mobileBriefAnchorInput(in)
	now, loc := in.AsOf, mobileBriefInputLocation(in)
	history := append([]MobileBriefV1(nil), in.PreviousBriefs...)
	sort.SliceStable(history, func(i, j int) bool { return mobileBriefAsOfTime(history[i]).After(mobileBriefAsOfTime(history[j])) })
	prior := map[string]morningBriefPrior{}
	roles := map[string][]string{}
	count := 0
	for _, brief := range history {
		at := mobileBriefAsOfTime(brief)
		if at.IsZero() || !at.Before(now) {
			continue
		}
		refs := mobileBriefEvidenceByRef(brief)
		for _, section := range mobileBriefSectionLines(brief.Sections) {
			for _, item := range section.items {
				if len(item.Refs) == 0 {
					continue
				}
				ref := refs[item.Refs[0]]
				key := mobileBriefLoopIdentity(ref.SourceKind, ref.SourceID)
				if _, exists := prior[key]; !exists {
					prior[key] = morningBriefPrior{item: item, at: at}
				}
			}
		}
		seenRoles := map[string]bool{}
		for i, paragraph := range brief.Narrative {
			for _, id := range paragraph.Refs {
				ref, ok := refs[id]
				if !ok {
					continue
				}
				role := "item"
				if i == 0 {
					role = "lead"
				}
				key := mobileBriefLoopIdentity(ref.SourceKind, ref.SourceID)
				if !seenRoles[key] {
					for len(roles[key]) < count {
						roles[key] = append(roles[key], "none")
					}
					roles[key] = append(roles[key], role)
					seenRoles[key] = true
				}
			}
		}
		for key := range roles {
			if len(roles[key]) <= count {
				roles[key] = append(roles[key], "none")
			}
		}
		count++
		if count == 3 {
			break
		}
	}
	var result []morningBriefCandidate
	seen := map[string]bool{}
	groups := []struct {
		items        []mobileBriefCandidate
		userOwnedDue bool
	}{
		{in.Commitments, true}, {in.WaitingOnYou, false}, {in.WorkingToward, false}, {in.OpenLoops, false},
		{in.Meetings, false}, {in.TomorrowMeetings, false}, {in.Moved, false}, {in.Doing, false}, {in.Deliverables, false}, {in.Schedules, false},
	}
	for _, group := range groups {
		for _, item := range prepareMobileBriefCandidates(group.items, mobileBriefNewestFirst) {
			if item.EvidenceUnavailable || item.ChatMessageID != "" || item.Outcome != nil || item.rankedUnbound || mobileBriefCandidateAnchor(item) == "" {
				continue
			}
			switch item.SourceKind {
			case mobileBriefWebSource, mobileBriefFileSource, mobileBriefMeetingAppSource, "chat_message", mobileBriefDayShapeSource, mobileBriefNoticedSource:
				continue
			}
			key := mobileBriefCandidateLoopKey(item)
			if seen[key] {
				continue
			}
			seen[key] = true
			historyKey := mobileBriefLoopIdentity(item.SourceKind, item.SourceID)
			if _, ok := prior[historyKey]; !ok {
				for _, alias := range item.LoopAliases {
					if _, found := prior[alias]; found {
						historyKey = alias
						break
					}
				}
			}
			priorRoles := append([]string(nil), roles[historyKey]...)
			for len(priorRoles) < count {
				priorRoles = append(priorRoles, "none")
			}
			row := morningBriefCandidate{SourceKind: item.SourceKind, SourceID: item.SourceID, SourceDigest: item.rankedSourceDigest, SourceOrigin: item.rankedOriginDigest,
				Dependencies: append([]morningBriefSourceDependency(nil), item.rankedDependencies...), Unbound: item.rankedUnbound,
				ID: mobileBriefItemKey("candidate", item.SourceKind, item.SourceID), Title: item.Title,
				Status: item.Status, LastTouch: formatMobileBriefTime(item.LastTouch), NextStep: item.NextStep,
				Due: formatMobileBriefTime(item.DueAt), Counterparty: item.Person, Attendees: append([]string(nil), item.rankedAttendees...), PriorBriefs: priorRoles}
			if item.SourceKind == mobileBriefCalendarSource {
				row.Confidence = 1
				row.Due = formatMobileBriefTime(item.At)
			} else if item.confidenceKnown && item.confidence >= 0 && item.confidence <= 1 {
				row.Confidence = item.confidence
			}
			if !item.OpenedAt.IsZero() && !item.OpenedAt.After(now) {
				row.DaysOpen = -mobileBriefCivilDays(item.OpenedAt, now, loc)
				row.DaysOpenKnown = true
			}
			if item.Kind == "waiting_on_reply" && !item.OpenedAt.IsZero() {
				row.Direction, row.DaysWaiting, row.NeedsUser = "owed_by_user", row.DaysOpen, true
				row.DaysWaitingKnown = row.DaysOpenKnown
			}
			if group.userOwnedDue && !item.DueAt.IsZero() && !item.DueAt.After(now.Add(24*time.Hour)) {
				row.NeedsUser = true
			}
			if item.SourceKind == mobileBriefCalendarSource && item.Status == "upcoming" {
				row.Within24Hours = !item.At.Before(now) && !item.At.After(now.Add(24*time.Hour))
				if item.rankedInvitePending {
					// The attendee decides their own response. This is not evidence
					// that the meeting itself requires preparation.
					row.NeedsUser, row.OnlyUserKnown, row.OnlyUser = true, true, true
					row.DecisionKind = "invitation_response"
				}
			}
			if item.Kind == "one_time_work" && (item.Status == "blocked" || item.Status == "needs_review") {
				row.NeedsUser = true
				if item.Status == "needs_review" && item.confidenceKnown && item.confidence == 1 {
					// This lifecycle state waits for the user's review decision.
					row.OnlyUserKnown, row.OnlyUser = true, true
					row.DecisionKind = "work_review"
				}
			}
			if item.SourceKind == "purpose" && item.Kind == PurposeKindCommitment && item.Status == "slipping" && !item.DueAt.IsZero() {
				row.Slipping = true
			}
			old, hadPrior := prior[historyKey]
			yesterday := now.In(loc).AddDate(0, 0, -1).Format("2006-01-02")
			if hadPrior && old.at.In(loc).Format("2006-01-02") == yesterday {
				row.ChangeKnown = true
				oldDue := old.item.DueAt
				if item.SourceKind == mobileBriefCalendarSource {
					oldDue = old.item.At
				}
				row.Changed = old.item.Status != item.Status || oldDue != row.Due || old.item.LastTouch != row.LastTouch || old.item.Title != item.Title
				if row.Changed {
					row.Change = "source changed"
				}
			}
			shown := false
			for _, role := range row.PriorBriefs {
				shown = shown || role != "none"
			}
			if item.SourceKind == "history" && item.Kind == "one_time_work" && item.Status == "completed" && shown && hadPrior && item.At.After(old.at) && !item.At.After(now) {
				row.Handled, row.Confidence = true, 1
			}
			if row.ChangeKnown && !row.Changed && shown && !morningBriefThresholdCrossed(item, old, now, loc) {
				continue
			}
			result = append(result, row)
		}
	}
	return result
}

func morningBriefThresholdCrossed(item mobileBriefCandidate, old morningBriefPrior, now time.Time, loc *time.Location) bool {
	if !item.DueAt.IsZero() && !item.DueAt.After(now.Add(24*time.Hour)) && item.DueAt.After(old.at.Add(24*time.Hour)) {
		return true
	}
	if item.Kind == "waiting_on_reply" && old.item.Kind != "waiting_on_reply" {
		return true
	}
	if !item.LastTouch.IsZero() && -mobileBriefCivilDays(item.LastTouch, now, loc) >= 7 && -mobileBriefCivilDays(item.LastTouch, old.at, loc) < 7 {
		return true
	}
	return false
}
