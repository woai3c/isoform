package main

import (
	"regexp"
	"sort"
	"strings"
)

// Display keywords are the curated, precision-first tags shown on an event card
// (mirrors AirJelly's `keywords` field). They are DISTINCT from Entities: entities
// are the high-recall stitching signal (regex + LLM, never capped, full of
// near-duplicate surface forms), whereas keywords are a small human-facing label
// set. The extraction LLM emits keywords directly; when it doesn't (older rows,
// no-LLM fallback, or it returned none) we derive them deterministically from the
// entities + title here. Either way the result is sanitized (deduped, capped,
// pure-digit and generic-stopword names dropped) so the card never shows the raw
// entity noise. Keywords that also appear in the title are intentionally KEPT:
// the tags double as clickable filters, so title-overlap is desired.

const maxDisplayKeywords = 5

var (
	// keywordSepRe unifies "-", "_" and whitespace so "deliverables-hub",
	// "deliverables_hub" and "Deliverables Hub" collapse to one comparison key.
	keywordSepRe = regexp.MustCompile(`[-_\s]+`)
	// keywordFileExtRe strips a trailing source/doc extension for the COMPARISON
	// key only (the displayed form keeps the extension — "TaskTimeline.tsx" is a
	// good tag, AirJelly keeps filenames). Used so "stream-card.tsx" and a bare
	// "stream-card" mention don't both survive.
	keywordFileExtRe = regexp.MustCompile(`\.[a-z0-9]{1,5}$`)
)

// keywordStopwords are generic words that never uniquely locate an event.
var keywordStopwords = map[string]bool{
	"coding": true, "browsing": true, "type safety": true, "code": true,
	"review": true, "debug": true, "debugging": true, "testing": true,
	"app": true, "project": true, "file": true, "document": true,
}

// normalizeKeywordKey returns the case/separator-insensitive comparison key for a
// keyword or entity name: lowercased, owner-prefix dropped (Isoform/x -> x),
// trailing file extension dropped, separators unified. Two surface forms of the
// same identifier share a key; genuinely different identifiers do not.
func normalizeKeywordKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.LastIndexByte(s, '/'); i >= 0 && i < len(s)-1 {
		s = s[i+1:]
	}
	s = keywordFileExtRe.ReplaceAllString(s, "")
	s = keywordSepRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// entityDisplayRank orders entity types by how display-worthy they are (higher =
// preferred when two forms collapse to the same key). Mirrors AirJelly's
// "proper nouns > tech > generic" priority.
func entityDisplayRank(t string) int {
	switch t {
	case entityPR:
		return 5
	case entityPerson:
		return 5
	case entityRepo:
		return 4
	case entityProject:
		return 4
	case entityTopic:
		return 3
	case entityChannel:
		return 1
	default:
		return 2
	}
}

// displayKeywordsFor resolves the final card tags for an event: prefer the LLM's
// curated keywords, falling back to a deterministic projection of the entities.
// Both paths drop pure-digit / generic-stopword names and cap the count; neither
// strips title-overlapping words (the tags are filters, so overlap is wanted).
func displayKeywordsFor(ev extractedEvent, entities []Entity) []string {
	if len(ev.Keywords) > 0 {
		if out := sanitizeLLMKeywords(ev.Keywords); len(out) > 0 {
			return out
		}
	}
	return deriveDisplayKeywords(entities, ev.Title, ev.Description)
}

// sanitizeLLMKeywords trusts the LLM's selection/order but enforces the invariants
// the prompt asks for (in case it slips): non-empty, deduped by normalized key,
// not pure digits, not a generic stopword, capped. We do NOT strip keywords that
// also appear in the title: the tags double as clickable filter handles (clicking
// "sth-event" filters that project), so title-overlap is intended — matching
// AirJelly, whose shipped tags routinely repeat title words.
func sanitizeLLMKeywords(keywords []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, kw := range keywords {
		kw = strings.TrimSpace(kw)
		if kw == "" || isAllDigits(kw) {
			continue
		}
		key := normalizeKeywordKey(kw)
		if key == "" || seen[key] || keywordStopwords[key] {
			continue
		}
		seen[key] = true
		out = append(out, kw)
		if len(out) >= maxDisplayKeywords {
			break
		}
	}
	return out
}

// deriveDisplayKeywords is the no-LLM fallback: pick the most display-worthy,
// non-duplicate entity names, ranked by (appears in title/description) then
// entity type, capped. Filenames are kept (good tags); pure numbers and generic
// stopwords are dropped. The cross-activity leakage that bloats entities ranks
// last (it isn't in this event's title/description) and falls off after the cap.
func deriveDisplayKeywords(entities []Entity, title, description string) []string {
	context := normalizeKeywordKey(title + " " + description)
	rawContext := strings.ToLower(title + " " + description)
	type cand struct {
		name     string
		key      string
		score    int  // 2 = identifier appears in title/desc, else 0
		rank     int  // entity display rank
		verbatim bool // this exact surface form appears in title/desc (prefer for display)
		order    int  // original index (stable tiebreak)
	}
	// betterForm decides which surface form to keep when two entities collapse to
	// the same identifier: prefer the human-written form (verbatim in title/desc),
	// then the higher-ranked entity type.
	betterForm := func(a, b cand) bool {
		if a.verbatim != b.verbatim {
			return a.verbatim
		}
		return a.rank > b.rank
	}
	seen := map[string]int{} // key -> index into cands
	var cands []cand
	for i, e := range entities {
		name := strings.TrimSpace(e.Name)
		key := normalizeKeywordKey(name)
		if key == "" || isAllDigits(name) || keywordStopwords[key] {
			continue
		}
		c := cand{
			name:     name,
			key:      key,
			rank:     entityDisplayRank(e.Type),
			verbatim: name != "" && strings.Contains(rawContext, strings.ToLower(name)),
			order:    i,
		}
		if context != "" && strings.Contains(context, key) {
			c.score = 2
		}
		if j, ok := seen[key]; ok {
			// Same identifier: keep max score for ranking, best form for display.
			if c.score > cands[j].score {
				cands[j].score = c.score
			}
			if betterForm(c, cands[j]) {
				cands[j].name = c.name
				cands[j].rank = c.rank
				cands[j].verbatim = c.verbatim
			}
			continue
		}
		seen[key] = len(cands)
		cands = append(cands, c)
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score > cands[j].score
		}
		if cands[i].rank != cands[j].rank {
			return cands[i].rank > cands[j].rank
		}
		return cands[i].order < cands[j].order
	})
	// Precision-first: keep ONLY identifiers that actually appear in this event's
	// title/description — those are the ones we can be confident locate THIS event.
	// Off-context entities (even strong repo/project/person types) are NEVER padded
	// in to fill the cap: that padding is exactly how cross-activity leakage (a
	// "tennis" file glanced at during a PR review, an unrelated repo in another tab)
	// bled into the tags. The cost is that a terse-title event may yield few or no
	// tags here — acceptable for this deterministic fallback, since the primary
	// (LLM) path supplies richer keywords and an empty tag row beats a wrong one.
	out := make([]string, 0, maxDisplayKeywords)
	for _, c := range cands {
		if c.score == 0 {
			break // sorted score-desc: once we hit 0, no in-context candidates remain
		}
		out = append(out, c.name)
		if len(out) >= maxDisplayKeywords {
			break
		}
	}
	return out
}
