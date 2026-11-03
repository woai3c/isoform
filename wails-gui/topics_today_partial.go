package main

import (
	"context"
	"encoding/json"
	"log"
	"reflect"
	"strings"
	"time"

	"wails-gui/internal/interests"
	"wails-gui/storage"
)

const topicTodayHistoryLimit = 64

// idx_activities_end also orders equal timestamps by rowid. Following that
// order avoids sorting a whole tied batch before verifying its first rows.
const topicTodayRecentQuery = `SELECT ` + activityColumns + ` FROM activities WHERE end_ms>=? AND start_ms<=? ORDER BY end_ms DESC,rowid DESC LIMIT 4`

// Bootstrap four recent activities and a bounded batch of original historical
// references. Every row passes the same source gates; stored names grant no
// authority. This never commits identity, chat proofs or partial state.
func (s *topicsService) partialTodayViews(ctx context.Context, state topicsStateFile) []TopicView {
	views, _, _ := s.partialTodayViewsWithSnapshot(ctx, state, nil)
	return views
}

func (s *topicsService) partialTodayViewsWithSnapshot(ctx context.Context, state topicsStateFile, snapshot *topicTodaySnapshot) ([]TopicView, bool, bool) {
	views := []TopicView{}
	historyRequested, historyLoaded, sourcesVerified := 0, 0, 0
	semanticRequested, semanticVerified := 0, 0
	defer func() {
		withheld := map[string]bool{}
		for _, item := range s.app.interestsSvc().store.load().Interests {
			if !state.Overrides[item.ID].Removed {
				withheld[item.ID] = true
			}
		}
		for _, project := range state.Projects {
			if !state.Overrides[project.ID].Removed && (project.Name != "" || project.Deferred) {
				withheld[project.ID] = true
			}
		}
		for _, view := range views {
			delete(withheld, view.ID)
		}
		log.Printf("[topics] today level=info stage=bootstrap bootstrap_verified=%d bootstrap_withheld=%d history_requested=%d history_loaded=%d sources_verified=%d semantic_requested=%d semantic_verified=%d budget_exhausted=%t", len(views), len(withheld), historyRequested, historyLoaded, sourcesVerified, semanticRequested, semanticVerified, ctx.Err() != nil)
	}()
	if ctx.Err() != nil || s.app.activity == nil || s.app.activity.store == nil {
		return views, false, ctx.Err() != nil
	}
	store := s.app.activity.store
	before, err := storage.DatabaseRevision(ctx, store.db)
	if err != nil {
		return views, false, false
	}
	now := s.now()
	recentDone := timeTopicStep("today", "bootstrap_recent")
	rows, err := store.reader().QueryContext(ctx, topicTodayRecentQuery, now.Add(-topicWindow).UnixMilli(), now.UnixMilli())
	if err != nil {
		recentDone("rows", 0)
		return views, false, false
	}
	activities, err := scanActivityRowsContext(ctx, rows)
	rows.Close()
	recentDone("rows", len(activities))
	if err != nil {
		return views, false, false
	}
	recent := map[string]bool{}
	for _, row := range activities {
		recent[row.ID] = true
	}
	historyDone := timeTopicStep("today", "bootstrap_history")
	history, requested, err := s.partialTodayHistory(ctx, state, activities)
	historyRequested, historyLoaded = requested, len(history)
	historyDone("recent_rows", len(activities), "requested", requested, "loaded", len(history))
	if err != nil {
		return views, false, false
	}
	activities = append(activities, history...)
	if snapshot != nil {
		// Capture file identities before the provenance checks read headers or
		// sidecars. Only this bounded batch's contributors are relevant here.
		sessionFilesDone := timeTopicStep("today", "bootstrap_session_files")
		snapshot.Sessions, err = s.partialTodaySessionFiles(ctx, state, activities)
		sessionFilesDone("activities", len(activities), "paths", len(snapshot.Sessions.Paths))
		if err != nil {
			return views, false, ctx.Err() != nil
		}
	}
	sourceDone := timeTopicStep("today", "bootstrap_activity_sources")
	sourceRows := len(activities)
	activities, err = s.app.filterTopicRegistryActivities(ctx, activities)
	sourceDone("checked", sourceRows, "verified", len(activities))
	if err != nil {
		return views, false, ctx.Err() != nil
	}
	sourcesVerified = len(activities)
	sources, requested, err := s.partialTodaySemanticSources(ctx, state)
	semanticRequested, semanticVerified = requested, len(sources)
	if err != nil {
		return views, false, ctx.Err() != nil
	}
	for _, row := range activities {
		sources["activity:"+row.ID] = true
	}
	// Followed labels are derivatives too. Authorize only original activity
	// references in the freshly gated recent/historical batch. Other source
	// kinds or references beyond the cap wait for the complete background read.
	following := UserTopicsView{}
	if s.refreshRegistryNames(ctx, now) == nil {
		following = s.partialTodayFollowing(ctx, state, sources)
	}
	after, err := storage.DatabaseRevision(ctx, store.db)
	if err != nil || !reflect.DeepEqual(before, after) || ctx.Err() != nil {
		return views, false, ctx.Err() != nil
	}
	release, err := storage.LockDatabaseRevision(ctx, store.db, after)
	if err != nil {
		return views, false, false
	}
	defer release()
	projects, activities, fresh := s.partialTodayProjects(ctx, state, activities, recent, now)
	partial := buildTopicRegistry(topicRegistryData{existing: projects, activities: activities, following: following, now: now, selfName: s.app.topicRegistrySelfName(ctx), sidebarPins: state.SidebarPins})
	if ctx.Err() != nil {
		return views, false, ctx.Err() != nil
	}
	identities := map[string]bool{}
	for _, project := range projects {
		identities[project.ID] = project.Name != ""
	}
	for _, project := range partial.projects {
		isNew := len(project.ActivityIDs) > 0
		for _, id := range project.ActivityIDs {
			isNew = isNew && fresh[id]
		}
		identities[project.ID] = identities[project.ID] || isNew
	}
	for _, view := range partial.views {
		if view.Kind != TopicKindProject || identities[view.ID] {
			views = append(views, view)
		}
	}
	sortTopicViews(views)
	return views, true, false
}

