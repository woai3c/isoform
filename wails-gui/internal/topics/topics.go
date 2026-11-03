// Package topics finds what the user has been looking at, as a handful of
// short labels, from text the Mac already observed: searches, window titles,
// meeting, chat and goal titles. It runs on the Mac only. Nothing here calls
// a model or the network, and a label is at most a few words, never a URL, a
// path, an address or a sentence of page text.
//
// Rank is pure: given the same signals and options it returns the same
// topics, so every ranking and privacy rule is a table test.
package topics

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Source names where a signal came from. Each source has its own weight:
// what the user typed or set as a goal says more than a window they passed.
type Source string

const (
	SourceSearch   Source = "search"   // a web search the user ran
	SourcePage     Source = "page"     // a page title from the browser history
	SourceWindow   Source = "window"   // a window title, with the time spent in it
	SourceTimeline Source = "timeline" // a line of the activity timeline summary
	SourceEntity   Source = "entity"   // a topic the activity summaries named
	SourceMeeting  Source = "meeting"  // a meeting or calendar event title
	SourceChat     Source = "chat"     // a chat or side chat title
	SourceMemory   Source = "memory"   // an About you entry title
	SourceGoal     Source = "goal"     // a Purpose Map goal or commitment
	SourceFile     Source = "file"     // a recent file name, without its folder
)

func sourceWeight(source Source) float64 {
	switch source {
	case SourceGoal, SourceSearch:
		return 3
	case SourceChat:
		return 2.5
	case SourceEntity:
		return 2
	case SourceMemory, SourceMeeting:
		return 1.5
	case SourceWindow, SourcePage, SourceFile:
		return 1
	case SourceTimeline:
		return 0.8
	default:
		return 0.5
	}
}

// explicitSource reports a source where one sighting is enough: the user
// wrote it down or asked about it.
func explicitSource(source Source) bool {
	switch source {
	case SourceGoal, SourceChat, SourceMemory:
		return true
	default:
		return false
	}
}

// Signal is one piece of observed text. Count folds repeats of the same text
// (the same search run three times) into one signal; zero counts as one.
type Signal struct {
	Source Source
	Text   string
	At     time.Time
	Dwell  time.Duration
	Count  int
}

// Topic is one ranked label.
type Topic struct {
	// ID is stable for the same key, so pins, renames and feedback follow a
	// topic across runs.
	ID string
	// Key is the lower-cased phrase; Label is its most common written form.
	Key      string
	Label    string
	Score    float64
	FirstAt  time.Time
	LastAt   time.Time
	Days     int
	Mentions int
	Sources  []Source
}

// Options bound and steer one ranking.
type Options struct {
	Now      time.Time
	Window   time.Duration // signals older than this are ignored; 14 days when zero
	Location *time.Location
	Limit    int // at most this many topics; 10 when zero
	// Names are people the user knows (contacts, attendees, the user). A
	// phrase with any of their name words is never a topic, so a person's
	// name never leaves the Mac as a search.
	Names []string
	// Blocked are keys the user removed or said no to. A phrase that
	// contains one, or is contained in one, is left out.
	Blocked []string
	// Weights multiply a key's score: feedback on finds raises or lowers it.
	Weights map[string]float64
}

const (
	defaultWindow = 14 * 24 * time.Hour
	defaultLimit  = 10
	halfLife      = 5 * 24 * time.Hour
	maxPhrase     = 3
	maxLabelBytes = 48
	// specificShare: a longer phrase replaces the shorter one inside it when
	// it carries at least this share of the shorter one's score.
	specificShare = 0.5
)

// IDFor is the stable id of a key.
func IDFor(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:6])
}

// KeyFor normalizes a label the way Rank keys phrases, so a renamed or
// pinned label can be compared with extracted ones.
func KeyFor(label string) string {
	var words []string
	for _, run := range phraseRuns(label) {
		for _, tok := range run {
			words = append(words, tok.key)
		}
	}
	return strings.Join(words, " ")
}

