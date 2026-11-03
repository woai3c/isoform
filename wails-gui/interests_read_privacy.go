package main

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"wails-gui/internal/interests"
	"wails-gui/storage"
)

// Stored interest names and explanations are derivatives, not fresh user
// input. All their original references must still authorize a model read.
// A missing source never becomes permission to replay its old derivative.
func (a *App) interestsReadFilter() func(interests.Interest) bool {
	allowMemory, memoryErr := a.memoryReadFilter()
	_, policyErr := a.activityReadPrivacyPolicy()
	checked := map[interests.Ref]bool{}
	allowed := map[interests.Ref]bool{}
	var purposes map[string]PurposeOpenLoopDetail
	var hashes map[string]bool
	// collectible rebuilds the search/named hashes the evidence pass would
	// derive right now, with the same collection logic and the same window.
	// A stored hash one of them still produces names something the sources
	// may still show; a hash they no longer produce is refused.
	collectible := func() map[string]bool {
		if hashes != nil {
			return hashes
		}
		hashes = map[string]bool{}
		now := time.Now()
		in := collectInterestInput(a, now.Add(-interests.RefreshWindow), now)
		if in.PeopleUnknown {
			// Without the people list nothing may be re-authorized.
			return hashes
		}
		items, _ := interestItems(in)
		for _, item := range items {
			switch item.Source {
			case interests.SourceSearch, interests.SourceNamed:
				hashes[item.Ref] = true
			default:
			}
		}
		return hashes
	}
	check := func(ref interests.Ref) bool {
		kind, id, ok := strings.Cut(ref.Key, ":")
		if !ok || id == "" {
			return false
		}
		switch ref.Source {
		case interests.SourceAbout:
			if kind != "about" || memoryErr != nil || allowMemory == nil {
				return false
			}
			memoryID, point, found := strings.Cut(id, "#")
			index, err := strconv.Atoi(point)
			if !found || err != nil || index < 0 {
				return false
			}
			entry, err := a.GetMemoryEntry(memoryID)
			return err == nil && allowMemory(entry.ID, entry.Content)
		case interests.SourceActivity:
			if a.activity == nil || a.activity.store == nil || policyErr != nil {
				return false
			}
			store := a.activity.store
			switch kind {
			case "activity":
				row, err := store.GetActivityByID(id)
				return err == nil && row != nil && len(a.filterComposerModelCandidates(store, []ActivityRow{*row})) == 1
			case "semantic":
				var appsJSON, sourcesJSON string
				var source activitySourceProvenance
				err := store.db.QueryRow(`SELECT apps,source_refs,start_ms,end_ms FROM activity_semantic_events WHERE id=?`, id).
					Scan(&appsJSON, &sourcesJSON, &source.StartTime, &source.EndTime)
				if err != nil {
					return false
				}
				return a.interestSemanticSourceAllowsRead(SemanticEventRow{ID: id, Apps: appsJSON, SourceRefs: sourcesJSON, StartMs: source.StartTime, EndMs: source.EndTime})
			default:
				return false
			}
		case interests.SourceChat:
			if kind != "chat" || a.store == nil {
				return false
			}
			ctx := context.Background()
			row, err := a.store.LoadSession(ctx, id)
			if err != nil || row == nil || row.AutomationResult || row.MeetingID != "" || row.Kind == storage.SessionKindMain || strings.HasPrefix(row.Label, "Transcript:") {
				return false
			}
			// LoadSession omits Hidden; query the stored flag explicitly.
			hidden, err := a.store.SessionHidden(ctx, id)
			return err == nil && !hidden
		case interests.SourceGoal:
			if kind != "goal" {
				return false
			}
			if purposes == nil {
				// Reuse Today's complete Purpose evidence closure, including
				// corrections and carried-forward fields from earlier events.
				purposes = map[string]PurposeOpenLoopDetail{}
				for _, detail := range a.collectTodayChatSources(context.Background(), time.Now()).Purposes {
					purposes[detail.OpenLoop.ID] = detail
				}
			}
			detail, ok := purposes[id]
			return ok && detail.Current.Title != "" && detail.Current.OccurredAt == ref.At
		case interests.SourceSearch, interests.SourceNamed:
			// The reference stores only a content hash, but it stands for one
			// typed query or one named thing: it is authorized while the same
			// collection still derives that hash from readable sources.
			if kind != string(ref.Source) {
				return false
			}
			return collectible()[ref.Key]
		default:
			return false
		}
	}
	return func(interest interests.Interest) bool {
		if a == nil || a.IsClosedEyesModeEnabled() || len(interest.Evidence) == 0 {
			return false
		}
		for _, ref := range interest.Evidence {
			if !checked[ref] {
				checked[ref], allowed[ref] = true, check(ref)
			}
			if !allowed[ref] {
				return false
			}
		}
		return true
	}
}