// Historical rows cannot mint work identities: Pin and Open resolve the saved
// IDs and aliases. Reuse the full reader's source-derived identity validation,
// but require the complete saved closure here. Missing, protected, goal-only or
// over-budget identities wait for the full build rather than claiming a row
// under a different key. Stored keys may suppress a row, never authorize it.
func (s *topicsService) partialTodayProjects(ctx context.Context, state topicsStateFile, rows []ActivityRow, recent map[string]bool, now time.Time) ([]topicProject, []ActivityRow, map[string]bool) {
	readable, withheld := s.app.readableTopicProjects(ctx, state.Projects, rows, nil, now, nil, nil)
	known := topicRowsByID(rows)
	projects := []topicProject{}
	allowedKeys, blockedKeys := map[string]bool{}, map[string]bool{}
	blockedRows := map[string]bool{}
	savedRows := map[string]bool{}
	for i, saved := range state.Projects {
		if ctx.Err() != nil {
			return nil, nil, nil
		}
		complete := !state.Overrides[saved.ID].Removed && !topicProjectWithheld(readable[i], withheld) && len(saved.ActivityIDs) > 0 && len(saved.GoalIDs) == 0
		for _, id := range saved.ActivityIDs {
			savedRows[id] = true
			if _, ok := known[id]; !ok {
				complete = false
			}
		}
		if complete {
			projects = append(projects, readable[i])
			for _, key := range readable[i].MemberKeys {
				allowedKeys[key] = true
			}
		} else {
			// Opaque reservations prevent a new recent project from stealing
			// an old ID or alias even when its sources are no longer readable.
			projects = append(projects, topicProject{ID: saved.ID, Aliases: append([]string(nil), saved.Aliases...)})
			for _, key := range saved.MemberKeys {
				blockedKeys[key] = true
			}
			for _, id := range saved.ActivityIDs {
				blockedRows[id] = true
			}
		}
	}
	activities := []ActivityRow{}
	fresh := map[string]bool{}
	self := s.app.topicRegistrySelfName(ctx)
	for _, row := range rows {
		if ctx.Err() != nil {
			return nil, nil, nil
		}
		// Preserve the original recent-batch discovery of genuinely new work;
		// historical or saved contributors must resolve an existing identity.
		isNew := recent[row.ID] && !savedRows[row.ID]
		allowed, blocked := isNew, blockedRows[row.ID]
		for _, key := range newTopicActivityObservation(row, self).Keys {
			allowed = allowed || allowedKeys[key]
			blocked = blocked || blockedKeys[key]
		}
		if allowed && !blocked {
			activities = append(activities, row)
			fresh[row.ID] = isNew
		}
	}
	// Only identities still supported by the retained rows can seed the build.
	// This also prevents a validated project with a blocked bridge from showing
	// a saved label without any current observation in the partial projection.
	projects, _ = s.app.readableTopicProjects(ctx, projects, activities, nil, now, nil, nil)
	return projects, activities, fresh
}

