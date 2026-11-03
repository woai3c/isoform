package main

import (
	"sort"
	"strings"
)

const composerTriageHintLimit = 3

// composerTriageHint describes an extraction boundary, not task identity or
// admission evidence. It deliberately carries no triage text, predictions,
// group/session identifiers, or additional evidence tokens.
type composerTriageHint struct {
	RunID             string
	SameBatch         bool
	CrossesSessionCut bool
}

// composerTriageHints reuses the triager's bounded batching rule over only the
// current and last three runs' eligible sessions. Missing or excluded capture
// metadata means no hint. The composer still works when triage has not run,
// its optional outputs are disabled, or its extraction batch differs later.
func (s *ActivityStore) composerTriageHints(run ActivityTimelineRunRow, prior []ActivityTimelineRunRow) []composerTriageHint {
	if allowed, err := s.timelineRunEvidenceAllowed(run); err != nil || !allowed {
		return nil
	}
	eligible := make([]ActivityTimelineRunRow, 0, composerTriageHintLimit)
	ids := append([]string(nil), run.SessionIDs...)
	considered := 0
	for _, prev := range prior {
		if prev.ID == run.ID {
			continue
		}
		considered++
		if considered > composerTriageHintLimit {
			break
		}
		if prev.EndMs > run.StartMs || run.StartMs-prev.EndMs > activityWeakLinkMs {
			continue
		}
		if allowed, err := s.timelineRunEvidenceAllowed(prev); err != nil || !allowed {
			continue
		}
		eligible = append(eligible, prev)
		ids = append(ids, prev.SessionIDs...)
	}
	if len(eligible) == 0 {
		return nil
	}
	sessions, err := s.GetSessionsByIDs(ids)
	if err != nil {
		return nil
	}
	rows := make([]ActivitySessionRow, 0, len(sessions))
	for _, session := range sessions {
		if session == nil || session.TriageStatus == "protected" {
			continue
		}
		allowed := true
		for _, app := range composeParseStrings(session.AppNames) {
			if activityEvidenceAppExcluded("", app) {
				allowed = false
				break
			}
		}
		if allowed {
			rows = append(rows, *session)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].StartTime != rows[j].StartTime {
			return rows[i].StartTime < rows[j].StartTime
		}
		return rows[i].ID < rows[j].ID
	})
	return composerTriageHintsForGroups(run, eligible, mergeSessions(rows))
}

func composerTriageHintsForGroups(run ActivityTimelineRunRow, prior []ActivityTimelineRunRow, groups []sessionGroup) []composerTriageHint {
	if run.EvidenceExcluded {
		return nil
	}
	sessionBatches := make(map[string]int)
	for i, group := range groups {
		for _, session := range group.Sessions {
			if session.TriageStatus != "protected" {
				sessionBatches[session.ID] = i
			}
		}
	}
	currentBatches, ok := composerRunTriageBatches(run, sessionBatches)
	if !ok {
		return nil
	}
	hints := make([]composerTriageHint, 0, composerTriageHintLimit)
	for i, prev := range prior {
		if i >= composerTriageHintLimit {
			break
		}
		if prev.ID == run.ID || prev.EvidenceExcluded || prev.EndMs > run.StartMs || run.StartMs-prev.EndMs > activityWeakLinkMs {
			continue
		}
		previousBatches, ok := composerRunTriageBatches(prev, sessionBatches)
		if !ok {
			continue
		}
		sameBatch := false
		for group := range currentBatches {
			if previousBatches[group] {
				sameBatch = true
				break
			}
		}
		sameSession := false
		for _, id := range run.SessionIDs {
			if composeContains(prev.SessionIDs, id) {
				sameSession = true
				break
			}
		}
		hints = append(hints, composerTriageHint{RunID: prev.ID, SameBatch: sameBatch, CrossesSessionCut: !sameSession})
	}
	return hints
}

func composerRunTriageBatches(run ActivityTimelineRunRow, sessionBatches map[string]int) (map[int]bool, bool) {
	if len(run.SessionIDs) == 0 {
		return nil, false
	}
	batches := make(map[int]bool)
	for _, id := range run.SessionIDs {
		batch, ok := sessionBatches[strings.TrimSpace(id)]
		if !ok {
			// Do not reveal even a boundary when any source session is unknown
			// or excluded. Batching is optional context, never a fallback join.
			return nil, false
		}
		batches[batch] = true
	}
	return batches, true
}
