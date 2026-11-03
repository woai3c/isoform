package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"wails-gui/internal/pii"
	"wails-gui/storage"
)

const searchSourceTimeout = 400 * time.Millisecond
const searchCandidateLimit = 50

type SearchTarget struct {
	Kind       string `json:"kind"`
	ID         string `json:"id,omitempty"`
	SessionID  string `json:"sessionId,omitempty"`
	ItemID     string `json:"itemId,omitempty"`
	ActivityID string `json:"activityId,omitempty"`
	ThreadID   string `json:"threadId,omitempty"`
	At         int64  `json:"at,omitempty"`
	StartMs    int64  `json:"startMs,omitempty"`
	EndMs      int64  `json:"endMs,omitempty"`
}

type SearchResult struct {
	ID         string       `json:"id"`
	Place      string       `json:"place"`
	Title      string       `json:"title"`
	Snippet    string       `json:"snippet"`
	At         int64        `json:"at"`
	TopicID    string       `json:"topicId,omitempty"`
	TopicName  string       `json:"topicName,omitempty"`
	Target     SearchTarget `json:"target"`
	observed   bool
	followed   bool
	sourcePath string
	sourceHash [sha256.Size]byte
}

type SearchGroup struct {
	Place   string         `json:"place"`
	Results []SearchResult `json:"results"`
}

type SearchResults struct {
	Query        string        `json:"query"`
	Groups       []SearchGroup `json:"groups"`
	FailedPlaces []string      `json:"failedPlaces"`
}

type searchCandidate struct {
	result           SearchResult
	body             string
	matchRawIdentity bool
}
type searchSource struct {
	place string
	read  func(context.Context, []string) ([]searchCandidate, error)
}

// SearchEverything searches current local page content without retaining queries.
func (a *App) SearchEverything(query string, limit int) (perfValue SearchResults, perfErr error) {
	defer logPageBinding("SearchEverything", time.Now(), &perfValue)
	return a.searchEverything(context.Background(), query, limit, a.searchSources())
}

func (a *App) searchSources() []searchSource {
	return []searchSource{
		{"topic", a.searchTopics}, {"chat", a.searchChats}, {"activity", a.searchActivity},
		{"person", a.searchPeople}, {"goal", a.searchGoals},
		{"know_how", func(ctx context.Context, words []string) ([]searchCandidate, error) {
			return a.searchEntries(ctx, words, false)
		}},
		{"about_you", func(ctx context.Context, words []string) ([]searchCandidate, error) {
			return a.searchEntries(ctx, words, true)
		}},
	}
}