// interestsFollowingReadFilter checks each activity identity, reusing verified
// source facts and reading only referenced cache misses. Other evidence kinds
// keep their complete checks in the background pass and wait for its snapshot.
func (a *App) interestsFollowingReadFilter(ctx context.Context) func(interests.Interest) bool {
	policy, err := a.activityReadPrivacyPolicyContext(ctx)
	return func(interest interests.Interest) bool {
		if a == nil || a.IsClosedEyesModeEnabled() || err != nil ||
			len(interest.Evidence) == 0 {
			return false
		}
		for _, ref := range interest.Evidence {
			if ctx.Err() != nil || ref.Source != interests.SourceActivity {
				return false
			}
			kind, id, ok := strings.Cut(ref.Key, ":")
			if !ok || id == "" || a.activity == nil || a.activity.store == nil {
				return false
			}
			switch kind {
			case "activity":
				row, err := a.activity.store.GetActivityByIDContext(ctx, id)
				if err != nil || row == nil || !a.interestFollowingActivity(ctx, *row, policy) {
					return false
				}
			case "semantic":
				if !a.interestFollowingSemantic(ctx, id, policy) {
					return false
				}
			default:
				return false
			}
		}
		return ctx.Err() == nil
	}
}

func (a *App) interestFollowingActivity(ctx context.Context, row ActivityRow, policy activityReadPrivacyPolicy) bool {
	ids := composeParseStrings(row.RunIDs)
	if len(ids) == 0 || len(ids) > 64 {
		return false
	}
	encoded, _ := json.Marshal(ids)
	store := a.activity.store
	cursor, err := store.reader().QueryContext(ctx, `SELECT `+timelineRunColumns+` FROM activity_timeline_groups WHERE id IN (SELECT value FROM json_each(?))`, string(encoded))
	if err != nil {
		return false
	}
	batch, err := scanTimelineRunRowsContext(ctx, cursor)
	cursor.Close()
	if err != nil || len(batch) != len(ids) {
		return false
	}
	runs := make(map[string]ActivityTimelineRunRow, len(batch))
	sessionIDs := []string{}
	seenSessions := map[string]bool{}
	for _, run := range batch {
		runs[run.ID] = run
		for _, id := range run.SessionIDs {
			if !seenSessions[id] {
				seenSessions[id] = true
				sessionIDs = append(sessionIDs, id)
			}
		}
		if len(sessionIDs) > 64 {
			return false
		}
	}
	sessions, err := store.GetSessionsByIDsContext(ctx, sessionIDs)
	if err != nil {
		return false
	}
	allowedSessions := map[string]bool{}
	return composerCandidateAllowsModelRead(row, func(id string) bool {
		run, found := runs[id]
		if !found || activitySourceAppIsSensitive(run.AppKey, run.AppName) || len(run.SessionIDs) == 0 || len(run.SessionIDs) > 64 {
			return false
		}
		for _, sourceID := range run.SessionIDs {
			allowed, checked := allowedSessions[sourceID]
			if !checked {
				session := sessions[sourceID]
				allowed = session != nil && a.interestFollowingSessionAllowed(ctx, *session, policy)
				allowedSessions[sourceID] = allowed
			}
			if !allowed {
				return false
			}
		}
		return ctx.Err() == nil
	})
}

