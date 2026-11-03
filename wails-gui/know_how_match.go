package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const knowHowMatchTTL = 2 * time.Minute

// GetKnowHowMatch finds an earlier fix using only the local knowledge FTS index.
func (a *App) GetKnowHowMatch() (perfValue *KnowHowMatch, perfErr error) {
	defer logPageBinding("GetKnowHowMatch", time.Now(), &perfValue)
	if a == nil || a.IsClosedEyesModeEnabled() || a.activity == nil || a.activity.store == nil || a.memoryStore == nil {
		return nil, nil
	}
	// Serialize computation and cache publication. No worker, polling, model,
	// index sync or embedding provider is started by this read.
	a.knowHow.matchMu.Lock()
	defer a.knowHow.matchMu.Unlock()
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	now := time.Now()
	runs, err := a.knowHowRecentRuns(ctx, now)
	if err != nil {
		return nil, err
	}
	readable := make([]ActivityTimelineRunRow, 0)
	currentSessions := map[string]bool{}
	for _, run := range runs {
		if !a.knowHowRunReadable(run) {
			continue
		}
		readable = append(readable, run)
		for _, id := range run.SessionIDs {
			currentSessions[id] = true
		}
	}
	// The shared owner reader creates its directory. A match read must not
	// create one when Knowledge has never been used on this computer.
	root, err := flavorDataDir()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(root, "knowledge")); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	entries, err := a.GetKnowledgeEntries()
	if err != nil {
		return nil, err
	}
	byID := make(map[string]KnowledgeEntry)
	fingerprint := sha256.New()
	for _, run := range readable {
		fmt.Fprintf(fingerprint, "%v", run)
	}
	for _, entry := range entries {
		if !a.memorySourceAllowsRead(entry.Content) || knowHowSharesSession(entry.SessionIDs, currentSessions) {
			continue
		}
		byID[entry.ID] = entry
		fmt.Fprintf(fingerprint, "%s:%x", entry.ID, sha256.Sum256([]byte(entry.Content)))
	}
	cacheKey := hex.EncodeToString(fingerprint.Sum(nil))
	if cacheKey == a.knowHow.cacheKey && now.Sub(a.knowHow.cacheAt) < knowHowMatchTTL {
		return a.publishKnowHowMatch(ctx, a.knowHow.cached, readable, byID, a.knowHow.cacheRunID)
	}
	var best *KnowHowMatch
	bestRunID := ""
	bestScore := 0.0
	for _, run := range readable {
		errors, paths := knowHowErrorTerms(run, now)
		if len(errors) == 0 {
			continue
		}
		signature := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(errors, "\n"))))
		results, err := a.knowHowKeywordSearch(ctx, strings.Join(append(append([]string(nil), errors...), paths...), " "))
		if err != nil {
			return nil, err
		}
		for _, id := range results {
			entry, ok := byID[id]
			if !ok {
				continue
			}
			card := knowHowCard(entry)
			switch card.Kind {
			case "fix", "gotcha", "how_to":
			default:
				continue
			}
			// FTS may lag a file edit. Score the current readable file rather
			// than its stale chunk; generic error words never qualify alone.
			scoringCard := card
			scoringCard.Points = []string{entry.Content}
			if doc, ok := parseOKFDocument(entry.Content); ok {
				scoringCard.Points = []string{doc.Body}
			}
			score := knowHowErrorScore(scoringCard, errors, paths)
			if score < 0.75 || score <= bestScore {
				continue
			}
			match := &KnowHowMatch{EntryID: id, Kind: card.Kind, Title: card.Title, CurrentApp: knowHowDisplayApp(run), PreviousAt: card.SavedAt, Signature: signature}
			dismissed, err := a.knowHowMatchDismissed(ctx, match)
			if err != nil {
				return nil, err
			}
			if dismissed {
				continue
			}
			best, bestScore = match, score
			bestRunID = run.ID
		}
	}
	a.knowHow.cacheKey, a.knowHow.cacheAt, a.knowHow.cached = cacheKey, now, best
	a.knowHow.cacheRunID = bestRunID
	return a.publishKnowHowMatch(ctx, best, readable, byID, bestRunID)
}

