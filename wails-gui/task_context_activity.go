package main

import (
	"fmt"
	"sort"
	"strings"
)

const (
	taskContextSourceActivitySnapshotKind = "activity_snapshot_context"
	taskContextActivitySnapshotLimit      = 4
	taskContextActivitySnapshotMaxChars   = 4_000
)

func taskShadowSnapshotContextContent(
	snapshot ActivitySnapshotRow,
	exactOCR string,
) string {
	parsed := parseSnapshotContent(snapshot)
	appName := strings.TrimSpace(resolveRealApp(parsed, snapshot.App))
	title := ""
	if parsed != nil {
		title = strings.TrimSpace(parsed.Title)
	}
	body := strings.TrimSpace(cleanBody(parsed, snapshot, exactOCR))
	if body == "" {
		return ""
	}
	parts := make([]string, 0, 3)
	if appName != "" && appName != unknownAppName {
		parts = append(parts, "App: "+appName)
	}
	if title != "" && !strings.EqualFold(title, body) {
		parts = append(parts, "Title: "+title)
	}
	parts = append(parts, "Visible context before task start:\n"+body)
	return truncateTaskContext(
		strings.Join(parts, "\n"),
		taskContextActivitySnapshotMaxChars,
	)
}

// taskShadowPreOnsetActivityContext freezes the latest exact visual context
// from this session group. It never reads a protected session, never includes
// a post-onset frame, and never falls back to OCR from a sibling snapshot.
func taskShadowPreOnsetActivityContext(
	store *ActivityStore,
	group sessionGroup,
	cutoffAt int64,
) ([]TaskContextCandidate, error) {
	if store == nil || store.db == nil || cutoffAt <= 0 {
		return []TaskContextCandidate{}, nil
	}
	snapshots := make([]ActivitySnapshotRow, 0)
	for _, session := range group.Sessions {
		rows, err := store.GetSessionSnapshots(session.ID)
		if err != nil {
			return nil, fmt.Errorf(
				"read pre-onset snapshots for %s: %w",
				session.ID,
				err,
			)
		}
		for _, snapshot := range rows {
			if snapshot.Timestamp > 0 && snapshot.Timestamp <= cutoffAt {
				snapshots = append(snapshots, snapshot)
			}
		}
	}
	sort.SliceStable(snapshots, func(i, j int) bool {
		if snapshots[i].Timestamp != snapshots[j].Timestamp {
			return snapshots[i].Timestamp > snapshots[j].Timestamp
		}
		return snapshots[i].ID > snapshots[j].ID
	})

	result := make([]TaskContextCandidate, 0, taskContextActivitySnapshotLimit)
	seenContent := make(map[string]struct{})
	for _, snapshot := range snapshots {
		if len(result) >= taskContextActivitySnapshotLimit {
			break
		}
		exactOCR, err := store.GetExactOCRTextBySnapshot(
			snapshot.SessionID,
			snapshot.ID,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"read exact pre-onset OCR for %s:%d: %w",
				snapshot.SessionID,
				snapshot.ID,
				err,
			)
		}
		content := taskShadowSnapshotContextContent(snapshot, exactOCR)
		if content == "" {
			continue
		}
		contentKey := taskContextHash(content)
		if _, duplicate := seenContent[contentKey]; duplicate {
			continue
		}
		seenContent[contentKey] = struct{}{}
		sourceID := fmt.Sprintf("%s:%d", snapshot.SessionID, snapshot.ID)
		result = append(result, TaskContextCandidate{
			Ref:             "activity-snapshot:" + sourceID,
			Section:         TaskContextSectionActivity,
			SourceKind:      taskContextSourceActivitySnapshotKind,
			SourceID:        sourceID,
			ObservedAt:      snapshot.Timestamp,
			Content:         content,
			EvidenceClass:   TaskContextEvidenceObservedContext,
			PrivacyDecision: TaskContextPrivacyRedact,
			Relevance:       1 - float64(len(result))*0.01,
		})
	}
	return result, nil
}
