package main

import (
	"context"
	"sort"
	"strings"
	"time"

	"wails-gui/internal/interests"
)

// This derivative and its membership are committed together under topics.json's
// publication barrier. The adapter never rebuilds or writes during admission.
type topicChatSnapshot struct {
	MarkdownRevisions map[string]string `json:"markdownRevisions,omitempty"`
	Topic             ChatTopic         `json:"topic"`
	Keys              ChatTopicKeys     `json:"keys"`
	Aliases           []string          `json:"aliases,omitempty"`
	DerivedAt         int64             `json:"derivedAt"`
	ValidUntil        int64             `json:"validUntil"`
}

type registryTopicSource struct{ app *App }

var _ TopicSource = (*registryTopicSource)(nil)

func (r *registryTopicSource) snapshot(ctx context.Context, id string) (topicChatSnapshot, error) {
	paused := r.app.IsClosedEyesModeEnabled()
	if paused && !pausedTopicReadAllowed(ctx) {
		return topicChatSnapshot{}, errTodayChatObservationPaused
	}
	if err := ctx.Err(); err != nil {
		return topicChatSnapshot{}, err
	}
	for _, snapshot := range r.app.topicsSvc().store.load().ChatTopics {
		if snapshot.Topic.ID != id && !topicStringIn(snapshot.Aliases, id) {
			continue
		}
		if !topicDisplayNameSafe(snapshot.Topic.Name) {
			return topicChatSnapshot{}, errTodayChatItemUnavailable
		}
		now := r.app.topicsSvc().now().UnixMilli()
		if paused {
			var err error
			now, err = r.app.observationPauseCoverageStart()
			if err != nil || now <= 0 || now > time.Now().UnixMilli() {
				return topicChatSnapshot{}, errTodayChatItemUnavailable
			}
		}
		if snapshot.DerivedAt <= 0 || now < snapshot.DerivedAt || paused && snapshot.DerivedAt == now || now >= snapshot.ValidUntil {
			return topicChatSnapshot{}, errTodayChatItemUnavailable
		}

		snapshot.Topic.ID = id // A3 preserves the requested tag, including aliases.
		return snapshot, nil
	}
	return topicChatSnapshot{}, errTodayChatItemUnavailable
}

// TopicSnapshot is the combined admission seam. A3 validates the returned
// refs once against its already collected sources under publication barriers.
func (r *registryTopicSource) TopicSnapshot(ctx context.Context, id string) (ChatTopic, ChatTopicKeys, error) {
	snapshot, err := r.snapshot(ctx, id)
	return snapshot.Topic, snapshot.Keys, err
}
func (r *registryTopicSource) read(ctx context.Context, id string) (topicChatSnapshot, error) {
	snapshot, err := r.snapshot(ctx, id)
	if err != nil {
		return snapshot, err
	}
	current, err := r.app.chatTopicSourceFingerprint(ctx, snapshot.Topic.Refs)
	if err != nil {
		return topicChatSnapshot{}, err
	}
	if current != snapshot.Topic.SourceFingerprint {
		return topicChatSnapshot{}, errTodayChatItemUnavailable
	}
	return snapshot, nil
}

func (r *registryTopicSource) TopicByID(ctx context.Context, id string) (ChatTopic, error) {
	snapshot, err := r.read(ctx, id)
	return snapshot.Topic, err
}
func (r *registryTopicSource) TopicKeys(ctx context.Context, id string) (ChatTopicKeys, error) {
	snapshot, err := r.read(ctx, id)
	return snapshot.Keys, err
}