func (a *App) searchEverything(ctx context.Context, query string, limit int, sources []searchSource) (SearchResults, error) {
	query = strings.TrimSpace(query)
	result := SearchResults{Query: query, Groups: []SearchGroup{}, FailedPlaces: []string{}}
	runes := []rune(query)
	if len(runes) > 200 {
		return result, nil
	}
	if len(runes) < 2 && (len(runes) != 1 || !(unicode.Is(unicode.Han, runes[0]) || unicode.Is(unicode.Hiragana, runes[0]) || unicode.Is(unicode.Katakana, runes[0]) || unicode.Is(unicode.Hangul, runes[0]))) {
		return result, nil
	}
	if limit <= 0 {
		limit = 5
	}
	limit = min(limit, 20)
	words := strings.Fields(strings.ToLower(query))
	mainRevision, activityRevision := a.searchRevisions(ctx)
	sensitiveBefore, _ := json.Marshal(pii.GetUserSensitiveApps())
	elapsed := map[string]time.Duration{}
	// Each source owns its context, work and failure. Sequential scheduling
	// keeps SQLite/file load low; a stalled source cannot hold up the next.
	for _, source := range sources {
		started := time.Now()
		sourceCtx, cancel := context.WithTimeout(ctx, searchSourceTimeout)
		type response struct {
			items []SearchResult
			err   error
		}
		done := make(chan response, 1)
		go func() {
			candidates, err := source.read(sourceCtx, words)
			var items []SearchResult
			if err == nil {
				items = rankSearchCandidates(sourceCtx, candidates, words, query, limit)
				err = sourceCtx.Err()
			}
			done <- response{items, err}
		}()
		var batch response
		select {
		case batch = <-done:
		case <-sourceCtx.Done():
			batch.err = sourceCtx.Err()
		}
		if batch.err == nil {
			batch.err = sourceCtx.Err()
		}
		cancel()
		elapsed[source.place] = time.Since(started)
		if batch.err != nil {
			result.FailedPlaces = append(result.FailedPlaces, source.place)
			continue
		}
		result.Groups = append(result.Groups, SearchGroup{Place: source.place, Results: batch.items})
	}
	// File rechecks share each source's original 400 ms work budget. A
	// vanished/changed/forgotten file is omitted; an unavailable source fails
	// only its own group. No recheck can stall unrelated source publication.
	for i := range result.Groups {
		group := &result.Groups[i]
		if group.Place != "about_you" && group.Place != "know_how" {
			continue
		}
		checkCtx, cancel := context.WithTimeout(ctx, searchSourceTimeout-elapsed[group.Place])
		type checked struct {
			items []SearchResult
			err   error
		}
		done := make(chan checked, 1)
		items := group.Results
		go func() {
			kept := []SearchResult{}
			var readErr error
			for _, r := range items {
				if checkCtx.Err() != nil {
					break
				}
				allowed, err := a.searchEntryCurrent(r)
				if err != nil {
					readErr = err
					break
				}
				if allowed {
					kept = append(kept, r)
				}
			}
			if readErr == nil {
				readErr = checkCtx.Err()
			}
			done <- checked{kept, readErr}
		}()
		var check checked
		select {
		case check = <-done:
		case <-checkCtx.Done():
			check.err = checkCtx.Err()
		}
		cancel()
		if check.err != nil {
			group.Results = nil
			result.FailedPlaces = append(result.FailedPlaces, group.Place)
		} else {
			group.Results = check.items
		}
	}
	// Revalidate database-backed visibility at publication. A concurrent
	// hide, Forget, correction or privacy edit drops affected groups rather
	// than returning a stale projection. These are read stamps, not a cache.
	mainAfter, activityAfter := a.searchRevisions(ctx)
	sensitiveAfter, _ := json.Marshal(pii.GetUserSensitiveApps())
	for i := range result.Groups {
		group := &result.Groups[i]
		if group.Results == nil {
			continue
		}
		changed := false
		switch group.Place {
		case "chat":
			changed = !mainRevision.same(mainAfter)
		case "activity":
			changed = !mainRevision.same(mainAfter) || !activityRevision.same(activityAfter) || string(sensitiveBefore) != string(sensitiveAfter)
		case "person":
			changed = !mainRevision.same(mainAfter)
			for _, r := range group.Results {
				if r.observed {
					changed = changed || !activityRevision.same(activityAfter) || string(sensitiveBefore) != string(sensitiveAfter)
				}
			}
		case "goal", "about_you":
			changed = !activityRevision.same(activityAfter)
		}
		if changed {
			result.FailedPlaces = append(result.FailedPlaces, group.Place)
			group.Results = nil
		}
		if a.IsClosedEyesModeEnabled() {
			if group.Place == "activity" {
				group.Results = []SearchResult{}
			}
			if group.Place == "topic" {
				kept := []SearchResult{}
				for _, r := range group.Results {
					if r.followed {
						kept = append(kept, r)
					}
				}
				group.Results = kept
			}
		}
	}
	groups := result.Groups[:0]
	for _, group := range result.Groups {
		if group.Results != nil {
			groups = append(groups, group)
		}
	}
	result.Groups = groups
	return result, nil
}

type searchRevision struct {
	stamp storage.ReadRevision
	err   error
}

func (r searchRevision) same(other searchRevision) bool {
	return r.err == nil && other.err == nil && r.stamp == other.stamp
}