// Look up original IDs, rather than assuming the newest eight activities still
// contain an older topic's contributors. A complete interest closure must fit
// in the bounded, privacy-verified batch before its label can be returned.
func (s *topicsService) partialTodayHistory(ctx context.Context, state topicsStateFile, recent []ActivityRow) ([]ActivityRow, int, error) {
	have := map[string]bool{}
	for _, row := range recent {
		have[row.ID] = true
	}
	wanted := []string{}
	add := func(id string) {
		if id != "" && !have[id] && len(wanted) < topicTodayHistoryLimit {
			have[id] = true
			wanted = append(wanted, id)
		}
	}
	for _, item := range s.app.interestsSvc().store.load().Interests {
		if ctx.Err() != nil {
			return nil, len(wanted), ctx.Err()
		}
		if state.Overrides[item.ID].Removed {
			continue
		}
		for _, ref := range item.Evidence {
			if ctx.Err() != nil {
				return nil, len(wanted), ctx.Err()
			}
			if ref.Source == interests.SourceActivity && strings.HasPrefix(ref.Key, "activity:") {
				add(strings.TrimPrefix(ref.Key, "activity:"))
			}
			if len(wanted) == topicTodayHistoryLimit {
				break
			}
		}
		if len(wanted) == topicTodayHistoryLimit {
			break
		}
	}
	for _, project := range state.Projects {
		if ctx.Err() != nil {
			return nil, len(wanted), ctx.Err()
		}
		if !state.Overrides[project.ID].Removed {
			for _, id := range project.ActivityIDs {
				if ctx.Err() != nil {
					return nil, len(wanted), ctx.Err()
				}
				add(id)
				if len(wanted) == topicTodayHistoryLimit {
					break
				}
			}
		}
		if len(wanted) == topicTodayHistoryLimit {
			break
		}
	}
	if len(wanted) == 0 {
		return nil, 0, ctx.Err()
	}
	encoded, _ := json.Marshal(wanted)
	rows, err := s.app.activity.store.reader().QueryContext(ctx, `SELECT `+activityColumns+` FROM activities WHERE id IN (SELECT value FROM json_each(?)) ORDER BY end_ms DESC,id DESC LIMIT ?`, string(encoded), topicTodayHistoryLimit)
	if err != nil {
		return nil, len(wanted), err
	}
	defer rows.Close()
	history, err := scanActivityRowsContext(ctx, rows)
	return history, len(wanted), err
}

// The caller holds the privacy/publication fences and a source revision fence
// through delivery. A missing, unsupported or unfinished proof never licenses a
// stored label. In particular, this is not the Closed Eyes stored-label path.
func (s *topicsService) partialTodayFollowing(ctx context.Context, state topicsStateFile, sources map[string]bool) UserTopicsView {
	svc := s.app.interestsSvc()
	user := svc.userChoices()
	floor := s.now().Add(-interests.RefreshWindow)
	var list []interests.Interest
	for _, item := range svc.store.load().Interests {
		if ctx.Err() != nil {
			return UserTopicsView{}
		}
		allowed := len(item.Evidence) > 0
		for _, ref := range item.Evidence {
			if ctx.Err() != nil {
				return UserTopicsView{}
			}
			// Check every original contributor, including mixed closures.
			allowed = allowed && ref.Source == interests.SourceActivity && sources[ref.Key]
			if !allowed {
				break
			}
		}
		if !allowed {
			continue
		}
		if last := item.Stats(time.Local).LastAt; !last.IsZero() && last.Before(floor) && !user.Pinned[item.ID] {
			continue
		}
		list = append(list, item)
	}
	svc.namesMu.Lock()
	names := append([]string(nil), svc.names...)
	svc.namesMu.Unlock()
	list = interests.Recheck(list, interests.Rules{Names: names, OwnNames: interestsOwnNames, Removed: user.Removed}, user.Pinned)
	if ctx.Err() != nil {
		return UserTopicsView{}
	}
	return userTopicsWithInterests(userTopicsLimit(interestTopics(list), state, len(list)+len(state.Overrides)), list)
}
