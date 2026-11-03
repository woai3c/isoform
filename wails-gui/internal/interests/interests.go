// Package interests names what the user is genuinely interested in, from
// what the Mac already summarized (activity summaries, Purpose Map items,
// About you notes, chat titles, searches, the products and tools named while
// researching). A model reads that evidence a few times a day and names a
// handful of interests; everything else here is pure and runs on the Mac:
// the prompt, reading the answer, the privacy checks, matching a refresh
// against the stored list so ids and names stay put, and the ranking.
//
// Only an interest's name and its short queries ever leave the Mac, as web
// searches. Its why line, the evidence and the text the model read stay.
package interests

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Kind says what sort of interest it is.
type Kind string

const (
	KindProject  Kind = "project"  // something the user builds or does over time
	KindResearch Kind = "research" // a product, technology or question they look into
	KindTheme    Kind = "theme"    // a broader field they keep returning to
	KindRoutine  Kind = "routine"  // a recurring kind of work
)

// Source names where a piece of evidence came from.
type Source string

const (
	SourceActivity Source = "activity" // an activity summary
	SourceGoal     Source = "goal"     // an open Purpose Map item
	SourceAbout    Source = "about"    // an About you point
	SourceChat     Source = "chat"     // a chat title
	SourceSearch   Source = "search"   // a web search the user typed
	SourceNamed    Source = "named"    // a product, tool or paper a summary named
)

// Mode is the kind of model pass.
type Mode string

const (
	// ModeRefresh is the regular pass on the light model: the new evidence,
	// anchored on the stored list, whose names it keeps.
	ModeRefresh Mode = "refresh"
	// ModeConsolidate is the monthly pass on the main model over the whole
	// window: it merges, renames and drops.
	ModeConsolidate Mode = "consolidate"
)

const (
	NameMaxWords  = 6
	NameMaxRunes  = 48
	QueryMaxWords = 8
	QueryMaxRunes = 80
	MaxQueries    = 2
	WhyMaxRunes   = 160
	// MaxStored bounds the stored list; the view shows fewer.
	MaxStored = 20
	// maxRefs bounds the evidence kept with one interest.
	maxRefs  = 80
	halfLife = 5 * 24 * time.Hour

	// RefreshWindow bounds the evidence a refresh reads and an interest
	// keeps; ConsolidateWindow the evidence the monthly pass reads.
	RefreshWindow     = 14 * 24 * time.Hour
	ConsolidateWindow = 30 * 24 * time.Hour
)

// Item is one piece of evidence. Ref is a stable local key ("activity:<id>")
// that is kept with an interest; Text goes to the model and is never kept.
type Item struct {
	Ref     string
	Source  Source
	Text    string
	At      time.Time
	Minutes float64
}

// Ref is one piece of evidence kept with an interest: where it came from,
// when, and the time spent. It holds no text.
type Ref struct {
	Key     string  `json:"key"`
	Source  Source  `json:"source"`
	At      int64   `json:"at"`
	Minutes float64 `json:"minutes,omitempty"`
}

// Interest is one stored interest.
type Interest struct {
	// ID is stable across refreshes: a refresh that names the same interest
	// again keeps it, so pins, renames and feedback follow it.
	ID string `json:"id"`
	// Key is the lower-cased name, for feedback weights.
	Key  string `json:"key"`
	Name string `json:"name"`
	Why  string `json:"why"`
	// WhyVerified reports the why was written by a pass that read only
	// currently readable evidence. An interest carrying a search or named
	// reference (which a read re-authorizes by deriving its content hash
	// again, never by replaying the full original contributor closure) shows
	// its why only while verified; the next pass rewrites an unverified why
	// from currently readable evidence.
	WhyVerified bool     `json:"whyVerified,omitempty"`
	Kind        Kind     `json:"kind"`
	Confidence  float64  `json:"confidence"`
	Queries     []string `json:"queries"`
	Evidence    []Ref    `json:"evidence"`
	// FirstAt is when it was first named, SeenAt the last pass that named
	// it (unix ms).
	FirstAt int64   `json:"firstAt"`
	SeenAt  int64   `json:"seenAt"`
	Score   float64 `json:"score"`
}

// Stats summarize an interest's evidence.
type Stats struct {
	Items   int
	Days    int
	Minutes float64
	Sources []Source
	LastAt  time.Time
}