type accumulator struct {
	key      string
	tokens   []string
	weight   float64
	mentions int
	days     map[string]bool
	sources  map[Source]bool
	explicit bool
	repeated bool
	product  bool
	// shaped is a phrase made only of product-shaped words, a shape a name
	// never has (productNamed). Han text has no such shape.
	shaped bool
	// nearBlocked counts the sightings that came with a blocked topic, so
	// a phrase that mostly lives beside one ("async runtime" beside "Rust
	// async") goes with it.
	nearBlocked int
	first       time.Time
	last        time.Time
	surfaces    map[string]int
}

// Rank turns signals into at most opts.Limit topics, best first.
func Rank(signals []Signal, opts Options) []Topic {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	window := opts.Window
	if window <= 0 {
		window = defaultWindow
	}
	loc := opts.Location
	if loc == nil {
		loc = time.Local
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	names := nameWords(opts.Names)
	blocked := make([][]string, 0, len(opts.Blocked))
	for _, key := range opts.Blocked {
		if fields := strings.Fields(KeyFor(key)); len(fields) > 0 {
			blocked = append(blocked, fields)
		}
	}

	accs := map[string]*accumulator{}
	for _, sig := range signals {
		if sig.At.IsZero() || sig.At.After(now) || now.Sub(sig.At) > window {
			continue
		}
		weight := signalWeight(sig, now)
		if weight <= 0 {
			continue
		}
		day := sig.At.In(loc).Format("2006-01-02")
		seen := map[string]bool{}
		runs := phraseRuns(sig.Text)
		withBlocked := runsMention(runs, blocked)
		for _, run := range runs {
			people := personTokens(run, names, sig.Source)
			for start := range run {
				for n := 1; n <= maxPhrase && start+n <= len(run); n++ {
					phrase := run[start : start+n]
					if !usablePhrase(phrase, people[start:start+n]) {
						continue
					}
					key := phraseKey(phrase)
					if seen[key] {
						continue
					}
					seen[key] = true
					acc := accs[key]
					if acc == nil {
						acc = &accumulator{
							key:      key,
							tokens:   strings.Fields(key),
							days:     map[string]bool{},
							sources:  map[Source]bool{},
							surfaces: map[string]int{},
							first:    sig.At,
						}
						accs[key] = acc
					}
					acc.weight += weight
					acc.mentions++
					acc.days[day] = true
					acc.sources[sig.Source] = true
					acc.explicit = acc.explicit || explicitSource(sig.Source)
					acc.repeated = acc.repeated || sig.Count > 1
					acc.product = acc.product || productLike(phrase)
					acc.shaped = acc.shaped || productNamed(phrase)
					acc.surfaces[phraseSurface(phrase)]++
					if withBlocked {
						acc.nearBlocked++
					}
					if sig.At.Before(acc.first) {
						acc.first = sig.At
					}
					if sig.At.After(acc.last) {
						acc.last = sig.At
					}
				}
			}
		}
	}

	scored := make([]*scoredPhrase, 0, len(accs))
	for _, acc := range accs {
		if !qualifies(acc) || overlapsAny(acc.tokens, blocked) || (acc.nearBlocked > 0 && acc.nearBlocked*2 >= acc.mentions) {
			continue
		}
		scored = append(scored, &scoredPhrase{acc: acc, score: score(acc, opts.Weights)})
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].acc.key < scored[j].acc.key
	})

	var picked []*scoredPhrase
	used := map[string]bool{}
	for _, cand := range scored {
		if len(picked) >= limit {
			break
		}
		if used[cand.acc.key] {
			continue
		}
		choice := cand
		// Prefer the specific phrase ("wails drag regions") over the word
		// inside it ("wails") when the specific one carries enough weight.
		for _, other := range scored {
			if used[other.acc.key] || len(other.acc.tokens) <= len(choice.acc.tokens) {
				continue
			}
			if containsTokens(other.acc.tokens, choice.acc.tokens) && other.score >= specificShare*choice.score {
				choice = other
			}
		}
		used[cand.acc.key] = true
		used[choice.acc.key] = true
		if conflicts(choice, picked) {
			continue
		}
		picked = append(picked, choice)
	}

	out := make([]Topic, 0, len(picked))
	for _, p := range picked {
		out = append(out, p.topic())
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

type scoredPhrase struct {
	acc   *accumulator
	score float64
}

func (p *scoredPhrase) topic() Topic {
	sources := make([]Source, 0, len(p.acc.sources))
	for source := range p.acc.sources {
		sources = append(sources, source)
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i] < sources[j] })
	return Topic{
		ID:       IDFor(p.acc.key),
		Key:      p.acc.key,
		Label:    bestSurface(p.acc.surfaces),
		Score:    math.Round(p.score*100) / 100,
		FirstAt:  p.acc.first,
		LastAt:   p.acc.last,
		Days:     len(p.acc.days),
		Mentions: p.acc.mentions,
		Sources:  sources,
	}
}