func (a *App) interestFollowingSessionAllowed(ctx context.Context, session ActivitySessionRow, policy activityReadPrivacyPolicy) bool {
	declared := activitySourceProvenance{StartTime: session.StartTime, EndTime: session.EndTime, Private: session.TriageStatus == "protected"}
	if session.AppNames != "" && json.Unmarshal([]byte(session.AppNames), &declared.Apps) != nil {
		return false
	}
	declared.Apps = append(declared.Apps, session.MeetingApp)
	// Recording dependencies need their complete background closure here.
	if session.TranscriptPath != "" || !policy.allows(declared) {
		return false
	}
	store := a.activity.store
	source, found, err := store.cachedActivitySessionProvenanceContext(ctx, session.ID)
	if err != nil {
		return false
	}
	if !found {
		// A referenced session outside the bounded activity pre-pass still
		// needs its own complete provenance check, never a whole-window read.
		source, err = store.activitySessionProvenanceContext(ctx, session)
	}
	return err == nil && policy.allows(source) && ctx.Err() == nil
}

func (a *App) interestFollowingSemantic(ctx context.Context, id string, policy activityReadPrivacyPolicy) bool {
	store := a.activity.store
	var row SemanticEventRow
	err := store.reader().QueryRowContext(ctx, `SELECT apps,source_refs,start_ms,end_ms FROM activity_semantic_events WHERE id=?`, id).
		Scan(&row.Apps, &row.SourceRefs, &row.StartMs, &row.EndMs)
	if err != nil {
		return false
	}
	var refs semanticSourceRefs
	source := activitySourceProvenance{StartTime: row.StartMs, EndTime: row.EndMs}
	if json.Unmarshal([]byte(row.Apps), &source.Apps) != nil || json.Unmarshal([]byte(row.SourceRefs), &refs) != nil ||
		len(refs.SessionIDs) == 0 || len(refs.SessionIDs) > 64 || len(refs.SnapshotIDs) > 64 || !policy.allows(source) {
		return false
	}
	sessions, err := store.GetSessionsByIDsContext(ctx, refs.SessionIDs)
	if err != nil || len(sessions) != len(refs.SessionIDs) {
		return false
	}
	for _, sessionID := range refs.SessionIDs {
		if !a.interestFollowingSessionAllowed(ctx, *sessions[sessionID], policy) {
			return false
		}
	}
	for _, snapshotID := range refs.SnapshotIDs {
		var sessionID string
		if err := store.reader().QueryRowContext(ctx, `SELECT session_id FROM activity_snapshots WHERE id=?`, snapshotID).Scan(&sessionID); err != nil || sessions[sessionID] == nil {
			return false
		}
	}
	return ctx.Err() == nil
}

// Derivative app/time metadata cannot stand in for its original sessions or
// snapshots. Validate the complete recorded closure, including foreign and
// missing snapshot identities, before any semantic text or entities are read.
func (a *App) interestSemanticSourceAllowsRead(row SemanticEventRow) bool {
	if a == nil || a.activity == nil || a.activity.store == nil {
		return false
	}
	policy, err := a.activityReadPrivacyPolicy()
	if err != nil {
		return false
	}
	var refs semanticSourceRefs
	source := activitySourceProvenance{StartTime: row.StartMs, EndTime: row.EndMs}
	if json.Unmarshal([]byte(row.Apps), &source.Apps) != nil || json.Unmarshal([]byte(row.SourceRefs), &refs) != nil || len(refs.SessionIDs) == 0 || !policy.allows(source) {
		return false
	}
	sessions := map[string]bool{}
	for _, id := range refs.SessionIDs {
		if !a.activityDependencyAllowsModelRead(id, nil) {
			return false
		}
		sessions[id] = true
	}
	for _, id := range refs.SnapshotIDs {
		var session string
		if err := a.activity.store.db.QueryRow(`SELECT session_id FROM activity_snapshots WHERE id=?`, id).Scan(&session); err != nil || !sessions[session] {
			return false
		}
	}
	return true
}