// Stats counts the evidence: pieces, local days, minutes, sources and the
// latest sighting.
func (it Interest) Stats(loc *time.Location) Stats {
	if loc == nil {
		loc = time.Local
	}
	days := map[string]bool{}
	sources := map[Source]bool{}
	var s Stats
	var last int64
	for _, ref := range it.Evidence {
		s.Items++
		s.Minutes += ref.Minutes
		sources[ref.Source] = true
		if ref.At > 0 {
			days[time.UnixMilli(ref.At).In(loc).Format("2006-01-02")] = true
		}
		if ref.At > last {
			last = ref.At
		}
	}
	s.Days = len(days)
	for source := range sources {
		s.Sources = append(s.Sources, source)
	}
	sort.Slice(s.Sources, func(i, j int) bool { return s.Sources[i] < s.Sources[j] })
	if last > 0 {
		s.LastAt = time.UnixMilli(last)
	}
	return s
}

// HasReDerivedRef reports an interest carrying a search or named reference.
// Those references store only a content hash, so a read can re-authorize them
// only by deriving the same hash from currently readable sources again, never
// by replaying the full original contributor closure.
func HasReDerivedRef(it Interest) bool {
	for _, ref := range it.Evidence {
		switch ref.Source {
		case SourceSearch, SourceNamed:
			return true
		default:
		}
	}
	return false
}

// KeyFor is the key of a name: its words, lower-cased.
func KeyFor(name string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '.' && r != '+' && r != '#'
	}), " ")
}

// IDFor is the id a new interest gets from its key. It never equals a
// topic id for the same words.
func IDFor(key string) string {
	sum := sha256.Sum256([]byte("interest\x00" + key))
	return hex.EncodeToString(sum[:6])
}

// similarWords are left out when two names are compared.
var similarStopWords = map[string]bool{
	"a": true, "an": true, "the": true, "and": true, "or": true, "of": true, "for": true, "on": true,
	"in": true, "with": true, "to": true, "new": true, "your": true, "my": true,
}

// nameWords are the words two names are compared on: lower-cased, a plural
// "s" dropped.
func nameWords(name string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(KeyFor(name), func(r rune) bool { return r == ' ' || r == '-' }) {
		if similarStopWords[w] {
			continue
		}
		if len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") {
			w = strings.TrimSuffix(w, "s")
		}
		out[w] = true
	}
	return out
}

// Similarity is how alike two names are, from 0 to 1: the shared words over
// all words, and 1 when every word of a name of two words or more is in the
// other.
func Similarity(a, b string) float64 {
	wa, wb := nameWords(a), nameWords(b)
	if len(wa) == 0 || len(wb) == 0 {
		return 0
	}
	shared := 0
	for w := range wa {
		if wb[w] {
			shared++
		}
	}
	small := len(wa)
	if len(wb) < small {
		small = len(wb)
	}
	if shared == small && small >= 2 {
		return 1
	}
	return float64(shared) / float64(len(wa)+len(wb)-shared)
}

// sameInterest is the bar for two names to be one interest. A model that
// words a stored interest differently ("open-source AI agent plugins" for
// "Magpie plugin system open source") still means the same one when the
// names share something (relatedNames) and most of what it cites, the
// stored one cites too (sameEvidence).
const (
	sameInterest = 0.5
	relatedNames = 0.2
	sameEvidence = 0.5
)

// evidenceOverlap is the share of fresh's evidence that current already
// holds.
func evidenceOverlap(fresh, current []Ref) float64 {
	if len(fresh) == 0 {
		return 0
	}
	held := map[string]bool{}
	for _, ref := range current {
		held[ref.Key] = true
	}
	shared := 0
	for _, ref := range fresh {
		if held[ref.Key] {
			shared++
		}
	}
	return float64(shared) / float64(len(fresh))
}

// matchScore says how surely fresh is the stored interest c, or 0.
func matchScore(f, c Interest) float64 {
	sim := Similarity(f.Name, c.Name)
	if sim >= sameInterest {
		return 1 + sim
	}
	if overlap := evidenceOverlap(f.Evidence, c.Evidence); sim >= relatedNames && overlap >= sameEvidence {
		return sim + overlap
	}
	return 0
}

// MergeOptions steer one Merge.
type MergeOptions struct {
	Mode Mode
	// Keep holds the ids the user pinned: never dropped.
	Keep map[string]bool
	// Removed holds the ids the user removed: kept for a restore, but never
	// matched, so a fresh interest is never folded into a hidden one.
	Removed map[string]bool
	// Held interests reserve their identity without contributing text or losing
	// evidence while a current read policy withholds them.
	Held   map[string]bool
	Now    time.Time
	Window time.Duration
}

