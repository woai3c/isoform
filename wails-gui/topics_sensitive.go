package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"wails-gui/internal/pii"
)

// A policy addition withdraws stored derivatives synchronously, including
// during Closed Eyes. Original opaque memberships remain eligible only for a
// later fresh source check; saved names/claim keys are never retained.
func (a *App) scrubSensitiveTopics() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return a.scrubSensitiveTopicsContext(ctx)
}

func (a *App) clearSensitiveChatTopics() error {
	svc := a.topicsSvc()
	svc.invalidateChatSources()
	svc.store.publication.Lock()
	defer svc.store.publication.Unlock()
	svc.store.mu.Lock()
	defer svc.store.mu.Unlock()
	state := svc.store.loadLocked()
	if len(state.ChatTopics) == 0 && state.Today == nil {
		return nil
	}
	state.ChatTopics = nil
	state.Today = nil
	_, err := svc.store.saveLocked(state)
	return err
}

func (a *App) scrubSensitiveTopicsContext(ctx context.Context) (resultErr error) {
	// Policy loads before Activity storage at startup. Missing storage is not
	// source withdrawal: preserve projects and user choices until it is ready.
	if a.activity == nil || a.activity.store == nil {
		return a.clearSensitiveChatTopics()
	}
	svc := a.topicsSvc()
	svc.invalidateChatSources()
	// Registered before publication unlocks so failure cleanup runs afterwards.
	// It needs only topic storage, even when a source/lock is unavailable.
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, a.clearSensitiveChatTopics())
		}
	}()
	// Follow publication order; acquire all or release while waiting. Memory
	// Forget cannot own its SQL transaction while we hold topic publication.
	var releases []func()
	unlock := func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
		releases = nil
	}
	if err := lockTodayChatPublication(ctx, func() bool {
		for _, lock := range []struct {
			try     func() bool
			release func()
		}{
			{memoryMutationMu.TryLock, memoryMutationMu.Unlock},
			{memoryFileLocks.publication.TryLock, memoryFileLocks.publication.Unlock},
			{pii.TryLockSensitiveAppsForPublication, pii.UnlockSensitiveAppsForPublication},
			{svc.store.publication.TryLock, svc.store.publication.Unlock},
		} {
			if !lock.try() {
				unlock()
				return false
			}
			releases = append(releases, lock.release)
		}
		return true
	}); err != nil {
		return err
	}
	defer unlock()
	state := svc.store.load()
	if len(state.Projects)+len(state.ChatTopics) == 0 {
		return nil
	}
	ids, goals := map[string]bool{}, map[string]bool{}
	for _, p := range state.Projects {
		for _, id := range p.ActivityIDs {
			ids[id] = true
		}
		for _, id := range p.GoalIDs {
			goals[id] = true
		}
	}
	for _, snap := range state.ChatTopics {
		for _, ref := range snap.Topic.Refs {
			switch ref.SourceKind {
			case "activity":
				ids[ref.SourceID] = true
			case "purpose":
				goals[ref.SourceID] = true
			}
		}
	}
	w := topicWithdrawal{kind: "activity", ids: map[string]bool{}, goals: map[string]bool{}}
	store := a.activity.store
	encoded, _ := json.Marshal(sortedTopicKeys(ids))
	rows, err := store.reader().QueryContext(ctx, `SELECT a.id,a.active_ms_by_app,r.app_key,r.app_name FROM activities a LEFT JOIN json_each(CASE WHEN json_valid(a.run_ids) THEN a.run_ids ELSE '[]' END) m LEFT JOIN activity_timeline_groups r ON r.id=m.value WHERE a.id IN (SELECT value FROM json_each(?))`, string(encoded))
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for rows.Next() {
		var id, apps string
		var key, name *string
		if err := rows.Scan(&id, &apps, &key, &name); err != nil {
			rows.Close()
			return err
		}
		seen[id] = true
		if composerBucketContainsSensitiveApp(ActivityRow{ActiveMsByApp: apps}) || key == nil || name == nil || activitySourceAppIsSensitive(*key, *name) {
			w.ids[id] = true
		}
	}
	for id := range ids {
		if !seen[id] {
			w.ids[id] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	details, err := NewPurposeMapStore(store.db).ListDetailsAllContext(ctx)
	if err != nil {
		return err
	}
	policy, err := a.activityReadPrivacyPolicy()
	if err != nil {
		return err
	}
	allowsDependency := func(id string, apps []string) bool {
		source, err := store.activityDependencyProvenance(id, apps)
		return err == nil && policy.allowsStoredDerivative(source)
	}
	markdownAllowed := map[string]bool{}
	allowsMarkdown := func(kind, id, expectedHash string) bool {
		key := kind + ":" + id + ":" + expectedHash
		if allowed, ok := markdownAllowed[key]; ok {
			return allowed
		}
		id = proactiveVoiceCurrentMarkdownID(id)
		var content string
		var readErr error
		if strings.HasPrefix(kind, "memory") {
			entry, err := a.GetMemoryEntry(id)
			content, readErr = entry.Content, err
		} else {
			entry, err := a.GetKnowledgeEntry(id)
			content, readErr = entry.Content, err
		}
		allowed := readErr == nil && a.memorySourceAllowsReadWithDependency(content, allowsDependency) && (expectedHash == "" || agencyPayloadHash([]byte(strings.TrimSpace(content))) == expectedHash)
		markdownAllowed[key] = allowed
		return allowed
	}
	kept := []topicChatSnapshot{}
	for _, snapshot := range state.ChatTopics {
		allowed := true
		for _, ref := range snapshot.Topic.Refs {
			switch ref.SourceKind {
			case "memory", "knowledge":
				revision, err := topicMarkdownRevision(ref.SourceKind, ref.SourceID)
				allowed = allowed && err == nil && snapshot.MarkdownRevisions[ref.SourceKind+":"+ref.SourceID] == revision && allowsMarkdown(ref.SourceKind, ref.SourceID, "")
			}
		}
		if allowed {
			kept = append(kept, snapshot)
		}
	}
	state.ChatTopics = kept
	for changed := true; changed; {
		changed = false
		for _, d := range details {
			if w.goals[d.OpenLoop.ID] {
				continue
			}
			for _, e := range d.Evidence {
				bad := false
				switch e.SourceKind {
				case "activity", "activity_summary":
					bad = w.ids[e.SourceID]
				case "activity_session", "meeting":
					bad = !allowsDependency(e.SourceID, nil)
				case "purpose", "purpose_open_loop":
					bad = w.goals[e.SourceID]
				case "memory", "memory_markdown", "knowledge", "knowledge_markdown":
					bad = e.SourceHash == "" || !allowsMarkdown(e.SourceKind, e.SourceID, e.SourceHash)
				case reminderPurposeSourceKind:
					bad = activitySourceAppIsSensitive("com.apple.reminders", "Reminders")
				case userPurposeSourceKind, "explicit_user_statement", "user_statement", activityFollowUpSourceKind:
					// The user's own words are independent of observed app data.
				default:
					bad = true
				}
				if bad {
					w.goals[d.OpenLoop.ID] = true
					changed = true
					break
				}
			}
			if err := ctx.Err(); err != nil {
				return err
			}
		}
	}
	scrubTopicState(&state, w, true)
	svc.store.mu.Lock()
	defer svc.store.mu.Unlock()
	_, err = svc.store.saveLocked(state)
	return err
}
