package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"wails-gui/internal/pii"
	"wails-gui/storage"
)

type topicChatProof struct {
	Version     int                   `json:"version"`
	Refs        []TodayChatContextRef `json:"refs"`
	Fingerprint string                `json:"fingerprint"`
	PausedAt    int64                 `json:"pausedAt,omitempty"`
}

// Persist identities and source digests, never the model's contextual prose.
// Bind only records actually packed into this turn, including related sources.
func (a *App) topicChatAdmissionMetadataFromSources(ctx context.Context, refs []TodayChatContextRef, sources todayChatSources) (string, error) {
	refs = append([]TodayChatContextRef{}, refs...)
	unique := []TodayChatContextRef{}
	seen := map[string]bool{}
	for _, ref := range refs {
		key := ref.SourceKind + ":" + ref.SourceID
		if !seen[key] {
			seen[key] = true
			unique = append(unique, TodayChatContextRef{SourceKind: ref.SourceKind, SourceID: ref.SourceID})
		}
	}
	resolved := sources.topicResolved
	if resolved == nil {
		resolved = map[string]topicChatFingerprintInput{}
	}
	fingerprint, err := a.chatTopicFingerprintFromSources(ctx, unique, sources, resolved)
	if err != nil {
		return "", err
	}
	var pausedAt int64
	if pausedTopicReadAllowed(ctx) && a.IsClosedEyesModeEnabled() {
		pausedAt, err = a.observationPauseCoverageStart()
		if err != nil || pausedAt <= 0 {
			return "", errTodayChatItemUnavailable
		}
	}
	raw, err := json.Marshal(struct {
		TopicContext topicChatProof `json:"topicContext"`
	}{topicChatProof{Version: 1, Refs: unique, Fingerprint: fingerprint, PausedAt: pausedAt}})
	return string(raw), err
}

// A cache belongs to one admitted turn. Revisions, files and live privacy
// policy are checked at every boundary; no authorization survives the turn.
type topicChatHistoryCache struct {
	mu                  sync.Mutex
	stamp               topicChatHistoryStamp
	valid               bool
	proofs              map[string]bool
	frozen              map[string]bool
	staleRuns           map[string]bool
	collections, checks int
}
type topicChatHistoryStamp struct {
	main, activity storage.ReadRevision
	files          todayChatAdmissionFileRevision
	policy         [sha256.Size]byte
	privacy        uint64
}