// Merge folds one accepted answer into the stored list. A fresh interest
// that matches a stored one (matchScore) keeps the stored id; on a refresh
// it also keeps the stored name, on a consolidation it takes the fresh one.
// Evidence is joined. A stored interest the answer left out stays on a
// refresh until its evidence is older than the window, and goes on a
// consolidation unless the user pinned it; a removed one stays until its
// evidence ages out. A non-empty why the answer wrote is verified (it was
// learned from the evidence the prompt held); an empty one keeps the stored
// why and its verification.
func Merge(current, fresh []Interest, opts MergeOptions) []Interest {
	nowMs := opts.Now.UnixMilli()
	floor := opts.Now.Add(-opts.Window).UnixMilli()
	used := map[int]bool{}
	ids := map[string]bool{}
	for _, it := range current {
		ids[it.ID] = true
	}
	var out []Interest
	for _, f := range fresh {
		best, bestScore := -1, 0.0
		for i, c := range current {
			if used[i] || opts.Removed[c.ID] || opts.Held[c.ID] {
				continue
			}
			if score := matchScore(f, c); score > bestScore {
				best, bestScore = i, score
			}
		}
		if best < 0 {
			f.ID = uniqueID(IDFor(f.Key), ids)
			ids[f.ID] = true
			f.FirstAt = nowMs
			f.SeenAt = nowMs
			if f.Why != "" {
				f.WhyVerified = true
			}
			f.Evidence = pruneRefs(f.Evidence, floor)
			out = append(out, f)
			continue
		}
		used[best] = true
		c := current[best]
		merged := f
		if merged.Why != "" {
			merged.WhyVerified = true
		} else {
			// An empty answer, or an echo of a line the checks once blanked,
			// never erases a stored why; its verification goes with it.
			merged.Why, merged.WhyVerified = c.Why, c.WhyVerified
		}
		merged.ID = c.ID
		merged.FirstAt = c.FirstAt
		merged.SeenAt = nowMs
		if opts.Mode == ModeRefresh {
			merged.Name, merged.Key = c.Name, c.Key
		}
		merged.Evidence = pruneRefs(joinRefs(c.Evidence, f.Evidence), floor)
		out = append(out, merged)
	}
	for i, c := range current {
		if used[i] {
			continue
		}
		if opts.Held[c.ID] {
			out = append(out, c)
			continue
		}
		c.Evidence = pruneRefs(c.Evidence, floor)
		switch {
		case opts.Keep[c.ID]:
			out = append(out, c)
		case len(c.Evidence) == 0:
		case opts.Mode == ModeConsolidate && !opts.Removed[c.ID]:
		default:
			out = append(out, c)
		}
	}
	return out
}

func uniqueID(id string, taken map[string]bool) string {
	candidate := id
	for n := 2; taken[candidate]; n++ {
		candidate = IDFor(fmt.Sprintf("%s#%d", id, n))
	}
	return candidate
}

// DedupeOptions steer one Dedupe.
type DedupeOptions struct {
	// Keep holds the ids the user pinned or renamed: their identity wins a
	// fold, and two kept rows never fold into each other.
	Keep map[string]bool
	// Removed holds the ids the user removed: they never fold, so a shown
	// interest is never folded into a hidden one or the other way around.
	Removed map[string]bool
	// Readable reports whether a row's recorded evidence still authorizes a
	// read. A pair folds only when the surviving row is readable, and only
	// readable references join it, so withheld evidence is never re-linked.
	Readable func(Interest) bool
}

// Dedupe folds stored near-duplicates (names at or above the same-interest
// bar) into one row: a refresh that could not match a held or reworded twin
// stored the same interest twice. The surviving row keeps the id the user
// pinned or renamed (else the older one), the non-empty why, and the joined
// readable evidence.
func Dedupe(list []Interest, opts DedupeOptions) []Interest {
	readable := opts.Readable
	if readable == nil {
		readable = func(Interest) bool { return true }
	}
	consumed := map[int]bool{}
	out := make([]Interest, 0, len(list))
	for i := range list {
		if consumed[i] {
			continue
		}
		row := list[i]
		if opts.Removed[row.ID] {
			out = append(out, row)
			continue
		}
		for j := i + 1; j < len(list); j++ {
			if consumed[j] || opts.Removed[list[j].ID] || Similarity(row.Name, list[j].Name) < sameInterest {
				continue
			}
			folded, ok := foldPair(row, list[j], opts.Keep, readable)
			if !ok {
				continue
			}
			consumed[j] = true
			row = folded
		}
		out = append(out, row)
	}
	return out
}

