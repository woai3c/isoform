package main

import (
	"encoding/json"
	"math"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Pure helpers for the Memory Dream cycle. Kept dependency-free so unit tests
// don't need to spin up MemoryStore / SQLite / the agent runner. Anything that
// touches App, MemoryStore, or the filesystem lives in memory_dream.go.

// normalizeMemoryContent collapses unicode forms and whitespace so two
// semantically identical memories don't miss exact-dedup over trivial diffs
// (mixed NFC/NFD, smart quotes vs straight quotes, trailing newlines, mixed
// tabs/spaces). The output is the canonical form used for the L1a bucket key.
func normalizeMemoryContent(s string) string {
	s = norm.NFC.String(s)
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := true
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		b.WriteRune(unicode.ToLower(r))
		prevSpace = false
	}
	return strings.TrimSpace(b.String())
}

// cosineSimilarity64 is a slim copy of cosineSimilarity that returns 0 when
// either side is empty. Defined separately so memory_dream_helpers.go has no
// transitive deps on memory_embedding.go for tests, but at runtime callers can
// pick either implementation interchangeably.
func cosineSimilarity64(a, b []float64) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var dot, na, nb float64
	for i := 0; i < n; i++ {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// l2NormalizeCentroid takes a list of embedding vectors and returns their
// element-wise mean, L2-normalized. Used to roll up per-chunk embeddings into
// a single document-level vector for memory-level similarity. Returns an
// empty slice when the input is empty or all-zero.
func l2NormalizeCentroid(vecs [][]float64) []float64 {
	if len(vecs) == 0 {
		return nil
	}
	dims := len(vecs[0])
	centroid := make([]float64, dims)
	for _, v := range vecs {
		if len(v) != dims {
			// Skip ragged rows rather than crashing — chunks for different
			// embedding models can land in the same table during a partial
			// reindex.
			continue
		}
		for i, x := range v {
			centroid[i] += x
		}
	}
	count := float64(len(vecs))
	for i := range centroid {
		centroid[i] /= count
	}
	var norm float64
	for _, x := range centroid {
		norm += x * x
	}
	if norm == 0 {
		return nil
	}
	inv := 1.0 / math.Sqrt(norm)
	for i := range centroid {
		centroid[i] *= inv
	}
	return centroid
}

// DreamMemoryCandidate is the in-memory view of a memory file the Dream
// service operates on. Carries everything the layers need without dragging
// MemoryEntry's full content along through batched LLM calls.
type DreamMemoryCandidate struct {
	ID         string
	Title      string
	Content    string
	Dimension  string
	Source     string
	UpdatedAt  int64
	ModTime    int64
	HasHistory bool
	// Embedding is the doc-level (centroid) vector. nil when the embedder
	// is unavailable or the memory has no indexed chunks — such memories
	// are still eligible for L1 exact-dedup but skip L2/L3.
	Embedding []float64
	// Normalized cache so we don't recompute it inside O(n²) loops or
	// across L1 bucketing.
	Normalized string
}

// dimensionTier returns a stable rank-priority for the memory's dimension.
// Higher = more valuable as a survivor. Unknown dimensions fall to 0 (last).
//
// The ordering reflects what the user is most likely to care about: the
// "deep" user model and "user_insight" represent durable judgments about
// who the user is, while "agentic_memory" tends to be ephemeral process
// notes from the agent itself.
func dimensionTier(dim string) int {
	switch dim {
	case "deep_user_model":
		return 4
	case "user_insight":
		return 4
	case "topic_highlight":
		return 3
	case "custom":
		return 2
	case "agentic_memory":
		return 1
	default:
		return 0
	}
}

// survivorRank scores a memory's worth-keeping. Higher wins. Used to pick the
// survivor of an exact-duplicate group and the representative of a
// near-duplicate cluster.
//
// Ordering (each component STRICTLY dominates the next, by orders of
// magnitude):
//
//  1. `HasHistory` (+1e12) — the user has edited this file at least once.
//     Whatever it is, demoting it would silently throw away their edits.
//  2. Dimension tier (tier * 1e7) — `deep_user_model` and `user_insight`
//     win over `agentic_memory` even when the latter is much newer.
//  3. Recency in DAYS-since-epoch (ts_ms / 86_400_000) — ts in
//     milliseconds is ~1.7e12 today, which would swamp the dimension
//     band if used directly; converting to days drops it to ~2e4 so it
//     functions as a tiebreaker only.
//
// The "each component strictly dominates" invariant is tested in
// TestSurvivorRankPrefersHigherDimension — change the magnitudes
// carelessly and that test will catch it.
func survivorRank(m DreamMemoryCandidate) int64 {
	var score int64
	if m.HasHistory {
		score += 1_000_000_000_000
	}
	score += int64(dimensionTier(m.Dimension)) * 10_000_000
	ts := m.UpdatedAt
	if ts == 0 {
		ts = m.ModTime
	}
	// ts is ms-since-epoch; convert to days so it stays well below the
	// dimension band (max ~20k days vs 1e7 per dimension tier step).
	const msPerDay = int64(24 * 60 * 60 * 1000)
	if ts > 0 {
		score += ts / msPerDay
	}
	return score
}

// pickSurvivor returns the highest-ranked memory in the group. Stable: ties
// resolve toward the original order (first-seen wins) so re-runs over the
// same data produce identical survivor choices.
func pickSurvivor(group []DreamMemoryCandidate) DreamMemoryCandidate {
	if len(group) == 0 {
		return DreamMemoryCandidate{}
	}
	best := group[0]
	bestRank := survivorRank(best)
	for _, m := range group[1:] {
		if r := survivorRank(m); r > bestRank {
			best = m
			bestRank = r
		}
	}
	return best
}

// DreamMergeResponse is the parsed shape returned by the L3 LLM call.
type DreamMergeResponse struct {
	DeleteIDs  []string             `json:"delete"`
	Synthesize []DreamSynthesisItem `json:"synthesize"`
}

// DreamSynthesisItem describes one merged memory the LLM proposes to create.
type DreamSynthesisItem struct {
	Title     string `json:"title"`
	Content   string `json:"content"`
	Dimension string `json:"dimension"`
}

// parseMergeResponse extracts the strict {delete, synthesize} JSON object the
// LLM is asked to emit. Robust to surrounding prose, markdown code fences,
// or stray commentary — pulls the first balanced top-level object and parses
// that. IDs not in `valid` are dropped. Synthesis entries with empty content
// are dropped. Returns an empty (zero-valued) response on any failure so the
// caller treats malformed LLM output as "do nothing", never as "delete
// everything".
func parseMergeResponse(text string, valid map[string]bool) DreamMergeResponse {
	var empty DreamMergeResponse

	// Find the first '{' and the matching '}'. Tracks depth so it handles
	// quoted strings poorly but well enough for our prompt — the LLM
	// occasionally wraps the JSON in ```json fences which strings-as-bytes
	// scanning handles fine.
	start := strings.IndexByte(text, '{')
	if start < 0 {
		return empty
	}
	depth := 0
	end := -1
	inString := false
	escaped := false
	for i := start; i < len(text); i++ {
		c := text[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return empty
	}

	var raw DreamMergeResponse
	if err := json.Unmarshal([]byte(text[start:end+1]), &raw); err != nil {
		return empty
	}

	out := DreamMergeResponse{}
	for _, id := range raw.DeleteIDs {
		if valid[id] {
			out.DeleteIDs = append(out.DeleteIDs, id)
		}
	}
	for _, s := range raw.Synthesize {
		content := strings.TrimSpace(s.Content)
		if content == "" {
			continue
		}
		title := strings.TrimSpace(s.Title)
		if title == "" {
			title = "Merged Memory"
		}
		dim := strings.ToLower(strings.TrimSpace(s.Dimension))
		if _, ok := validMemoryDimensions[dim]; !ok || dim == "custom" {
			dim = ""
		}
		out.Synthesize = append(out.Synthesize, DreamSynthesisItem{
			Title:     title,
			Content:   content,
			Dimension: dim,
		})
	}
	return out
}

// extractFactTokens returns a deduped, lowercased set of "fact tokens" used
// to verify that a synthesized memory still covers the high-information
// content of every memory it claims to subsume. We strip the frontmatter
// block and any markdown framing, then keep words of length ≥ 4 that aren't
// in a small English stopword list — a rough but cheap proxy for "the
// content-bearing words in this memory". The synthesis must echo at least
// `factCoverageMin` of every source memory's fact set, or we reject the
// merge and treat the cluster as untouched.
//
// This is the code-side backstop on the LLM's "zero information loss"
// promise: even if the model hallucinates a confident-looking summary that
// drops half the facts, the verifier will reject it.
func extractFactTokens(content string) map[string]struct{} {
	body := stripMemoryFrontmatter(content)
	tokens := make(map[string]struct{})
	var word strings.Builder
	flush := func() {
		if word.Len() == 0 {
			return
		}
		w := strings.ToLower(word.String())
		word.Reset()
		if len(w) < 4 {
			return
		}
		if _, skip := dreamStopwords[w]; skip {
			return
		}
		tokens[w] = struct{}{}
	}
	for _, r := range body {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			word.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return tokens
}

// stripMemoryFrontmatter removes the `# Title` heading and the leading
// `- **Key**: Value` block at the top of a memory file so the fact-token
// extractor doesn't count "Source", "Updated", "Dimension", and friends
// as substantive content. Anything after the frontmatter block (including
// other headings) is preserved.
func stripMemoryFrontmatter(content string) string {
	content = legacyMemoryWithoutConfirmation(content)
	if doc, ok := parseOKFDocument(content); ok {
		return doc.Body
	}
	lines := strings.Split(content, "\n")
	skippedTitle := false
	metadata := false
	body := -1
	for i, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			if metadata {
				return strings.Join(lines[i+1:], "\n")
			}
			continue
		}
		if !skippedTitle && !metadata && strings.HasPrefix(trimmed, "# ") {
			skippedTitle = true
			continue
		}
		if strings.HasPrefix(trimmed, "- **") {
			metadata = true
			continue
		}
		if metadata && len(raw) > len(strings.TrimLeft(raw, " \t")) && strings.HasPrefix(trimmed, "- ") {
			continue
		}
		body = i
		break
	}
	if body < 0 {
		return ""
	}
	return strings.Join(lines[body:], "\n")
}

// deletionSafetyThreshold is the fraction of each deleted memory's fact
// tokens that some SINGLE survivor (a kept memory, or a synthesized merger)
// must contain for the deletion to be accepted.
//
// 0.85 is the result of two competing pressures:
//
//   - Too low (e.g. the old 0.70): the symmetric N/M-overlap case slips
//     through. Two memories sharing ~80% of content but each carrying a
//     distinct 20%: each memory's tokens are 80% present in the *other*,
//     so a union-of-survivors check at 0.70 happily accepts "delete A,
//     keep B" — and A's 20% unique content quietly vanishes.
//
//   - Too high (e.g. 0.95): legitimate LLM-mediated mergers that
//     paraphrase shared concepts get rejected, because rewriting tokens
//     drops below 95% lexical overlap.
//
// 0.85 leaves ~15% headroom for paraphrasing while still failing the
// 80/20 symmetric case (best single-survivor coverage = 80% < 85%).
const deletionSafetyThreshold = 0.85

// verifyDeletionSafety returns true when every deleted memory is safely
// subsumed by at least ONE survivor (a kept memory or a synthesized
// memory), where "subsumed" means the survivor's fact tokens cover at
// least `threshold` of the deleted memory's fact tokens.
//
// Why per-survivor instead of union? A union of two survivors trivially
// covers any deleted memory whose tokens are split between them, even
// when neither survivor actually contains the deleted's distinctive
// content. The classic failure is the symmetric 80/20 case:
//
//	deleted A:  shared tokens + a_unique
//	survivor B: shared tokens + b_unique
//	survivor C: shared tokens + c_unique
//
// Union(B,C) contains shared tokens but nothing matching a_unique. With
// a union check at 0.70, A's coverage looks like 80% (the shared tokens)
// — passes — and a_unique disappears. With the single-survivor check,
// best coverage of A is max(80%, 80%) = 80% < 0.85 → rejected, which is
// the correct answer.
//
// Empty survivor list always returns false (the cluster must not vanish).
// Empty deleted list returns true (nothing to verify).
func verifyDeletionSafety(deletedContents, survivorContents []string, threshold float64) bool {
	if len(deletedContents) == 0 {
		return true
	}
	if len(survivorContents) == 0 {
		return false
	}
	survivorTokenSets := make([]map[string]struct{}, len(survivorContents))
	for i, s := range survivorContents {
		survivorTokenSets[i] = extractFactTokens(s)
	}
	for _, d := range deletedContents {
		need := extractFactTokens(d)
		if len(need) == 0 {
			// A memory with no fact-bearing tokens (e.g. frontmatter only)
			// is uninteresting to preserve — let the deletion through.
			continue
		}
		bestCoverage := 0.0
		for _, sT := range survivorTokenSets {
			hit := 0
			for tok := range need {
				if _, ok := sT[tok]; ok {
					hit++
				}
			}
			coverage := float64(hit) / float64(len(need))
			if coverage > bestCoverage {
				bestCoverage = coverage
			}
		}
		if bestCoverage < threshold {
			return false
		}
	}
	return true
}

// dreamStopwords is a tiny English stopword list used by extractFactTokens.
// Kept small and unilingual on purpose: memories often mix Chinese and
// English, and Chinese gets handled naturally because we don't tokenize on
// CJK character boundaries — each Chinese rune sequence becomes its own
// "word" via the IsLetter run and falls through the length-4 filter as a
// multi-rune block. Adding Chinese stopwords would over-aggressively drop
// signal.
var dreamStopwords = map[string]struct{}{
	"the": {}, "and": {}, "for": {}, "with": {}, "that": {}, "this": {},
	"from": {}, "into": {}, "have": {}, "been": {}, "will": {}, "would": {},
	"could": {}, "should": {}, "user": {}, "users": {}, "they": {}, "them": {},
	"these": {}, "those": {}, "their": {}, "than": {}, "then": {}, "when": {},
	"what": {}, "where": {}, "while": {}, "about": {},
}

// exactDuplicateGroups buckets memories by their normalized content key. For
// each bucket with ≥2 entries it returns the survivor (highest survivorRank)
// and the list of duplicates to archive. Empty content keys are skipped so a
// memory consisting only of frontmatter doesn't get collapsed into another
// such memory.
func exactDuplicateGroups(candidates []DreamMemoryCandidate) []DreamExactGroup {
	buckets := map[string][]DreamMemoryCandidate{}
	for _, c := range candidates {
		key := c.Normalized
		if key == "" {
			continue
		}
		buckets[key] = append(buckets[key], c)
	}
	var groups []DreamExactGroup
	for _, arr := range buckets {
		if len(arr) < 2 {
			continue
		}
		sort.SliceStable(arr, func(i, j int) bool { return survivorRank(arr[i]) > survivorRank(arr[j]) })
		groups = append(groups, DreamExactGroup{
			Survivor:   arr[0],
			Duplicates: append([]DreamMemoryCandidate(nil), arr[1:]...),
		})
	}
	return groups
}

// DreamExactGroup names a survivor and the duplicates the L1a pass will
// archive against it.
type DreamExactGroup struct {
	Survivor   DreamMemoryCandidate
	Duplicates []DreamMemoryCandidate
}

// unionFind is a small disjoint-set used by the L2/L3 clusterer.
type unionFind struct {
	parent map[string]string
}

func newUnionFind() *unionFind { return &unionFind{parent: map[string]string{}} }

func (u *unionFind) find(x string) string {
	p, ok := u.parent[x]
	if !ok {
		u.parent[x] = x
		return x
	}
	for p != u.parent[p] {
		u.parent[p] = u.parent[u.parent[p]]
		p = u.parent[p]
	}
	return p
}

func (u *unionFind) union(a, b string) {
	ra := u.find(a)
	rb := u.find(b)
	if ra != rb {
		u.parent[ra] = rb
	}
}
