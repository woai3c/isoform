package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
)

// CorrectionKind discriminates rejection rows from reassignment rows when
// they are merged into a single correction stream.
type CorrectionKind string

const (
	CorrectionKindReject   CorrectionKind = "reject"
	CorrectionKindReassign CorrectionKind = "reassign"
)

// CorrectionSummary is the merged view of one user correction (either a
// reject or a reassign) used by the activity triager to:
//
//  1. Inject a "past corrections" hint into the memory-generation prompt so
//     the model has a fighting chance to avoid recreating the same wrong
//     memory.
//  2. Run the post-generation correction-match gate (corrections_gate.go),
//     which refuses to save a memory whose title/points overlap closely
//     enough with a recent correction that it is almost certainly the same
//     bug class re-emerging.
//
// The struct flattens the two source schemas (MemoryRejectionEntry,
// MemoryReassignmentEntry) into a uniform shape so downstream code does not
// need to switch on Kind for the common fields.
type CorrectionSummary struct {
	Kind       CorrectionKind `json:"kind"`
	OccurredAt int64          `json:"occurredAt"`
	MemoryID   string         `json:"memoryId,omitempty"`
	Title      string         `json:"title,omitempty"`
	Source     string         `json:"source,omitempty"`
	Dimension  string         `json:"dimension,omitempty"`
	Reason     string         `json:"reason,omitempty"`
	TargetName string         `json:"targetName,omitempty"` // only meaningful for reassign
	TargetKind string         `json:"targetKind,omitempty"` // only meaningful for reassign
	SessionIDs []string       `json:"sessionIds,omitempty"`
}

// LoadRecentCorrections reads .rejections.jsonl + .reassignments.jsonl from
// dir, merges them, returns the maxN most-recent rows (newest first).
// Missing files are not an error — a brand-new install simply has nothing
// to learn from yet. Corrupt JSON lines are skipped with a log line so a
// single bad row never poisons the whole stream.
func LoadRecentCorrections(dir string, maxN int) ([]CorrectionSummary, error) {
	if maxN <= 0 {
		maxN = 50
	}
	rejections, err := loadRejections(filepath.Join(dir, memoryRejectionsLogName))
	if err != nil {
		return nil, fmt.Errorf("load rejections: %w", err)
	}
	reassignments, err := loadReassignments(filepath.Join(dir, memoryReassignmentsLogName))
	if err != nil {
		return nil, fmt.Errorf("load reassignments: %w", err)
	}
	merged := make([]CorrectionSummary, 0, len(rejections)+len(reassignments))
	merged = append(merged, rejections...)
	merged = append(merged, reassignments...)
	sort.SliceStable(merged, func(i, j int) bool {
		return merged[i].OccurredAt > merged[j].OccurredAt
	})
	if len(merged) > maxN {
		merged = merged[:maxN]
	}
	return merged, nil
}

