package main

import (
	"context"
	"sort"
	"strconv"
	"time"
)

// morningBriefReadableHistory selects at most three earlier, source-proven
// briefs. Its result is suitable only for immediate use inside a collection
// fence: a caller must also bind and recheck every prior brief's full model
// inputs before passing this data to a model or storing its output.
func (a *App) morningBriefReadableHistory(ctx context.Context, briefs []MobileBriefV1, cutoff time.Time) []MobileBriefV1 {
	if a == nil || a.IsClosedEyesModeEnabled() || ctx.Err() != nil || cutoff.IsZero() {
		return nil
	}
	sources := a.collectTodayChatSources(ctx, time.Now())
	ordered := append([]MobileBriefV1(nil), briefs...)
	sort.SliceStable(ordered, func(i, j int) bool { return mobileBriefAsOfTime(ordered[i]).After(mobileBriefAsOfTime(ordered[j])) })
	var history []MobileBriefV1
	seen := map[string]bool{}
	considered := 0
	for _, brief := range ordered {
		at := mobileBriefAsOfTime(brief)
		if brief.BriefID == "" || seen[brief.BriefID] || at.IsZero() || !at.Before(cutoff) {
			continue
		}
		if considered == 3 {
			break
		}
		seen[brief.BriefID] = true
		considered++
		readable, withheld := a.mobileBriefDelivery(brief)
		if withheld.Reason != "" || len(readable.Narrative) == 0 || !a.todayChatBriefFrozen(ctx, readable, sources) {
			continue
		}
		// Legacy section rows were never frozen as exact model inputs, even
		// when they cite a readable source. Only the ranked manifest records
		// the original candidate facts needed for a source-backed diff.
		bySource, used := map[string]string{}, map[string]bool{}
		for _, ref := range readable.Evidence {
			bySource[mobileBriefSourceKey(ref.SourceKind, ref.SourceID)] = ref.Ref
			used[ref.Ref] = true
		}
		var rows []MobileBriefItem
		if readable.rankedEdition && readable.modelBinding != nil {
			for _, input := range readable.modelBinding.RankedInputs {
				key := mobileBriefSourceKey(input.Kind, input.ID)
				ref := bySource[key]
				if ref == "" {
					n := len(readable.Evidence) + 1
					ref = "ranked-history-" + strconv.Itoa(n)
					for used[ref] {
						n++
						ref = "ranked-history-" + strconv.Itoa(n)
					}
					readable.Evidence = append(readable.Evidence, MobileBriefRef{Ref: ref, SourceKind: input.Kind, SourceID: input.ID})
					bySource[key], used[ref] = ref, true
				}
				item := MobileBriefItem{Title: input.Candidate.Title, Status: input.Candidate.Status,
					LastTouch: input.Candidate.LastTouch, DueAt: input.Candidate.Due, Refs: []string{ref}}
				switch input.Kind {
				case mobileBriefCalendarSource:
					item.Kind, item.At, item.DueAt = "meeting", input.Candidate.Due, ""
				case mobileBriefWaitingReplySource:
					item.Kind = "waiting_on_reply"
				case "history":
					item.Kind = "one_time_work"
				}
				rows = append(rows, item)
			}
		}
		readable.Sections = MobileBriefSections{OpenLoops: rows}
		history = append(history, readable)
		if ctx.Err() != nil {
			break
		}
	}
	if ctx.Err() != nil {
		return nil
	}
	return history
}

// A prior prose proof stores no previous text. The exact stored brief and all
// its original model inputs are reopened through the existing privacy gates.
type morningBriefPriorProof struct {
	BriefID   string `json:"briefId"`
	Synthesis string `json:"synthesis"`
}

func (a *App) morningBriefPriorProofs(ctx context.Context, store *mobileBriefStore, history []MobileBriefV1, cutoff time.Time) ([]morningBriefPriorProof, bool) {
	if a == nil || store == nil || a.IsClosedEyesModeEnabled() || ctx.Err() != nil || cutoff.IsZero() || len(history) > 3 {
		return nil, false
	}
	proofs := make([]morningBriefPriorProof, 0, len(history))
	for _, brief := range history {
		if brief.modelBinding == nil || brief.modelBinding.Synthesis == "" || mobileBriefSynthesisDigest(brief) != brief.modelBinding.Synthesis {
			return nil, false
		}
		proofs = append(proofs, morningBriefPriorProof{BriefID: brief.BriefID, Synthesis: brief.modelBinding.Synthesis})
	}
	if !a.morningBriefPriorProofsReadable(ctx, store, cutoff, proofs) {
		return nil, false
	}
	return proofs, true
}

func (a *App) morningBriefPriorProofsReadable(ctx context.Context, store *mobileBriefStore, cutoff time.Time, proofs []morningBriefPriorProof) bool {
	if a == nil || store == nil || a.IsClosedEyesModeEnabled() || ctx.Err() != nil || cutoff.IsZero() || len(proofs) > 3 {
		return false
	}
	allowed := morningBriefLastThreeIDs(store, cutoff)
	sources := a.collectTodayChatSources(ctx, time.Now())
	seen := map[string]bool{}
	var previous time.Time
	for _, proof := range proofs {
		if proof.BriefID == "" || proof.Synthesis == "" || seen[proof.BriefID] || !allowed[proof.BriefID] {
			return false
		}
		seen[proof.BriefID] = true
		brief, found := store.load(proof.BriefID)
		at := mobileBriefAsOfTime(brief)
		if !found || at.IsZero() || !at.Before(cutoff) || !previous.IsZero() && at.After(previous) || brief.modelBinding == nil || brief.modelBinding.Synthesis != proof.Synthesis {
			return false
		}
		readable, withheld := a.mobileBriefDelivery(brief)
		if withheld.Reason != "" || len(readable.Narrative) == 0 || mobileBriefSynthesisDigest(readable) != proof.Synthesis || !a.todayChatBriefFrozen(ctx, readable, sources) {
			return false
		}
		previous = at
	}
	return ctx.Err() == nil
}