// conflicts reports a candidate that repeats a picked topic (one phrase
// inside the other) or crowds one word into a third topic.
func conflicts(cand *scoredPhrase, picked []*scoredPhrase) bool {
	shared := map[string]int{}
	for _, p := range picked {
		if containsTokens(p.acc.tokens, cand.acc.tokens) || containsTokens(cand.acc.tokens, p.acc.tokens) {
			return true
		}
		for _, tok := range cand.acc.tokens {
			if containsTokens(p.acc.tokens, []string{tok}) && !numeric(tok) {
				shared[tok]++
			}
		}
	}
	for _, count := range shared {
		if count >= 2 {
			return true
		}
	}
	return false
}

func signalWeight(sig Signal, now time.Time) float64 {
	age := now.Sub(sig.At)
	recency := math.Pow(0.5, float64(age)/float64(halfLife))
	dwell := 1.0
	if minutes := sig.Dwell.Minutes(); minutes > 0 {
		dwell += math.Log2(1+minutes) / 2
	}
	count := 1.0
	if sig.Count > 1 {
		count += math.Log2(float64(sig.Count))
	}
	return sourceWeight(sig.Source) * recency * dwell * count
}

func score(acc *accumulator, weights map[string]float64) float64 {
	s := acc.weight
	s *= 1 + 0.5*float64(len(acc.days)-1)
	s *= 1 + 0.3*float64(len(acc.sources)-1)
	switch len(acc.tokens) {
	case 1:
		if !acc.product {
			s *= 0.6
		}
	case 2:
		s *= 1.2
	default:
		s *= 1.3
	}
	if w, ok := weights[acc.key]; ok && w > 0 {
		s *= w
	}
	return s
}

// qualifies keeps phrases seen more than once, or once where the user said
// it themselves. A single word needs more: three sightings, or the shape of
// a product name ("SwiftUI", "M4", "Kubernetes 1.31").
//
// Every phrase, whatever showed it (titles, summaries, meetings, About you,
// goals, live or from the journal), needs three different days unless every
// word in it has a product shape no name has (productNamed; Han text never
// does): a name nobody listed ("okonkwo resume", "程小红 简历") turns up a
// day or two, a real interest keeps coming back. A meeting or an About-you
// entry does not vouch for a window title beside it.
func qualifies(acc *accumulator) bool {
	if len(acc.days) < 3 && !acc.shaped {
		return false
	}
	if len(acc.tokens) == 1 {
		return acc.mentions >= 3 || (acc.product && (acc.mentions >= 2 || acc.explicit))
	}
	return acc.mentions >= 2 || acc.explicit || acc.repeated
}

func overlapsAny(tokens []string, blocked [][]string) bool {
	for _, b := range blocked {
		if containsTokens(tokens, b) || containsTokens(b, tokens) {
			return true
		}
	}
	return false
}

// runsMention reports whether any blocked phrase appears in the runs.
func runsMention(runs [][]token, blocked [][]string) bool {
	for _, run := range runs {
		keys := make([]string, len(run))
		for i, tok := range run {
			keys[i] = tok.key
		}
		for _, b := range blocked {
			if containsTokens(keys, b) {
				return true
			}
		}
	}
	return false
}