func loadRejections(path string) ([]CorrectionSummary, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []CorrectionSummary
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var raw MemoryRejectionEntry
		if err := json.Unmarshal(line, &raw); err != nil {
			continue // skip corrupt row, keep going
		}
		out = append(out, CorrectionSummary{
			Kind:       CorrectionKindReject,
			OccurredAt: raw.RejectedAt,
			MemoryID:   raw.MemoryID,
			Title:      raw.Title,
			Source:     raw.Source,
			Dimension:  raw.Dimension,
			Reason:     raw.Reason,
			SessionIDs: raw.ActivitySessionIDs,
		})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func loadReassignments(path string) ([]CorrectionSummary, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []CorrectionSummary
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var raw MemoryReassignmentEntry
		if err := json.Unmarshal(line, &raw); err != nil {
			continue
		}
		out = append(out, CorrectionSummary{
			Kind:       CorrectionKindReassign,
			OccurredAt: raw.ReassignedAt,
			MemoryID:   raw.MemoryID,
			Title:      raw.Title,
			Source:     raw.Source,
			Dimension:  raw.OriginalDimension,
			Reason:     raw.Reason,
			TargetName: raw.CorrectPrincipalName,
			TargetKind: raw.CorrectPrincipalKind,
			SessionIDs: raw.ActivitySessionIDs,
		})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// FormatCorrectionsPromptBlock emits only code-owned attribution guidance.
// Private titles, reasons and principal names remain available to the local
// post-generation match gate, but are never model inputs or reusable facts.
func FormatCorrectionsPromptBlock(corrections []CorrectionSummary) string {
	var rejected, reassigned bool
	for _, correction := range corrections {
		switch correction.Kind {
		case CorrectionKindReject:
			rejected = true
		case CorrectionKindReassign:
			reassigned = true
		}
	}
	if !rejected && !reassigned {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## Past user corrections\n\n")
	b.WriteString("Apply these attribution checks to the current source evidence. Previously corrected claims are checked locally after generation.\n\n")
	if rejected {
		b.WriteString("- REJECTED: Extract only claims explicitly supported by the current source. Skip uncertain or inferred personal attributions.\n")
	}
	if reassigned {
		b.WriteString("- REASSIGNED: Keep each speaker's statements attached to that speaker. Attribute a statement to the user only when the source explicitly identifies the user as its speaker or confirms it as their own.\n")
	}
	b.WriteString("\n")
	return b.String()
}

// NormalizeForMatch lowercases + collapses whitespace + strips most
// punctuation so two surface forms of the same memory ("I love chestnuts!"
// and "I LOVE Chestnuts") tokenize to the same words. CJK characters are
// preserved as individual tokens (one per rune) so "我喜欢栗子" matches
// "我喜欢栗子。"
func NormalizeForMatch(s string) []string {
	var tokens []string
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			tokens = append(tokens, current.String())
			current.Reset()
		}
	}
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.Is(unicode.Han, r), unicode.Is(unicode.Hiragana, r), unicode.Is(unicode.Katakana, r):
			flush()
			tokens = append(tokens, string(r))
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			current.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return tokens
}

// JaccardSimilarity returns the intersection-over-union of two token sets.
// Empty inputs return 0. Used by the correction-match gate to decide
// whether a freshly-generated memory point is "close enough" to a recently
// rejected one to be the same bug class.
func JaccardSimilarity(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	setA := make(map[string]struct{}, len(a))
	for _, t := range a {
		setA[t] = struct{}{}
	}
	setB := make(map[string]struct{}, len(b))
	for _, t := range b {
		setB[t] = struct{}{}
	}
	union := make(map[string]struct{}, len(setA)+len(setB))
	for t := range setA {
		union[t] = struct{}{}
	}
	for t := range setB {
		union[t] = struct{}{}
	}
	var intersection int
	for t := range setA {
		if _, ok := setB[t]; ok {
			intersection++
		}
	}
	if len(union) == 0 {
		return 0
	}
	return float64(intersection) / float64(len(union))
}

// CorrectionMatch is the result of a single (candidate, correction) pair
// scoring above the threshold. Returned by FindMatchingCorrection so the
// caller can log which correction blocked the write.
type CorrectionMatch struct {
	Correction CorrectionSummary
	Candidate  string  // the memory title or point that matched
	Score      float64 // best similarity score
}

// CorrectionMatchThreshold is the jaccard cutoff above which a candidate
// memory is considered to "re-emerge" a previously-corrected one. Tuned
// conservatively (0.6) — false positives here mean silently dropping a
// genuine memory; we'd rather a marginal candidate slip through than block
// real signal. Make it adjustable later if real-world tuning needs it.
const CorrectionMatchThreshold = 0.6

// FindMatchingCorrection scans corrections for any whose normalized title
// or reason overlaps the candidate strings (title + memory points) by at
// least CorrectionMatchThreshold. Returns the strongest match (highest
// score) or nil when nothing crosses the bar.
//
// The candidate side is the full set of "things this memory says" — title
// plus each point — because a model can re-emerge a previously-rejected
// claim under a fresh title. We don't compare against the deleted entry's
// body (we never stored it) so the match relies on title overlap + reason
// overlap, which both encode the essence of why the user objected.
func FindMatchingCorrection(corrections []CorrectionSummary, candidateTitle string, candidatePoints []string) *CorrectionMatch {
	candidates := make([][]string, 0, 1+len(candidatePoints))
	if t := strings.TrimSpace(candidateTitle); t != "" {
		candidates = append(candidates, NormalizeForMatch(t))
	}
	for _, p := range candidatePoints {
		if t := strings.TrimSpace(p); t != "" {
			candidates = append(candidates, NormalizeForMatch(t))
		}
	}
	if len(candidates) == 0 {
		return nil
	}

	var best *CorrectionMatch
	for _, c := range corrections {
		corrTokens := NormalizeForMatch(strings.TrimSpace(c.Title))
		if len(corrTokens) == 0 {
			// Fall back to reason tokens when title was empty.
			corrTokens = NormalizeForMatch(strings.TrimSpace(c.Reason))
		}
		if len(corrTokens) == 0 {
			continue
		}
		for _, cand := range candidates {
			score := JaccardSimilarity(corrTokens, cand)
			if score >= CorrectionMatchThreshold {
				if best == nil || score > best.Score {
					match := c
					best = &CorrectionMatch{Correction: match, Candidate: joinTokens(cand), Score: score}
				}
			}
		}
	}
	return best
}

func joinTokens(tokens []string) string {
	return strings.Join(tokens, " ")
}