func (a *App) searchRevisions(parent context.Context) (searchRevision, searchRevision) {
	ctx, cancel := context.WithTimeout(parent, searchSourceTimeout)
	defer cancel()
	var main, activity searchRevision
	if a != nil && a.store != nil {
		main.stamp, main.err = a.store.ReadRevision(ctx)
	}
	if a != nil && a.activity != nil && a.activity.store != nil {
		activity.stamp, activity.err = storage.DatabaseRevision(ctx, a.activity.store.db)
	}
	return main, activity
}

func (a *App) searchEntryCurrent(r SearchResult) (bool, error) {
	if r.sourcePath == "" {
		return false, nil
	}
	info, err := os.Lstat(r.sourcePath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return false, nil
	}
	f, err := os.Open(r.sourcePath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	f.Close()
	if err != nil {
		return false, err
	}
	if len(data) > 1<<20 || sha256.Sum256(data) != r.sourceHash {
		return false, nil
	}
	if r.Place == "about_you" {
		forgotten, err := isMemoryForgotten(filepath.Dir(r.sourcePath), r.ID, string(data), time.Now())
		return !forgotten, err
	}
	return true, nil
}

func searchMatches(text string, words []string) bool {
	text = strings.ToLower(text)
	for _, word := range words {
		if !strings.Contains(text, word) {
			return false
		}
	}
	return true
}

func searchTier(title, query string) int {
	title = strings.ToLower(title)
	query = strings.ToLower(query)
	switch true {
	case title == query:
		return 0
	case strings.HasPrefix(title, query):
		return 1
	case strings.Contains(title, query):
		return 2
	default:
		return 3
	}
}

func rankSearchCandidates(ctx context.Context, candidates []searchCandidate, words []string, query string, limit int) []SearchResult {
	out := []SearchResult{}
	for _, c := range candidates[:min(len(candidates), searchCandidateLimit)] {
		if ctx.Err() != nil {
			return nil
		}
		r := c.result
		title := pii.DisplayText(r.Title)
		body := pii.DisplayText(c.body)
		matchText := title + "\n" + body
		// People supports direct address/handle lookup like its page. Its
		// source has already gated captured evidence; display stays redacted.
		if c.matchRawIdentity && r.Place == "person" {
			matchText = r.Title + "\n" + c.body
		}
		// Other places still redact before matching so guessing a secret
		// cannot disclose its presence through a result's identity.
		if !searchMatches(matchText, words) {
			continue
		}
		r.Title = title
		r.Snippet = searchSnippet(body, words)
		if r.Snippet == "" {
			r.Snippet = searchSnippet(r.Title, words)
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ti, tj := searchTier(out[i].Title, query), searchTier(out[j].Title, query)
		if ti != tj {
			return ti < tj
		}
		if out[i].At != out[j].At {
			return out[i].At > out[j].At
		}
		return out[i].ID < out[j].ID
	})
	return out[:min(limit, len(out))]
}

func searchSnippet(text string, words []string) string {
	text, _ = pii.RedactText(text)
	text = strings.TrimSpace(text)
	lower := strings.ToLower(text)
	first := -1
	for _, word := range words {
		at := strings.Index(lower, word)
		if at >= 0 && (first < 0 || at < first) {
			first = at
		}
	}
	start := 0
	if first >= 0 {
		start = max(0, utf8.RuneCountInString(lower[:first])-35)
	}
	runes := []rune(text)
	return string(runes[start:min(len(runes), start+140)])
}

// StreamItemNotFoundError gives callers a stable code for missing/hidden rows.
type StreamItemNotFoundError struct{}

func (*StreamItemNotFoundError) Error() string { return "SEARCH_STREAM_ITEM_NOT_FOUND" }

// FindStreamItemCursor returns the exclusive cursor that includes this item.
func (a *App) FindStreamItemCursor(sessionLocalID, itemID string) (int, error) {
	if a == nil || a.store == nil {
		return 0, &StreamItemNotFoundError{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), searchSourceTimeout)
	defer cancel()
	cursor, err := a.store.FindVisibleStreamItemCursor(ctx, sessionLocalID, itemID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, &StreamItemNotFoundError{}
	}
	return cursor, err
}