// Capture the same fingerprint as chatTopicSourceFingerprint at derivation,
// sharing one Today source collection and resolution cache across this batch.
// No missing or unsupported contributor is truncated or replaced by a wrapper.
func (s *topicsService) deriveChatTopics(ctx context.Context, result topicRegistryResult, in topicRegistryData, outcomes map[string]*ActivityThreadOutcome, revision topicRegistryRevision) ([]topicChatSnapshot, error) {
	if len(result.views) == 0 || s.app.IsClosedEyesModeEnabled() {
		return nil, ctx.Err()
	}
	sources := s.registryChatSources(ctx, in, revision)
	resolved := map[string]topicChatFingerprintInput{}
	out := []topicChatSnapshot{}
	storedInterests := s.app.interestsSvc().store.load().Interests
	storedState := s.store.load()
	knownActivities := topicRowsByID(in.activities)
	knownGoals := map[string]bool{}
	for _, goal := range in.goals {
		knownGoals[goal.ID] = true
	}
	for _, view := range result.views {
		if ctx.Err() != nil {
			break
		}
		refs := []TodayChatContextRef{}
		add := func(kind, id string) {
			if id == "" {
				return
			}
			for _, ref := range refs {
				if ref.SourceKind == kind && ref.SourceID == id {
					return
				}
			}
			refs = append(refs, TodayChatContextRef{SourceKind: kind, SourceID: id})
		}
		valid := true
		keys := result.keys[view.ID]
		if view.Kind == TopicKindProject {
			var project topicProject
			for _, p := range result.projects {
				if p.ID == view.ID {
					project = p
					break
				}
			}
			for _, id := range project.ActivityIDs {
				if _, known := knownActivities[id]; !known {
					continue
				}
				add("activity", id)
				if outcome := outcomes[id]; outcome != nil {
					add("activity", threadOutcomeSourceID(id, outcome))
				}
			}
			for _, id := range project.GoalIDs {
				if !knownGoals[id] {
					continue
				}
				add("purpose", id)
			}
			for _, meeting := range in.meetings {
				if topicProjectHasAnyKey(project, topicMeetingKeys(meeting, in.selfName)) || topicNameWithin(meeting.Title, project.Name) {
					add("calendar", meeting.ID)
				}
			}
		}
		// Finds can supply latest text in either kind. Include the original
		// search contributors as well as the find, never refreshed evidence.
		if view.Kind == TopicKindFollowing || view.SourceCounts["find"] > 0 {
			interestID := keys.InterestID
			if interestID == "" {
				interestID = view.ID
			}
			interestIDs := map[string]bool{interestID: true}
			for alias, canonical := range result.canonical {
				if canonical == view.ID && result.keys[alias].InterestID != "" {
					interestIDs[result.keys[alias].InterestID] = true
				}
			}
			found := false
			for _, it := range storedInterests {
				if !interestIDs[it.ID] {
					continue
				}
				found = true
				add("interest", it.ID)
				for _, ref := range it.Evidence {
					if !topicChatInterestRef(ref, add) {
						valid = false
					}
				}
			}
			if view.Kind == TopicKindFollowing && !found {
				valid = false
			}
			for _, find := range in.finds {
				if !interestIDs[find.TopicID] {
					continue
				}
				add("find", find.ID)
			}
			if view.SourceCounts["find"] > 0 {
				for _, sent := range storedState.Finds.Sent {
					if sent.ReadSources == nil {
						valid = false
						continue
					}
					for _, ref := range sent.ReadSources.Evidence {
						if !topicChatInterestRef(ref, add) {
							valid = false
						}
					}
				}
			}
		}
		if !valid || len(refs) == 0 || len(refs) > 64 {
			continue
		}
		fingerprint, err := s.app.chatTopicFingerprintFromSources(ctx, refs, sources, resolved)
		if err != nil {
			continue
		}
		topic := ChatTopic{ID: view.ID, Name: view.Name, Status: view.Status, Refs: refs, SourceFingerprint: fingerprint}
		if view.Latest != nil {
			topic.Latest = view.Latest.Text
		}
		if view.Next != nil {
			topic.Next = view.Next.Text
		}
		var aliases []string
		for alias, canonical := range result.canonical {
			if canonical == view.ID {
				aliases = append(aliases, alias)
			}
		}
		sort.Strings(aliases)
		snapshot := topicChatSnapshot{Topic: topic, Keys: ChatTopicKeys{ThreadKeys: keys.ThreadKeys, GoalIDs: keys.GoalIDs, InterestID: keys.InterestID}, Aliases: aliases, DerivedAt: in.now.UnixMilli(), ValidUntil: topicChatValidUntil(in).UnixMilli()}
		snapshot.MarkdownRevisions = map[string]string{}
		for _, ref := range topic.Refs {
			if ref.SourceKind != "memory" && ref.SourceKind != "knowledge" {
				continue
			}
			revision, err := topicMarkdownRevision(ref.SourceKind, ref.SourceID)
			if err != nil {
				valid = false
				break
			}
			snapshot.MarkdownRevisions[ref.SourceKind+":"+ref.SourceID] = revision
		}
		if !valid {
			continue
		}
		// An unchanged, still-fresh derivative keeps its original derivation
		// time. Merely reading Today must not churn the admission file digest.
		for _, old := range storedState.ChatTopics {
			if old.Topic.ID != view.ID || old.DerivedAt <= 0 || old.DerivedAt > snapshot.DerivedAt || old.ValidUntil != snapshot.ValidUntil {
				continue
			}
			candidate := snapshot
			candidate.DerivedAt = old.DerivedAt
			if proactiveVoiceHash(candidate) == proactiveVoiceHash(old) {
				snapshot = candidate
				break
			}
		}
		out = append(out, snapshot)
		if s.registryDuringChat != nil {
			s.registryDuringChat(view.ID)
		}
	}
	return out, ctx.Err()
}

func topicChatInterestRef(ref interests.Ref, add func(string, string)) bool {
	kind, id, ok := strings.Cut(ref.Key, ":")
	if !ok || id == "" {
		return false
	}
	switch ref.Source {
	case interests.SourceActivity:
		if kind != "activity" {
			return false
		}
		add("activity", id)
	case interests.SourceGoal:
		if kind != "goal" {
			return false
		}
		add("purpose", id)
	case interests.SourceAbout:
		if kind != "about" {
			return false
		}
		id, _, _ = strings.Cut(id, "#")
		add("memory", id)
	default:
		// Re-derivable hashes and legacy chat labels retain no original source
		// closure. They remain following cards, not contextual chat authority.
		return false
	}
	return true
}

