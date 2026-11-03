package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"wails-gui/internal/pii"
)

// Relevance-scoped dedup recall for activity triage.
//
// The triage prompt embeds a "deduplication check" block listing existing
// memory/knowledge entries so the model can avoid re-creating duplicates. The
// original implementation dumped the first 50 entries found on disk (directory
// order, no relevance), which made the prompt grow without bound as a user
// accumulated entries — the dominant, ever-increasing cost of background
// triage. These helpers instead pull only the top-k entries most relevant to
// the current activity summary, so prompt size stays bounded regardless of how
// much history a user has built up.
//
// Both helpers fall back to the full scan / recency ordering when their
// relevance signal is unavailable, so the worst case degrades to the prior
// behaviour rather than silently dropping dedup context (which would risk
// duplicate products).

const (
	// dedupRecallMaxEntries caps how many existing entries each dedup section
	// lists. ~20 keeps the section a few KB while still giving the model a
	// representative slice of the most-related prior work.
	dedupRecallMaxEntries = 20

	// Operational guard for relevance searches. Selection is controlled by a
	// character budget, not by a top-N product rule.
	dedupRecallCandidateScanLimit = 200

	// There are two distinct caps below; they govern different paths, neither
	// replaces the other:
	//   - automationDedupRankCap (15) — the automation list once it has been IDF
	//     relevance-ranked (the normal path: non-empty query with >=1 match).
	//   - dedupListCap (40) — the handoff list (always full, unranked) AND the
	//     automation list's UNRANKED FALLBACK (empty query, or nothing matched,
	//     where rankAutomationDedupJobs returns the original order untrimmed).

	// dedupListCap bounds the handoff dedup list (pending handoffs are shown in
	// full, unranked — the set is transient and tiny, and handoff has no
	// fingerprint backstop, so trimming it would risk a missed duplicate for no
	// real token saving). It also serves as the absolute guard for the
	// automation list's unranked fallback path (see above). One-line-per-item,
	// so a generous cap just protects against a pathological count blowing up
	// the prompt.
	dedupListCap = 40

	// automationDedupRankCap bounds how many existing automations the dedup
	// section lists after IDF relevance ranking (see rankAutomationDedupJobs).
	// It does NOT supersede dedupListCap (40) — that still guards the unranked
	// fallback path; this 15 applies only on the ranked path. Unlike a hard
	// top-k filter, ranking keeps every automation while the count stays under
	// this cap (so a short list never drops a likely duplicate) and only trims
	// the least-relevant tail once a user has accumulated many — which is what
	// bounds per-triage prompt cost as automations grow. Set comfortably above a
	// typical automation count so trimming (and its small missed-duplicate risk)
	// only kicks in at real scale.
	automationDedupRankCap = 15
)

func appendDedupWithinBudget(b *strings.Builder, entry string, maxChars int) bool {
	if entry == "" {
		return true
	}
	if maxChars <= 0 || b.Len()+len(entry) <= maxChars {
		b.WriteString(entry)
		return true
	}
	remaining := maxChars - b.Len()
	if remaining > 128 {
		b.WriteString(truncateAtRuneBoundary(entry, remaining))
		b.WriteString("\n... (context budget reached)\n")
	}
	return false
}

func dedupFromResultsWithReadFilter(
	results []MemorySearchResult,
	source MemorySource,
	maxChars int,
	resolve func(path string) string,
	allowRead func(id, content string) bool,
) string {
	return dedupFromResultsWithReadCapture(results, source, maxChars, resolve, allowRead, nil)
}

func dedupFromResultsWithReadCapture(
	results []MemorySearchResult,
	source MemorySource,
	maxChars int,
	resolve func(path string) string,
	allowRead func(id, content string) bool,
	onSelected func(id, content string),
) string {
	if allowRead == nil {
		return ""
	}

	var b strings.Builder
	seen := make(map[string]struct{})
	for _, r := range results {
		if MemorySource(r.Source) != source {
			continue
		}
		id := strings.TrimSuffix(filepath.Base(r.Path), ".md")
		if id == "" {
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		data, err := os.ReadFile(resolve(r.Path))
		if err != nil {
			continue
		}
		if !allowRead(id, string(data)) {
			continue
		}
		seen[id] = struct{}{}
		if !appendDedupSourceWithinBudget(&b, id, string(data), maxChars, onSelected) {
			break
		}
	}
	return b.String()
}

func loadDedupDirectorySummaryWithReadFilter(dir string, maxChars int, allowRead func(id, content string) bool) string {
	return loadDedupDirectorySummaryWithReadCapture(dir, maxChars, allowRead, nil)
}

func loadDedupDirectorySummaryWithReadCapture(dir string, maxChars int, allowRead func(id, content string) bool, onSelected func(id, content string)) string {
	if allowRead == nil {
		return ""
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	sort.SliceStable(entries, func(i, j int) bool {
		left, leftErr := entries[i].Info()
		right, rightErr := entries[j].Info()
		if leftErr != nil || rightErr != nil {
			return entries[i].Name() < entries[j].Name()
		}
		return left.ModTime().After(right.ModTime())
	})
	var b strings.Builder
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".md")
		if !allowRead(id, string(data)) {
			continue
		}
		if !appendDedupSourceWithinBudget(&b, id, string(data), maxChars, onSelected) {
			break
		}
	}
	return b.String()
}

