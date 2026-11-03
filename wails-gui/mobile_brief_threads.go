package main

import (
	"context"
	"sort"
	"strings"
)

// Continuations name the same concrete object across days, rather than a broad
// topic or person. Metadata selects its latest state before any text is read.
// A newer private, unfinished or unextracted continuation must never revive an
// older cart/draft claim. Root hashes are grouping identities, never authority.
type mobileBriefThreadState struct {
	id, parent, kind, state, acted string
	start, end                     int64
	version                        int
}

func mobileBriefThreadFamilies(rows []mobileBriefThreadState, asOf int64) map[string][]mobileBriefThreadState {
	byID := map[string]mobileBriefThreadState{}
	for _, row := range rows {
		if row.start <= asOf {
			byID[row.id] = row
		}
	}
	families := map[string][]mobileBriefThreadState{}
	for _, row := range byID {
		root := row.id
		seen := map[string]bool{}
		for len(seen) < 128 {
			if seen[root] {
				root = ""
				break
			}
			seen[root] = true
			ancestor, ok := byID[root]
			if !ok || ancestor.parent == "" {
				break
			}
			root = ancestor.parent
		}
		if root == "" || len(seen) >= 128 {
			continue
		}
		families[root] = append(families[root], row)
	}
	for root, family := range families {
		sort.SliceStable(family, func(i, j int) bool {
			if family[i].end != family[j].end {
				return family[i].end > family[j].end
			}
			if family[i].start != family[j].start {
				return family[i].start > family[j].start
			}
			return family[i].id < family[j].id
		})
		families[root] = family
	}
	return families
}