func topicChatRowProof(row storage.StreamItemRow) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(row.AutomationContext), &fields) != nil {
		return ""
	}
	return string(fields["topicContext"])
}
func (a *App) topicChatHistoryStamp(ctx context.Context) (topicChatHistoryStamp, error) {
	var stamp topicChatHistoryStamp
	var err error
	stamp.main, err = a.store.ReadRevision(ctx)
	if err != nil {
		return stamp, err
	}
	if a.activity != nil && a.activity.store != nil {
		stamp.activity, err = storage.DatabaseRevision(ctx, a.activity.store.db)
		if err != nil {
			return stamp, err
		}
		stamp.privacy = a.activity.store.readPrivacyRevision.Load()
		if stamp.privacy%2 != 0 {
			return stamp, errChatSourceAccessChanged
		}
	}
	stamp.files, err = a.topicChatHistoryFiles(ctx)
	if err != nil {
		return stamp, err
	}
	raw, err := json.Marshal([]any{a.IsClosedEyesModeEnabled(), pii.GetUserSensitiveApps(), GetUserVoiceInputApps()})
	stamp.policy = sha256.Sum256(raw)
	return stamp, err
}
func (a *App) topicHistoryContext(ctx context.Context, tag string) context.Context {
	if topicTurnFromContext(ctx) != nil {
		return ctx
	}
	a.mu.Lock()
	binding := a.topicHistoryTurns[tag]
	a.mu.Unlock()
	if binding == nil {
		binding = &topicTurnBinding{}
	}
	return context.WithValue(ctx, topicTurnContextKey{}, binding)
}
func (a *App) topicChatProofResults(ctx context.Context, proofs map[string]bool) map[string]bool {
	binding := topicTurnFromContext(ctx)
	cache := &binding.history
	cache.mu.Lock()
	defer cache.mu.Unlock()
	denied := map[string]bool{}
	paused := a.IsClosedEyesModeEnabled()
	pausedAt := binding.pausedAt.Load()
	if paused && pausedAt == 0 {
		return denied
	}
	stamp, err := a.topicChatHistoryStamp(ctx)
	if err != nil {
		return denied
	}
	if !cache.valid || cache.stamp != stamp {
		cache.valid = false
		cache.proofs = map[string]bool{}
	}
	missing := false
	for raw := range proofs {
		if _, ok := cache.proofs[raw]; !ok {
			missing = true
		}
	}
	if !missing {
		return cloneTopicChatProofResults(cache.proofs)
	}
	for _, lock := range []struct {
		try     func() bool
		release func()
	}{
		{a.closedEyes.mu.TryLock, a.closedEyes.mu.Unlock},
		{memoryMutationMu.TryLock, memoryMutationMu.Unlock},
		{memoryFileLocks.publication.TryLock, memoryFileLocks.publication.Unlock},
		{pii.TryLockSensitiveAppsForPublication, pii.UnlockSensitiveAppsForPublication},
	} {
		if lockTodayChatPublication(ctx, lock.try) != nil {
			return denied
		}
		defer lock.release()
	}
	ref := TodayChatContextRef{SourceKind: "topic", SourceID: "history"}
	release, err := a.lockTodayChatFileServices(ctx, ref)
	if err != nil {
		return denied
	}
	defer release()
	before, err := a.topicChatHistoryStamp(ctx)
	if err != nil {
		return denied
	}
	if !cache.valid || cache.stamp != before {
		cache.proofs = map[string]bool{}
	}
	// One privacy-filtered collection serves every proof and every reference in
	// the actual model window. Shared references are resolved only once.
	if paused {
		since, err := a.observationPauseCoverageStart()
		if err != nil || since != pausedAt || a.closedEyes.policyPending.Load() {
			return denied
		}
		ctx = context.WithValue(ctx, pausedBriefReadKey{}, true)
	}
	// The stamp above read the folder from disk; the sources must read it too,
	// not from a snapshot checked a moment ago (an outside edit would go unseen).
	a.recheckMemoryFolder()
	sources := a.collectTodayChatSources(ctx, time.Now())
	cache.collections++
	resolved := map[string]topicChatFingerprintInput{}
	for raw := range proofs {
		if _, ok := cache.proofs[raw]; ok {
			continue
		}
		cache.checks++
		var proof topicChatProof
		allowed := json.Unmarshal([]byte(raw), &proof) == nil && proof.Version == 1 && len(proof.Fingerprint) == 64 && (!paused || proof.PausedAt == pausedAt)
		if allowed {
			current, err := a.chatTopicFingerprintFromSources(ctx, proof.Refs, sources, resolved)
			allowed = err == nil && current == proof.Fingerprint
		}
		cache.proofs[raw] = allowed
	}
	after, err := a.topicChatHistoryStamp(ctx)
	if err != nil || before != after {
		cache.valid = false
		return denied
	}
	cache.stamp, cache.valid = after, true
	logTopicHistoryProofs(cache.proofs)
	return cloneTopicChatProofResults(cache.proofs)
}