func appendDedupSourceWithinBudget(b *strings.Builder, id, content string, maxChars int, onSelected func(id, content string)) bool {
	before := b.Len()
	more := appendDedupWithinBudget(b, formatDedupEntry(id, content), maxChars)
	if b.Len() > before && onSelected != nil {
		onSelected(id, content)
	}
	return more
}

// formatDedupEntry renders one memory/knowledge entry as the compact dedup
// line used in the triage prompt: "- [id=<id>] <title>" followed by up to three
// leading bullet points (for semantic comparison by the model). The [id=...]
// tag is what the model echoes back as updateId, so it must be the filename
// stem. Returns "" only for empty content.
func formatDedupEntry(id, content string) string {
	// Redact the complete source before selecting bullets or applying the
	// prompt budget, which can otherwise cut through a recognizable secret.
	content, _ = pii.RedactText(content)
	title := ""
	var points []string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") && title == "" {
			title = strings.TrimPrefix(line, "# ")
		}
		if strings.HasPrefix(line, "- ") && !strings.HasPrefix(line, "- **") {
			points = append(points, line)
		}
	}
	if title == "" {
		title = id
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("- [id=%s] %s", id, title))
	limit := min(3, len(points))
	for i := range limit {
		b.WriteString(fmt.Sprintf("\n  %s", points[i]))
	}
	if len(points) > limit {
		b.WriteString(fmt.Sprintf("\n  ... +%d more points", len(points)-limit))
	}
	b.WriteString("\n")
	return b.String()
}

func dedupQueryTokens(query string) map[string]struct{} {
	tokens := map[string]struct{}{}
	for _, t := range ftsTokens(strings.ToLower(query)) {
		if utf8.RuneCountInString(t) >= 3 {
			tokens[t] = struct{}{}
		}
	}
	return tokens
}

func dedupTokenOverlapScore(queryTokens map[string]struct{}, candidate string) int {
	if len(queryTokens) == 0 {
		return 0
	}

	docTokens := map[string]struct{}{}
	for _, tok := range ftsTokens(strings.ToLower(candidate)) {
		docTokens[tok] = struct{}{}
	}

	score := 0
	for tok := range queryTokens {
		if _, ok := docTokens[tok]; ok {
			score++
		}
	}
	return score
}

func (a *App) loadRelevantKnowledgeSummaryFallback(query string, maxEntries int) string {
	return a.loadRelevantKnowledgeSummaryFallbackWithCapture(query, 0, maxEntries, nil)
}

func (a *App) loadRelevantKnowledgeSummaryFallbackWithCapture(query string, maxChars, maxEntries int, onSelected func(id, content string)) string {
	if strings.TrimSpace(query) == "" {
		return a.loadModelDedupSummaryWithCapture(nil, "", 100_000, 0, MemorySourceKnowledge, onSelected)
	}

	allowRead, err := a.memoryReadFilter()
	if err != nil {
		return ""
	}

	dir, err := knowledgeDir()
	if err != nil {
		return ""
	}
	if maxEntries <= 0 {
		maxEntries = dedupRecallMaxEntries
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}

	// Lower-cased keyword set from the activity summary. Tokens shorter than 3
	// chars are dropped as noise; this keeps CJK runs (single token) and
	// meaningful English words while skipping "a"/"of"/etc.
	queryTokens := dedupQueryTokens(query)

	type kEntry struct {
		id      string
		content string
		score   int
		mtime   int64
	}
	all := make([]kEntry, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".md")
		content := string(data)
		if !allowRead(id, content) {
			continue
		}

		score := 0
		if len(queryTokens) > 0 {
			score = dedupTokenOverlapScore(queryTokens, id+"\n"+content)
		}

		var mtime int64
		if info, infoErr := e.Info(); infoErr == nil {
			mtime = info.ModTime().UnixMilli()
		}
		all = append(all, kEntry{id: id, content: content, score: score, mtime: mtime})
	}
	if len(all) == 0 {
		return ""
	}

	sort.SliceStable(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		return all[i].mtime > all[j].mtime
	})

	var b strings.Builder
	for i, e := range all {
		if i >= maxEntries {
			break
		}
		if !appendDedupSourceWithinBudget(&b, e.id, e.content, maxChars, onSelected) {
			break
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return b.String()
}