func collectMobileBriefThreadHeads(ctx context.Context, a *App, in *mobileBriefInput) {
	if in == nil {
		return
	}
	eligible := map[string]mobileBriefCandidate{}
	latestRows := map[string]bool{}
	// Recap runs first. Reconcile all action-producing aliases even when the
	// newest continuation is completed, unextracted, protected or unavailable.
	defer func() {
		filter := func(items []mobileBriefCandidate) []mobileBriefCandidate {
			var out []mobileBriefCandidate
			for _, item := range items {
				if item.Kind == mobileBriefThreadOutcomeSource && (threadOutcomeUnfinished(item.Outcome) || item.NextStep != "" || item.Status == "needs_review") {
					current, ok := eligible[mobileBriefSourceKey(item.SourceKind, item.SourceID)]
					if ok {
						item.LoopKey, item.LoopAliases = current.LoopKey, current.LoopAliases
					} else {
						id := ""
						if strings.HasPrefix(item.SourceID, threadOutcomeMomentPrefix) {
							if cut := strings.LastIndex(item.SourceID, ":"); cut > 0 {
								id = strings.TrimPrefix(item.SourceID[:cut], threadOutcomeMomentPrefix)
							}
						}
						// A latest settled decision can still have a legitimate next
						// step. It need not become an unfinished open loop to survive.
						current := latestRows[id] && a != nil && a.threadOutcomeSourceReadable(ctx, item.SourceID)
						if !current {
							if item.Outcome == nil || threadOutcomeUnfinished(item.Outcome) {
								continue
							}
							// Preserve the historical fact without replaying its old
							// proposed action or outcome evidence as current work.
							item.Status, item.NextStep, item.Outcome = "done", "", nil
						}
					}
				}
				out = append(out, item)
			}
			return out
		}
		in.Moved, in.Doing, in.OpenLoops = filter(in.Moved), filter(in.Doing), filter(in.OpenLoops)
	}()
	if a == nil || a.activity == nil || a.activity.store == nil || a.IsClosedEyesModeEnabled() {
		return
	}
	store := a.activity.store
	families, err := loadMobileBriefThreadFamilies(ctx, store, in.AsOf.UnixMilli())
	if err != nil {
		return
	}

	type head struct {
		root string
		row  mobileBriefThreadState
	}
	var heads []head
	for root, family := range families {
		row := family[0]
		latestRows[row.id] = true
		if row.end > in.AsOf.UnixMilli() || row.version != threadOutcomeVersion || row.kind == "assistant" && row.acted == "observed" {
			continue
		}
		switch row.state {
		case "cart_left", "checkout_started", "answered", "draft", "incomplete":
			heads = append(heads, head{root, row})
		}
	}
	sort.SliceStable(heads, func(i, j int) bool {
		if heads[i].row.end != heads[j].row.end {
			return heads[i].row.end < heads[j].row.end
		}
		return heads[i].row.id < heads[j].row.id
	})
	if len(heads) > 128 {
		heads = heads[:128]
	}
	for _, head := range heads {
		row, err := store.GetActivityByID(head.row.id)
		if err != nil || row == nil {
			continue
		}
		outcome := a.readableThreadOutcome(ctx, *row)
		if outcome == nil || !threadOutcomeUnfinished(outcome) || outcome.Kind == "assistant" && outcome.UserActed == "observed" {
			continue
		}
		item := mobileBriefCandidate{SourceKind: mobileBriefRecapSource, SourceID: threadOutcomeSourceID(row.ID, outcome), Title: outcome.Summary, Status: "needs_review", Kind: mobileBriefThreadOutcomeSource, At: mobileBriefMillis(row.EndMs), LastTouch: mobileBriefMillis(row.EndMs), Outcome: outcome, NextStep: outcome.NextStep, LoopKey: "thread-" + agencyPayloadHash([]byte(head.root))}
		// The thread was first seen when its earliest continuation started.
		for _, member := range families[head.root] {
			if opened := mobileBriefMillis(member.start); !opened.IsZero() && (item.OpenedAt.IsZero() || opened.Before(item.OpenedAt)) {
				item.OpenedAt = opened
			}
		}
		// Stored briefs name individual rows. Match any member
		// of the family for two-brief suppression without copying its text.
		item.LoopAliases = append(item.LoopAliases, mobileBriefLoopIdentity(mobileBriefRecapSource, threadOutcomeMomentPrefix+head.root+":identity"))
		for _, member := range families[head.root] {
			item.LoopAliases = append(item.LoopAliases, mobileBriefLoopIdentity(mobileBriefRecapSource, threadOutcomeMomentPrefix+member.id+":identity"))
		}
		eligible[mobileBriefSourceKey(item.SourceKind, item.SourceID)] = item
		in.OpenLoops = append(in.OpenLoops, item)
	}
}

func loadMobileBriefThreadFamilies(ctx context.Context, store *ActivityStore, asOf int64) (map[string][]mobileBriefThreadState, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT id,continues_activity_id,start_ms,end_ms,
 CASE WHEN json_valid(thread_outcome) THEN COALESCE(json_extract(thread_outcome,'$.outcome.kind'),'') ELSE '' END,
 CASE WHEN json_valid(thread_outcome) THEN COALESCE(json_extract(thread_outcome,'$.outcome.state'),'') ELSE '' END,
 CASE WHEN json_valid(thread_outcome) THEN COALESCE(json_extract(thread_outcome,'$.outcome.userActed'),'') ELSE '' END,
 CASE WHEN json_valid(thread_outcome) THEN COALESCE(json_extract(thread_outcome,'$.outcome.version'),0) ELSE 0 END
 FROM activities WHERE start_ms<=?`, asOf)
	if err != nil {
		return nil, err
	}
	var metadata []mobileBriefThreadState
	for rows.Next() {
		var row mobileBriefThreadState
		if err := rows.Scan(&row.id, &row.parent, &row.start, &row.end, &row.kind, &row.state, &row.acted, &row.version); err != nil {
			rows.Close()
			return nil, err
		}
		metadata = append(metadata, row)
	}
	readErr := rows.Err()
	rows.Close()
	if readErr != nil {
		return nil, readErr
	}
	return mobileBriefThreadFamilies(metadata, asOf), nil
}