// Only topic exchanges are windowed. Ordinary rows (including compaction
// and older tools) retain feat's complete native-history contract.
func (a *App) filterTopicChatHistory(ctx context.Context, rows []storage.StreamItemRow) ([]storage.StreamItemRow, bool) {
	cutoff, texts := len(rows), 0
	for i := len(rows) - 1; i >= 0 && len(rows)-i <= 256; i-- {
		cutoff = i
		if (rows[i].Kind == "user" || rows[i].Kind == "assistant") && rows[i].Content != "" {
			texts++
		}
		if texts == 30 {
			break
		}
	}
	recentRuns := map[string]bool{}
	rawProofs := make([]string, len(rows))
	anyTopic := false
	for i, row := range rows {
		rawProofs[i] = topicChatRowProof(row)
		if rawProofs[i] != "" {
			anyTopic = true
			if i >= cutoff && row.RunID != "" {
				recentRuns[row.RunID] = true
			}
		}
	}
	if !anyTopic {
		return rows, false
	}
	proofs := map[string]bool{}
	eligible := func(i int) bool { return i >= cutoff || (rows[i].RunID != "" && recentRuns[rows[i].RunID]) }
	for i, raw := range rawProofs {
		if raw != "" && eligible(i) {
			proofs[raw] = true
		}
	}
	allowed := map[string]bool{}
	if len(proofs) > 0 {
		allowed = a.topicChatProofResults(ctx, proofs)
	}
	deniedIDs, deniedRuns := map[string]bool{}, map[string]bool{}
	staleRuns := map[string]bool{}
	for i, row := range rows {
		raw := rawProofs[i]
		if raw != "" && (!eligible(i) || !allowed[raw]) {
			deniedIDs[row.ID] = true
			if row.RunID != "" {
				deniedRuns[row.RunID] = true
				if eligible(i) && !allowed[raw] {
					staleRuns[row.RunID] = true
				}
			}
		}
	}
	if binding := topicTurnFromContext(ctx); binding != nil && len(staleRuns) > 0 {
		binding.history.mu.Lock()
		if binding.history.staleRuns == nil {
			binding.history.staleRuns = map[string]bool{}
		}
		for run := range staleRuns {
			binding.history.staleRuns[run] = true
		}
		binding.history.mu.Unlock()
	}
	out := make([]storage.StreamItemRow, 0, len(rows))
	bound := false
	for i, row := range rows {
		if deniedIDs[row.ID] || deniedRuns[row.RunID] {
			continue
		}
		var link struct {
			UserItemID string `json:"userItemId"`
		}
		if row.Kind == "context_status" && json.Unmarshal([]byte(row.ToolInput), &link) == nil && deniedIDs[link.UserItemID] {
			continue
		}
		if rawProofs[i] != "" {
			bound = true
		}
		out = append(out, row)
	}
	return out, bound
}

// A receipt and the replacement executor identity commit together. Read-only
// history projection never rotates a session; sends apply detected withdrawal.
func (a *App) topicChatSession(ctx context.Context, tag, sessionID string) (string, error) {
	if a == nil || a.store == nil {
		return sessionID, nil
	}
	runs := []string{}
	if binding := topicTurnFromContext(ctx); binding != nil {
		binding.history.mu.Lock()
		for run := range binding.history.staleRuns {
			runs = append(runs, run)
		}
		binding.history.mu.Unlock()
	}
	return a.store.ResolveTopicChatSession(ctx, tag, sessionID, runs)
}

func freezeTopicChatHistory(ctx context.Context, rows []storage.StreamItemRow) {
	if binding := topicTurnFromContext(ctx); binding != nil {
		binding.history.mu.Lock()
		defer binding.history.mu.Unlock()
		if binding.history.frozen == nil {
			binding.history.frozen = map[string]bool{}
		}
		for _, row := range rows {
			if raw := topicChatRowProof(row); raw != "" {
				binding.history.frozen[raw] = true
			}
		}
	}
}

// A newly admitted topic prompt and history already handed to a runner must
// remain proven. An old exchange excluded before composition is harmless.
func (a *App) validateTurnTopicContext(ctx context.Context) error {
	binding := topicTurnFromContext(ctx)
	if binding == nil {
		return nil
	}
	proofs := map[string]bool{}
	binding.history.mu.Lock()
	for raw := range binding.history.frozen {
		proofs[raw] = true
	}
	binding.history.mu.Unlock()
	if binding.userItemID != "" {
		row, err := a.store.LoadStreamItem(ctx, binding.userItemID)
		if err != nil || row == nil || row.Kind != "user" || row.AutomationContext != binding.metadata || binding.metadata == "" {
			return errChatSourceAccessChanged
		}
		raw := topicChatRowProof(*row)
		if raw == "" {
			return errChatSourceAccessChanged
		}
		proofs[raw] = true
	}
	if len(proofs) == 0 {
		return nil
	}
	allowed := a.topicChatProofResults(ctx, proofs)
	for raw := range proofs {
		if !allowed[raw] {
			return errChatSourceAccessChanged
		}
	}
	return ctx.Err()
}