// containsTokens reports whether needle appears as a contiguous run in hay.
func containsTokens(hay, needle []string) bool {
	if len(needle) == 0 || len(needle) > len(hay) {
		return false
	}
	for i := 0; i+len(needle) <= len(hay); i++ {
		match := true
		for j := range needle {
			if hay[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func bestSurface(surfaces map[string]int) string {
	best, bestCount := "", -1
	for surface, count := range surfaces {
		if count > bestCount || (count == bestCount && surface < best) {
			best, bestCount = surface, count
		}
	}
	if len(best) > maxLabelBytes {
		cut := maxLabelBytes
		for cut > 0 && !utf8.RuneStart(best[cut]) {
			cut--
		}
		best = strings.TrimSpace(best[:cut])
	}
	return best
}

// token is one word of a phrase: its written form and its key.
type token struct {
	surface string
	key     string
}

func phraseKey(phrase []token) string {
	keys := make([]string, len(phrase))
	for i, tok := range phrase {
		keys[i] = tok.key
	}
	return strings.Join(keys, " ")
}

func phraseSurface(phrase []token) string {
	words := make([]string, len(phrase))
	for i, tok := range phrase {
		words[i] = tok.surface
	}
	return strings.Join(words, " ")
}

// personTokens marks the words of a run that name a person: a known name,
// a name-like word, and a capitalized word right beside one ("Priya Raman"
// marks both). In searches and page titles the word right after a name is
// marked in any case too, since lookups are typed in lower case ("sam
// altman").
func personTokens(run []token, names map[string]bool, source Source) []bool {
	marks := make([]bool, len(run))
	for i, tok := range run {
		marks[i] = names[tok.key] || nameLike(tok, source)
	}
	lookup := source == SourceSearch || source == SourcePage
	out := append([]bool(nil), marks...)
	for i, tok := range run {
		if out[i] {
			continue
		}
		if lookup && i > 0 && marks[i-1] {
			out[i] = true
			continue
		}
		if !capitalized(tok.surface) {
			continue
		}
		if (i > 0 && marks[i-1]) || (i+1 < len(run) && marks[i+1]) {
			out[i] = true
		}
	}
	return out
}

func capitalized(surface string) bool {
	runes := []rune(surface)
	return len(runes) > 1 && unicode.IsUpper(runes[0]) && unicode.IsLower(runes[1])
}

func usablePhrase(phrase []token, people []bool) bool {
	allNumeric := true
	for i, tok := range phrase {
		if people[i] {
			return false
		}
		if !numeric(tok.key) {
			allNumeric = false
		}
	}
	if allNumeric {
		return false
	}
	// A phrase never starts or ends on a bare number ("2 drafts").
	if numeric(phrase[0].key) && len(phrase) > 1 {
		return false
	}
	return true
}

// productLike is a word written like a name of a thing: an inner capital
// ("SwiftUI", "iOS"), a digit ("M4", "Go1.25"), or a version beside it.
// A Han word counts too: its script has no capitals to show the shape.
func productLike(phrase []token) bool {
	if productShaped(phrase) {
		return true
	}
	for _, tok := range phrase {
		if hanToken(tok.key) {
			return true
		}
	}
	return false
}

// productNamed reports a phrase made only of product-shaped words (numbers
// aside, and at least one): "SwiftUI Charts", "M4", "iOS 18". One plain
// word, an all-caps word or a "Mc" name in it ("Okonkwo CV", "okonkwo resume
// v2", "McCarthy") is enough to make it not product-named, and a single word
// needs a digit: "LeBron" or "DeShawn" alone is shaped like a product but is
// a name.
func productNamed(phrase []token) bool {
	words := 0
	digit := false
	for _, tok := range phrase {
		if numeric(tok.key) {
			digit = true
			continue
		}
		if !wordShaped(tok.surface) {
			return false
		}
		words++
		digit = digit || strings.ContainsAny(tok.surface, "0123456789")
	}
	return words > 1 || (words == 1 && digit)
}

// wordShaped reports a word with a digit beside its letters ("M4", "go1.25")
// or a capital right after a lower-case letter ("SwiftUI", "iPhone"), that is
// not a code symbol, not all capitals and not a "Mc" name.
func wordShaped(surface string) bool {
	if identifierLike(surface) {
		return false
	}
	runes := []rune(surface)
	if len(runes) > 2 && runes[0] == 'M' && runes[1] == 'c' && unicode.IsUpper(runes[2]) {
		return false
	}
	letter, digit, innerCapital := false, false, false
	for i, r := range runes {
		switch {
		case unicode.IsDigit(r):
			digit = true
		case unicode.IsLetter(r):
			letter = true
			if i > 0 && unicode.IsUpper(r) && unicode.IsLower(runes[i-1]) {
				innerCapital = true
			}
		}
	}
	return (letter && digit) || innerCapital
}

// productShaped reports an inner capital or a digit in a word of the phrase.
func productShaped(phrase []token) bool {
	for _, tok := range phrase {
		if identifierLike(tok.surface) {
			continue
		}
		runes := []rune(tok.surface)
		for i, r := range runes {
			if unicode.IsDigit(r) {
				return true
			}
			if i > 0 && unicode.IsUpper(r) {
				return true
			}
		}
	}
	return false
}

// identifierLike is a code symbol ("viewReturn", "useState"): lower-case
// letters, then a capital starting a lower-case word. "iPhone" and "macOS"
// are not.
func identifierLike(surface string) bool {
	runes := []rune(surface)
	lower := 0
	for lower < len(runes) && unicode.IsLower(runes[lower]) {
		lower++
	}
	return lower >= 2 && lower+1 < len(runes) && unicode.IsUpper(runes[lower]) && unicode.IsLower(runes[lower+1])
}

func numeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) && r != '.' {
			return false
		}
	}
	return true
}

func hanToken(s string) bool {
	for _, r := range s {
		if !unicode.Is(unicode.Han, r) {
			return false
		}
	}
	return s != ""
}

func nameWords(names []string) map[string]bool {
	out := map[string]bool{}
	for _, name := range names {
		for _, run := range phraseRuns(name) {
			for _, tok := range run {
				if len([]rune(tok.key)) >= 2 && !numeric(tok.key) {
					out[tok.key] = true
				}
			}
		}
		// An address names its owner too: "ana.lopez@" reads as two words.
		if at := strings.IndexByte(name, '@'); at > 0 {
			for _, part := range strings.FieldsFunc(strings.ToLower(name[:at]), func(r rune) bool {
				return r == '.' || r == '_' || r == '-' || r == '+'
			}) {
				if len(part) >= 2 {
					out[part] = true
				}
			}
		}
	}
	return out
}

// Phrase is one short phrase and how often a day's signals showed it.
type Phrase struct {
	Text   string
	Source Source
	Count  int
}

// Digest folds signals into the phrases worth keeping for later rankings:
// two- and three-word phrases and words shaped like a product name, most
// seen first, at most limit. A phrase inside a longer kept phrase seen as
// often is dropped, since the longer one brings it back. Names are left
// out the same way Rank leaves them out.
func Digest(signals []Signal, names []string, limit int) []Phrase {
	nameSet := nameWords(names)
	type entry struct {
		key     string
		tokens  []string
		surface string
		source  Source
		count   int
		weight  float64
	}
	byKey := map[string]*entry{}
	for _, sig := range signals {
		count := sig.Count
		if count < 1 {
			count = 1
		}
		seen := map[string]bool{}
		for _, run := range phraseRuns(sig.Text) {
			people := personTokens(run, nameSet, sig.Source)
			for start := range run {
				for n := 1; n <= maxPhrase && start+n <= len(run); n++ {
					phrase := run[start : start+n]
					if !usablePhrase(phrase, people[start:start+n]) || (n == 1 && !productLike(phrase)) {
						continue
					}
					key := phraseKey(phrase)
					if seen[key] {
						continue
					}
					seen[key] = true
					e := byKey[key]
					if e == nil {
						e = &entry{key: key, tokens: strings.Fields(key), surface: phraseSurface(phrase), source: sig.Source}
						byKey[key] = e
					}
					e.count += count
					e.weight += float64(count) * sourceWeight(sig.Source)
					if sourceWeight(sig.Source) > sourceWeight(e.source) {
						e.source = sig.Source
					}
				}
			}
		}
	}
	entries := make([]*entry, 0, len(byKey))
	for _, e := range byKey {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].weight != entries[j].weight {
			return entries[i].weight > entries[j].weight
		}
		if len(entries[i].tokens) != len(entries[j].tokens) {
			return len(entries[i].tokens) > len(entries[j].tokens)
		}
		return entries[i].key < entries[j].key
	})
	var out []Phrase
	var kept []*entry
	for _, e := range entries {
		if limit > 0 && len(out) >= limit {
			break
		}
		covered := false
		for _, k := range kept {
			if k.count >= e.count && containsTokens(k.tokens, e.tokens) {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		kept = append(kept, e)
		out = append(out, Phrase{Text: e.surface, Source: e.source, Count: e.count})
	}
	return out
}