// foldPair folds b into a (or a into b when the user's choice says so) and
// reports the surviving row, or none when the pair may not fold.
func foldPair(a, b Interest, keep map[string]bool, readable func(Interest) bool) (Interest, bool) {
	survivor, other := a, b
	switch {
	case keep[a.ID] && keep[b.ID]:
		return Interest{}, false
	case keep[b.ID]:
		survivor, other = b, a
	case !keep[a.ID] && b.FirstAt > 0 && (a.FirstAt == 0 || b.FirstAt < a.FirstAt):
		survivor, other = b, a
	default:
	}
	folded := survivor
	// The non-empty why wins, and a verified one beats an unverified one.
	if other.Why != "" && (folded.Why == "" || (!folded.WhyVerified && other.WhyVerified)) {
		folded.Why, folded.WhyVerified = other.Why, other.WhyVerified
	}
	if other.Confidence > folded.Confidence {
		folded.Confidence = other.Confidence
	}
	if len(folded.Queries) == 0 {
		folded.Queries = other.Queries
	}
	if other.FirstAt > 0 && (folded.FirstAt == 0 || other.FirstAt < folded.FirstAt) {
		folded.FirstAt = other.FirstAt
	}
	if other.SeenAt > folded.SeenAt {
		folded.SeenAt = other.SeenAt
	}
	var evidence []Ref
	for _, ref := range joinRefs(survivor.Evidence, other.Evidence) {
		if readable(Interest{Evidence: []Ref{ref}}) {
			evidence = append(evidence, ref)
		}
	}
	folded.Evidence = evidence
	if len(folded.Evidence) == 0 || !readable(folded) {
		return Interest{}, false
	}
	return folded, true
}

// joinRefs joins two evidence lists, one ref per key, the later one kept.
func joinRefs(a, b []Ref) []Ref {
	byKey := map[string]Ref{}
	var order []string
	for _, list := range [][]Ref{a, b} {
		for _, ref := range list {
			if _, ok := byKey[ref.Key]; !ok {
				order = append(order, ref.Key)
			}
			byKey[ref.Key] = ref
		}
	}
	out := make([]Ref, 0, len(order))
	for _, key := range order {
		out = append(out, byKey[key])
	}
	return out
}

// pruneRefs drops evidence older than floor (undated evidence stays) and
// keeps the newest maxRefs.
func pruneRefs(refs []Ref, floor int64) []Ref {
	out := make([]Ref, 0, len(refs))
	for _, ref := range refs {
		if ref.At != 0 && ref.At < floor {
			continue
		}
		out = append(out, ref)
	}
	if len(out) > maxRefs {
		sort.SliceStable(out, func(i, j int) bool { return out[i].At > out[j].At })
		out = out[:maxRefs]
	}
	return out
}

// Rank scores and orders the list, best first, at most MaxStored; an
// interest keep holds (pinned) is never cut. The score is the model's
// confidence, raised by time spent, by the number of sources and days the
// evidence spans, and by a feedback weight on the key, and halved for every
// five days since the latest evidence.
func Rank(list []Interest, weights map[string]float64, keep map[string]bool, now time.Time, loc *time.Location) []Interest {
	out := append([]Interest(nil), list...)
	for i := range out {
		stats := out[i].Stats(loc)
		s := out[i].Confidence
		s *= 1 + math.Log1p(stats.Minutes)
		s *= 1 + 0.25*float64(max(len(stats.Sources)-1, 0))
		s *= 1 + 0.3*float64(min(max(stats.Days-1, 0), 6))
		if !stats.LastAt.IsZero() && now.After(stats.LastAt) {
			s *= math.Pow(0.5, float64(now.Sub(stats.LastAt))/float64(halfLife))
		}
		if w, ok := weights[out[i].Key]; ok && w > 0 {
			s *= w
		}
		out[i].Score = math.Round(s*100) / 100
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Name < out[j].Name
	})
	if len(out) <= MaxStored {
		return out
	}
	kept := out[:0:0]
	room := MaxStored
	for _, it := range out {
		if keep[it.ID] {
			room--
		}
	}
	for _, it := range out {
		switch {
		case keep[it.ID]:
			kept = append(kept, it)
		case room > 0:
			kept = append(kept, it)
			room--
		default:
		}
	}
	return kept
}