// Freeze all readable member identities that can lend keys or a displayed
// thread title. Later additions do not invalidate an earlier turn's history.
func (a *App) chatTopicEventRefs(ctx context.Context, events []MainChatTopicItem) ([]TodayChatContextRef, error) {
	refs := []TodayChatContextRef{}
	if a.activity == nil || a.activity.store == nil {
		return refs, nil
	}
	seen := map[string]bool{}
	for _, event := range events {
		rows, err := a.activity.store.reader().QueryContext(ctx, `SELECT activity_id FROM activity_thread_members
            WHERE thread_id=(SELECT thread_id FROM activity_thread_members WHERE activity_id=?) ORDER BY activity_id LIMIT ?`, strings.TrimPrefix(event.ID, "event:"), topicThreadSourceLimit)
		if err != nil {
			return nil, err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		var basis string
		if err := a.activity.store.reader().QueryRowContext(ctx, `SELECT t.title_basis FROM activity_threads t JOIN activity_thread_members m ON m.thread_id=t.id WHERE m.activity_id=?`, strings.TrimPrefix(event.ID, "event:")).Scan(&basis); err != nil {
			return nil, err
		}
		basisIDs := composeParseStrings(basis)
		if len(basisIDs) <= topicThreadSourceLimit {
			ids = append(ids, basisIDs...)
		}
		for _, id := range ids {
			if seen[id] {
				continue
			}
			if _, _, allowed := a.chatTopicActivity(ctx, id); allowed {
				seen[id] = true
				refs = append(refs, TodayChatContextRef{SourceKind: "activity", SourceID: id})
			}
		}
	}
	return refs, ctx.Err()
}

func cloneTopicChatProofResults(proofs map[string]bool) map[string]bool {
	copy := make(map[string]bool, len(proofs))
	for raw, allowed := range proofs {
		copy[raw] = allowed
	}
	return copy
}

// The cheap cache boundary hashes source bytes without re-collecting sources.
// Include nested controls and recording metadata consulted by the Today gates.
func (a *App) topicChatHistoryFiles(ctx context.Context) (todayChatAdmissionFileRevision, error) {
	var result todayChatAdmissionFileRevision
	admission, err := a.readTodayChatAdmissionFiles(ctx, TodayChatContextRef{SourceKind: "topic", SourceID: "history"})
	if err != nil {
		return result, err
	}
	digest := sha256.New()
	todayChatAdmissionHash(digest, "admission", admission[:])
	memory, err := memoryDir()
	if err != nil {
		return result, err
	}
	knowledge, err := knowledgeDir()
	if err != nil {
		return result, err
	}
	dirs := []string{memory, knowledge}
	if a.activity != nil && a.activity.store != nil {
		dirs = append(dirs, a.activity.store.AudioDir())
	}
	for _, dir := range dirs {
		if err := chatContextDirectoryStamp(ctx, digest, dir); err != nil {
			return result, err
		}
	}
	// Transcript headers can live beside the recording sidecars. Their gate
	// consults .txt files as well as JSON; binaries never participate.
	if len(dirs) > 2 {
		err = filepath.WalkDir(dirs[2], func(path string, entry os.DirEntry, err error) error {
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if !entry.IsDir() && filepath.Ext(path) == ".txt" {
				return todayChatAdmissionFile(ctx, digest, path, path)
			}
			return ctx.Err()
		})
		if err != nil {
			return result, err
		}
	}
	copy(result[:], digest.Sum(nil))
	return result, ctx.Err()
}

func turnHasFrozenTopicHistory(ctx context.Context) bool {
	binding := topicTurnFromContext(ctx)
	if binding == nil {
		return false
	}
	binding.history.mu.Lock()
	defer binding.history.mu.Unlock()
	return len(binding.history.frozen) > 0
}

// logTopicHistoryProofs writes one line each time the history's proofs are
// checked again. It carries counts only: no note text, title or topic name.
func logTopicHistoryProofs(proofs map[string]bool) {
	allowed := 0
	for _, ok := range proofs {
		if ok {
			allowed++
		}
	}
	log.Printf("[topic-history] checked proofs=%d kept=%d withheld=%d", len(proofs), allowed, len(proofs)-allowed)
}