// The stored naming record is an additional identity, never permission for its
// evidence. Contextual following topics also carry every supported original
// contributor as a separate ref. Unsupported legacy closures fail closed.
func (a *App) resolveTopicInterest(ctx context.Context, id string, sources todayChatSources) (todayChatItem, error) {
	if a.IsClosedEyesModeEnabled() {
		return todayChatItem{}, errTodayChatObservationPaused
	}
	state := a.topicsSvc().store.load()
	if override, ok := state.Overrides[id]; ok && override.Removed {
		return todayChatItem{}, errTodayChatItemUnavailable
	}
	for _, it := range a.interestsSvc().store.load().Interests {
		if it.ID != id {
			continue
		}
		if len(it.Evidence) == 0 {
			return todayChatItem{}, errTodayChatItemUnavailable
		}
		// Goal evidence reuses this collection's complete Today projection;
		// the generic interest filter would collect all sources again.
		remaining := it
		remaining.Evidence = nil
		for _, ref := range it.Evidence {
			if !topicChatInterestRef(ref, func(string, string) {}) {
				return todayChatItem{}, errTodayChatItemUnavailable
			}
			if ref.Source != interests.SourceGoal {
				remaining.Evidence = append(remaining.Evidence, ref)
				continue
			}
			_, goalID, _ := strings.Cut(ref.Key, ":")
			allowed := false
			for _, goal := range sources.Purposes {
				if goal.OpenLoop.ID == goalID && goal.Current.Title != "" && goal.Current.OccurredAt == ref.At {
					allowed = true
					break
				}
			}
			if !allowed {
				return todayChatItem{}, errTodayChatItemUnavailable
			}
		}
		if len(remaining.Evidence) > 0 && !a.interestsReadFilter()(remaining) {
			return todayChatItem{}, errTodayChatItemUnavailable
		}
		// Include the naming record and its user override so a rename or removal
		// invalidates admission/history even if the original activity is intact.
		text := proactiveVoiceHash(struct {
			Interest interests.Interest
			Override topicOverride
		}{it, state.Overrides[id]})
		return todayChatItem{Record: todayChatRecord{Kind: "interest", ID: id, Text: text}}, ctx.Err()
	}
	return todayChatItem{}, errTodayChatItemUnavailable
}

// Clock transitions invalidate derived status/next even when source bytes do
// not change. Refreshing Today derives a new snapshot; admission never does.
func topicChatValidUntil(in topicRegistryData) time.Time {
	now := in.now
	until := time.UnixMilli(startOfDayLocal(now.UnixMilli())).In(time.Local).AddDate(0, 0, 1)
	take := func(at time.Time) {
		if at.After(now) && at.Before(until) {
			until = at
		}
	}
	for _, row := range in.activities {
		take(time.UnixMilli(topicActivityAt(row)).Add(topicRegistryMovingFor))
	}
	for _, meeting := range in.meetings {
		take(time.UnixMilli(meeting.StartAt))
	}
	return until
}

// Revision-keyed local source cache: repeated sidebar/list/pin reads share one
// collection. Source/policy changes or withdrawal invalidate it; time-dependent
// facts are rendered against each current derivation time.
type topicChatSourceCache struct {
	revision topicRegistryRevision
	sources  todayChatSources
	until    time.Time
}

func (s *topicsService) cachedRegistryChatSources(now time.Time, revision topicRegistryRevision) (todayChatSources, bool, uint64) {
	s.chatCacheMu.Lock()
	defer s.chatCacheMu.Unlock()
	if c := s.chatCache; c != nil && now.Before(c.until) && !now.Before(c.sources.Now) && sameTopicRegistrySources(c.revision, revision) {
		sources := c.sources
		sources.Now = now
		return sources, true, s.chatCacheEpoch
	}
	return todayChatSources{}, false, s.chatCacheEpoch
}
func (s *topicsService) registryChatSources(ctx context.Context, in topicRegistryData, revision topicRegistryRevision) todayChatSources {
	cached, hit, epoch := s.cachedRegistryChatSources(in.now, revision)
	if hit {
		return cached
	}
	sources := s.app.collectTodayChatSourcesWithActivities(ctx, in.now, &in.activities)
	if ctx.Err() == nil {
		s.chatCacheMu.Lock()
		if s.chatCacheEpoch == epoch {
			until := topicChatValidUntil(in)
			if maxAge := in.now.Add(time.Minute); maxAge.Before(until) {
				until = maxAge
			}
			s.chatCache = &topicChatSourceCache{revision: revision, sources: sources, until: until}
		}
		s.chatCacheMu.Unlock()
	}
	return sources
}
func (s *topicsService) invalidateChatSources() {
	s.chatCacheMu.Lock()
	s.chatCacheEpoch++
	s.chatCache = nil
	s.chatCacheMu.Unlock()
	s.registryCacheMu.Lock()
	s.registryPartial = nil
	s.registryCacheMu.Unlock()
}

// ListWorkTopicIDs enumerates only work topics from the published registry.
func (r *registryTopicSource) ListWorkTopicIDs() []string {
	return r.app.topicsSvc().ListWorkTopicIDs()
}