// Timeline's ordinary range helper uses containment. A long-running moment
// can contain a fresh error, so this read deliberately uses overlap instead.
func (a *App) knowHowRecentRuns(ctx context.Context, now time.Time) ([]ActivityTimelineRunRow, error) {
	rows, err := a.activity.store.reader().QueryContext(ctx,
		`SELECT `+timelineRunColumns+` FROM activity_timeline_groups
         WHERE end_ms >= ? AND start_ms <= ? ORDER BY end_ms DESC LIMIT 2000`,
		now.Add(-30*time.Minute).UnixMilli(), now.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTimelineRunRows(rows)
}

func (a *App) knowHowRunReadable(run ActivityTimelineRunRow) bool {
	return !run.EvidenceExcluded && !activityEvidenceAppExcluded(run.AppKey, run.AppName) && a.activityTimelineRunAllowsModelRead(a.activity.store, run)
}

func knowHowDisplayApp(run ActivityTimelineRunRow) string {
	if activityEvidenceAppExcluded(run.AppKey, run.AppName) || workPatternIsAmbientSystemApp(run.AppKey, run.AppName) {
		return ""
	}
	return run.AppName
}

func knowHowSharesSession(ids []string, current map[string]bool) bool {
	for _, id := range ids {
		if current[id] {
			return true
		}
	}
	return false
}

func knowHowErrorTerms(run ActivityTimelineRunRow, now time.Time) ([]string, []string) {
	errors, paths := map[string]bool{}, map[string]bool{}
	for _, token := range run.EvidenceTokens {
		if token.LastSeenMs < now.Add(-30*time.Minute).UnixMilli() || token.LastSeenMs > now.UnixMilli() {
			continue
		}
		value := strings.Join(strings.Fields(strings.ToLower(token.Value)), " ")
		switch token.Kind {
		case "error":
			switch value {
			case "", "error", "fatal", "panic", "exception":
				continue
			}
			errors[value] = true
		case "path":
			paths[value] = true
		}
	}
	sorted := func(values map[string]bool) []string {
		out := make([]string, 0, len(values))
		for value := range values {
			out = append(out, value)
		}
		sort.Strings(out)
		return out
	}
	return sorted(errors), sorted(paths)
}

func knowHowErrorScore(card KnowHowCard, errors, paths []string) float64 {
	text := strings.ToLower(card.Title + "\n" + card.Takeaway + "\n" + strings.Join(card.Points, "\n"))
	words := map[string]bool{}
	for _, word := range ftsTokens(text) {
		words[word] = true
	}
	hits := 0
	for _, value := range errors {
		all := len(ftsTokens(value)) > 0
		for _, word := range ftsTokens(value) {
			all = all && words[word]
		}
		if all {
			hits++
		}
	}
	if len(errors) == 0 || hits == 0 {
		return 0
	}
	score := float64(hits) / float64(len(errors))
	if len(paths) == 0 {
		return score
	}
	pathHits := 0
	for _, path := range paths {
		if strings.Contains(text, path) {
			pathHits++
		}
	}
	return 0.8*score + 0.2*float64(pathHits)/float64(len(paths))
}

func (a *App) knowHowKeywordSearch(ctx context.Context, query string) ([]string, error) {
	store := a.memoryStore
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed || store.db == nil {
		return nil, nil
	}
	fts := buildFTSQueryWithOp(query, "OR")
	if fts == "" {
		return nil, nil
	}
	rows, err := store.db.QueryContext(ctx, `SELECT path FROM memory_chunks_fts WHERE memory_chunks_fts MATCH ? AND source = 'knowledge' ORDER BY bm25(memory_chunks_fts) LIMIT 100`, fts)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0)
	seen := map[string]bool{}
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		// Index paths are resolved only to the enumerated knowledge IDs. No
		// index-provided path is opened, and no other source enters ranking.
		id := strings.TrimSuffix(filepath.Base(path), ".md")
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	return ids, rows.Err()
}

func (a *App) publishKnowHowMatch(ctx context.Context, match *KnowHowMatch, runs []ActivityTimelineRunRow, entries map[string]KnowledgeEntry, sourceRunID string) (*KnowHowMatch, error) {
	if match == nil || a.IsClosedEyesModeEnabled() {
		return nil, nil
	}
	entry, ok := entries[match.EntryID]
	if !ok || !a.memorySourceAllowsRead(entry.Content) {
		return nil, nil
	}
	fresh, err := a.GetKnowledgeEntry(match.EntryID)
	if err != nil {
		return nil, nil
	}
	if fresh.Content != entry.Content || !a.memorySourceAllowsRead(fresh.Content) {
		return nil, nil
	}
	current := map[string]bool{}
	sourceAllowed := false
	var origin ActivityTimelineRunRow
	now := time.Now()
	for _, run := range runs {
		if !a.knowHowRunReadable(run) {
			continue
		}
		for _, id := range run.SessionIDs {
			current[id] = true
		}
		errors, _ := knowHowErrorTerms(run, now)
		signature := fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(errors, "\n"))))
		if run.ID == sourceRunID && len(errors) > 0 && signature == match.Signature {
			sourceAllowed = true
			origin = run
		}
	}
	if !sourceAllowed || knowHowSharesSession(fresh.SessionIDs, current) {
		return nil, nil
	}
	dismissed, err := a.knowHowMatchDismissed(ctx, match)
	if dismissed || err != nil {
		return nil, err
	}
	// The offered app belongs to the exact origin, even if another readable
	// moment has the same error. Recheck after the dismissal-store read.
	if a.IsClosedEyesModeEnabled() || !a.memorySourceAllowsRead(fresh.Content) || !a.knowHowRunReadable(origin) {
		return nil, nil
	}
	copy := *match
	copy.CurrentApp = knowHowDisplayApp(origin)
	return &copy, nil
}

func knowHowDismissalKey(id, signature string) string {
	return fmt.Sprintf("know_how_match_dismissed:%x", sha256.Sum256([]byte(id+"\x00"+signature)))
}
func (a *App) knowHowMatchDismissed(ctx context.Context, match *KnowHowMatch) (bool, error) {
	if a.store == nil {
		return true, nil
	}
	value, err := a.store.GetUIState(ctx, knowHowDismissalKey(match.EntryID, match.Signature))
	return value == "1", err
}

// DismissKnowHowMatch permanently suppresses this entry/error pair on this computer.
func (a *App) DismissKnowHowMatch(entryID, signature string) error {
	if !validMemoryForgetRecordID(entryID) {
		return fmt.Errorf("invalid entry id")
	}
	decoded, err := hex.DecodeString(signature)
	if err != nil || len(decoded) != sha256.Size {
		return fmt.Errorf("invalid signature")
	}
	signature = hex.EncodeToString(decoded)
	if a.store == nil {
		return fmt.Errorf("state store unavailable")
	}
	a.knowHow.matchMu.Lock()
	defer a.knowHow.matchMu.Unlock()
	if err := a.store.SetUIState(context.Background(), knowHowDismissalKey(entryID, signature), "1"); err != nil {
		return err
	}
	a.knowHow.cacheKey = ""
	a.knowHow.cached = nil
	return nil
}
