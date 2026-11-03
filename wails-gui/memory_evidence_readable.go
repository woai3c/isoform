package main

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"

	"wails-gui/internal/pii"
)

type ReadableEvidence struct {
	At                int64  `json:"at"`
	DurationMs        int64  `json:"durationMs"`
	AppName           string `json:"appName"`
	Text              string `json:"text"`
	ActivitySessionID string `json:"activitySessionId"`
}

// GetMemoryEvidenceReadable returns moment titles, never captured screen text.
func (a *App) GetMemoryEvidenceReadable(id string) ([]ReadableEvidence, error) {
	if a.memoryEvidenceReadObserver != nil {
		a.memoryEvidenceReadObserver()
	}
	rows := []ReadableEvidence{}
	entry, err := a.GetMemoryEntry(id)
	if err != nil {
		return nil, err
	}
	allow, err := a.memoryReadFilter()
	if err != nil {
		return nil, err
	}
	if !allow(id, entry.Content) || a.activity == nil || a.activity.store == nil {
		return rows, nil
	}
	ctx := context.Background()
	privacy, err := a.mcpReadPrivacy(ctx)
	if errors.Is(err, errMCPReadPaused) {
		return rows, nil
	}
	if err != nil {
		return nil, err
	}
	store := a.activity.store
	moments := map[string]bool{}
	ids := memoryEvidenceSessionIDs(entry.Content)
	support := map[string]bool{}
	for _, id := range ids {
		support[id] = true
	}
	for _, sessionID := range ids {
		session, err := store.GetSessionByID(sessionID)
		if err != nil {
			return nil, err
		}
		if session == nil || !a.activitySessionAllowsModelRead(store, *session) {
			continue
		}
		// Runs link sessions to moments. Rank by dwell time to choose the
		// session's main app, and require the whole moment's source graph.
		var activityID, appName, appKey, title string
		err = store.reader().QueryRow(`SELECT a.id, g.app_name, g.app_key,
            CASE WHEN a.user_title != '' THEN a.user_title ELSE a.title END
            FROM activity_timeline_groups g JOIN activity_run_links l ON l.run_id=g.id
            JOIN activities a ON a.id=l.activity_id
            WHERE EXISTS (SELECT 1 FROM json_each(g.session_ids) WHERE value=?)
            AND g.evidence_excluded=0
            ORDER BY g.active_ms DESC, a.updated_at DESC, a.id LIMIT 1`, sessionID).Scan(&activityID, &appName, &appKey, &title)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, err
		}
		if isSystemNoiseApp(appName, appKey) || strings.TrimSpace(appName) == "" || strings.TrimSpace(title) == "" {
			continue
		}
		moment, err := store.GetActivityByID(activityID)
		if err != nil {
			return nil, err
		}
		if moment == nil || !a.memoryEvidenceMomentAllowed(ctx, privacy, store, *moment, support) {
			continue
		}
		// The title is already Isoform's moment summary (or the user's edit).
		// Hide titles containing PII or stored masks; never fall back to raw source text.
		if pii.HasRedaction(title) || pii.HasRedaction(appName) {
			continue
		}
		text := strings.Join(strings.Fields(title), " ")
		appName = redactActivityModelText(appName)
		if !a.activityDependencyAllowsModelRead(sessionID, entry.Apps) {
			continue
		}
		moments[activityID] = true
		rows = append(rows, ReadableEvidence{session.StartTime, max(int64(0), session.EndTime-session.StartTime), appName, text, sessionID})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].At == rows[j].At {
			return rows[i].ActivitySessionID < rows[j].ActivitySessionID
		}
		return rows[i].At > rows[j].At
	})
	// Re-read source/policy after the database work; a withdrawn belief must
	// not leave a readable derivative behind while this projection was built.
	current, err := a.GetMemoryEntry(id)
	if err != nil {
		return nil, err
	}
	if current.Content != entry.Content || !allow(id, current.Content) {
		return []ReadableEvidence{}, nil
	}
	// Search's gate caches within a read; use a fresh gate for publication.
	privacy, err = a.mcpReadPrivacy(ctx)
	if errors.Is(err, errMCPReadPaused) {
		return []ReadableEvidence{}, nil
	}
	if err != nil {
		return nil, err
	}
	for activityID := range moments {
		moment, err := store.GetActivityByID(activityID)
		if err != nil {
			return nil, err
		}
		if moment == nil || !a.memoryEvidenceMomentAllowed(ctx, privacy, store, *moment, support) {
			return []ReadableEvidence{}, nil
		}
	}
	for _, row := range rows {
		if !a.activityDependencyAllowsModelRead(row.ActivitySessionID, entry.Apps) {
			return []ReadableEvidence{}, nil
		}
	}
	seenSessions, seenText := map[string]bool{}, map[string]bool{}
	unique := []ReadableEvidence{}
	for _, row := range rows {
		if seenSessions[row.ActivitySessionID] || seenText[row.Text] {
			continue
		}
		seenSessions[row.ActivitySessionID], seenText[row.Text] = true, true
		unique = append(unique, row)
	}
	return unique[:min(8, len(unique))], nil
}

// A moment title can summarize several runs. Do not broaden one supporting
// session into a neighbour's topic, even when the neighbour is readable.
func (a *App) memoryEvidenceMomentAllowed(ctx context.Context, privacy *mcpReadPrivacy, store *ActivityStore, moment ActivityRow, support map[string]bool) bool {
	allowed, _, err := a.searchMomentAllowed(ctx, privacy, moment.RunIDs, moment.ActiveMsByApp, moment.StartMs, moment.EndMs)
	if err != nil || !allowed || pii.HasRedaction(moment.Title) {
		return false
	}
	for _, runID := range composeParseStrings(moment.RunIDs) {
		run, err := store.GetTimelineRunByID(runID)
		if err != nil || run == nil || run.EvidenceExcluded {
			return false
		}
		for _, id := range run.SessionIDs {
			if !support[id] {
				return false
			}
		}
	}
	return true
}
