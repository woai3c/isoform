package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const activityThreadInterruptionMaxMs = int64(30 * time.Minute / time.Millisecond)

// ActivityThreadResumePoint is the safest specific identifier observed at a stretch's end.
type ActivityThreadResumePoint struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
}

// ActivityThreadStretch is one uninterrupted run of a thread's moments. Start and End are Unix milliseconds.
type ActivityThreadStretch struct {
	Start       int64                      `json:"start"`
	End         int64                      `json:"end"`
	ActiveMs    int64                      `json:"activeMs"`
	ActivityIDs []string                   `json:"activityIds"`
	ResumePoint *ActivityThreadResumePoint `json:"resumePoint"`
	// idleBefore marks a split inside one run of the thread's moments: no
	// other work came between, only time without input.
	idleBefore bool
}

// ActivityThreadGap separates two stretches. ThreadID is empty when an app or away time labels the gap.
type ActivityThreadGap struct {
	Start    int64  `json:"start"`
	End      int64  `json:"end"`
	Minutes  int    `json:"minutes"`
	Label    string `json:"label"`
	ThreadID string `json:"threadId"`
	// Idle is time without input inside the same work (activity review
	// v1.0.1): no other work or app filled it and the computer was neither
	// locked nor asleep. It splits the bar but is never an interruption.
	Idle bool `json:"idle"`
}

// ActivityThreadLinkReason describes the latest visible membership; rule keys keep their structured prefixes.
type ActivityThreadLinkReason struct {
	Kind                 string   `json:"kind"`
	Keys                 []string `json:"keys"`
	Text                 string   `json:"text"`
	ResumedWithinMinutes *int     `json:"resumedWithinMinutes,omitempty"`
}

// activityThreadStoredReason binds new model text to the complete readable day
// context. Purpose-derived inputs are withheld unless their provenance is proven. Old
// plain-text reasons remain loadable but cannot prove safe derived-text reads.
type activityThreadStoredReason struct {
	Version      int               `json:"version"`
	Text         string            `json:"text"`
	Context      string            `json:"context"`
	Titles       map[string]string `json:"titles,omitempty"`
	Aux          string            `json:"aux,omitempty"`
	Sources      string            `json:"sources,omitempty"`
	UnsafeInputs bool              `json:"unsafeInputs,omitempty"`
}

func (a *App) activityThreadReasonContext(ctx context.Context, rows []ActivityRow, batch ...*activityThreadDayEvidence) (string, error) {
	var evidence *activityThreadDayEvidence
	if len(batch) > 0 {
		evidence = batch[0]
	} else {
		var err error
		evidence, err = a.activityThreadDayEvidence(ctx, rows)
		if err != nil {
			return "", err
		}
	}
	moments := make([]threadMoment, 0, len(rows))
	sources := map[string]string{}
	for _, row := range rows {
		moments = append(moments, threadMomentFromRow(row))
		for _, id := range composeParseStrings(row.RunIDs) {
			run, ok := evidence.runs[id]
			if !ok || !evidence.allowed[id] {
				return "", nil
			}
			sources[id] = fmt.Sprintf("%s:%t", evidence.fingerprints[id], run.EvidenceExcluded)
		}
	}
	sort.Slice(moments, func(i, j int) bool { return moments[i].ID < moments[j].ID })
	encoded, err := json.Marshal(struct {
		Moments []threadMoment
		Sources map[string]string
	}{moments, sources})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(encoded)), nil
}

func activityThreadReasonTitleHashes(threads []activityThreadRow) map[string]string {
	hashes := map[string]string{}
	for _, thread := range threads {
		title := thread.UserTitle
		if title == "" {
			title = thread.Title
		}
		if title != "" {
			hashes[thread.ID] = fmt.Sprintf("%x", sha256.Sum256([]byte(title)))
		}
	}
	return hashes
}

// A legacy generated title with missing source basis cannot supply a safe
// explanation input. User titles are explicit inputs and checked by hash.
func activityThreadReasonTitlesReadable(threads []activityThreadRow, allowed map[string]ActivityRow) bool {
	for _, thread := range threads {
		if thread.UserTitle != "" || thread.Title == "" {
			continue
		}
		if len(thread.TitleBasis) == 0 || thread.ReasonBasis.Version != 2 || thread.ReasonBasis.Context == "" || thread.ReasonBasis.UnsafeInputs {
			return false
		}
		for _, id := range thread.TitleBasis {
			if _, ok := allowed[id]; !ok {
				return false
			}
		}
	}
	return true
}

func (a *App) currentActivityThreadReasonContext(ctx context.Context, day int64, rows []ActivityRow) (string, error) {
	return a.activityThreadReasonContext(ctx, rows)
}

type activityThreadTimedMoment struct {
	row      ActivityRow
	threadID string
}

func (a *App) addActivityThreadStretchDetails(ctx context.Context, views []ActivityThreadView, members []activityThreadMember, rows []ActivityRow, spent map[string]int64, threads []activityThreadRow, start, end int64) error {
	if len(views) == 0 {
		return nil
	}
	live := make(map[string]ActivityRow, len(rows))
	for _, row := range rows {
		live[row.ID] = row
	}
	evidence, err := a.activityThreadDayEvidence(ctx, rows)
	if err != nil {
		return err
	}
	aux, err := a.activityThreadReasonAux(ctx)
	if err != nil {
		return err
	}
	titleHashes := activityThreadReasonTitleHashes(threads)
	threadByID := map[string]activityThreadRow{}
	for _, thread := range threads {
		threadByID[thread.ID] = thread
	}
	reasonContext := ""
	if len(rows) > 0 {
		var err error
		reasonContext, err = a.activityThreadReasonContext(ctx, rows, evidence)
		if err != nil {
			return err
		}
	}
	validatedContexts := map[string]string{}
	for _, thread := range threads {
		basis := thread.ReasonBasis
		if basis.UnsafeInputs || basis.Version != 2 || basis.Context == "" || basis.Sources != reasonContext || basis.Aux != aux {
			continue
		}
		valid := true
		for id, hash := range basis.Titles {
			if titleHashes[id] != hash {
				valid = false
				break
			}
		}
		if valid {
			validatedContexts[thread.ID] = basis.Context
		}
	}
	memberByActivity := make(map[string]activityThreadMember, len(members))
	for _, member := range members {
		if member.ReasonContext != "" && member.ReasonContext == validatedContexts[member.ThreadID] {
			member.ReasonContext = reasonContext
		} else {
			member.ReasonContext = ""
		}
		memberByActivity[member.ActivityID] = member
	}
	viewIndex := make(map[string]int, len(views))
	titles := make(map[string]string, len(views))
	for i := range views {
		viewIndex[views[i].ID] = i
		titles[views[i].ID] = views[i].Title
	}
	stretches, err := a.activityThreadFocusStretches(ctx, views, live, spent, evidence, start, end)
	if err != nil {
		return err
	}

	outcomes := a.activityThreadDayOutcomes(ctx, rows, evidence)
	gapSources := activityThreadGapSources(rows, memberByActivity, viewIndex)
	broken, err := a.activityThreadAwayEvidence(ctx, start, end)
	if err != nil {
		return err
	}
	for i := range views {
		view := &views[i]
		view.ActiveMs = 0
		for _, id := range view.ActivityIDs {
			view.ActiveMs += spent[id]
		}
		activityThreadSetStretches(view, stretches[view.ID], live, evidence, gapSources, titles, broken)
		view.LinkReason = activityThreadLinkReason(*view, memberByActivity, reasonContext)
		view.Reviewed = threadByID[view.ID].UserConfirmed
		if len(view.ActivityIDs) > 0 {
			last := live[view.ActivityIDs[len(view.ActivityIDs)-1]]
			view.Unfinished = threadOutcomeUnfinished(outcomes.readable(last))
		}
	}

	for i := range views {
		if views[i].ActiveMs >= activityThreadInterruptionMaxMs {
			continue
		}
		for j := range views {
			if i == j {
				continue
			}
			for _, gap := range views[j].Gaps {
				if !gap.Idle && views[i].StartMs >= gap.Start && views[i].EndMs <= gap.End {
					views[i].Interruption = true
					break
				}
			}
			if views[i].Interruption {
				break
			}
		}
	}
	return nil
}

func activityThreadLinkReason(view ActivityThreadView, memberByActivity map[string]activityThreadMember, context ...string) ActivityThreadLinkReason {
	reason := ActivityThreadLinkReason{Keys: []string{}}
	for i := len(view.ActivityIDs) - 1; i >= 0; i-- {
		member, ok := memberByActivity[view.ActivityIDs[i]]
		if !ok {
			continue
		}
		switch member.Source {
		case threadSourceRule:
			reason.Kind = "rule"
		case threadSourceAI:
			reason.Kind = "model"
			if len(context) > 0 && context[0] != "" && member.ReasonContext == context[0] {
				reason.Text = redactThreadOutcomeText(member.Reason)
			}
		case threadSourceUser:
			reason.Kind = "user"
		default:
			continue
		}
		break
	}
	if reason.Kind == "rule" {
		seen := map[string]bool{}
		for _, id := range view.ActivityIDs {
			member := memberByActivity[id]
			if member.Source != threadSourceRule {
				continue
			}
			prefix, value, ok := strings.Cut(member.Reason, ":")
			if !ok || strings.TrimSpace(value) == "" {
				continue
			}
			switch prefix {
			case "obj":
				prefix = "file"
			case "pr", "person", "channel", "site":
			default:
				continue
			}
			key := prefix + ":" + value
			if redactThreadOutcomeText(key) != key {
				continue
			}
			if !seen[key] {
				reason.Keys = append(reason.Keys, key)
				seen[key] = true
			}
		}
	}
	for _, gap := range view.Gaps {
		if gap.Idle {
			continue
		}
		if reason.ResumedWithinMinutes == nil || gap.Minutes < *reason.ResumedWithinMinutes {
			minutes := gap.Minutes
			reason.ResumedWithinMinutes = &minutes
		}
	}
	return reason
}

type activityThreadInterval struct {
	start, end int64
}

type activityThreadAppInterval struct {
	activityThreadInterval
	weight float64
}

// Concurrent moments can describe the same foreground app. Use the maximum
// proportional occupancy in each segment instead of counting that time twice.
func activityThreadAppTimeMs(intervals []activityThreadAppInterval) int64 {
	edges := make([]int64, 0, len(intervals)*2)
	for _, interval := range intervals {
		edges = append(edges, interval.start, interval.end)
	}
	sort.Slice(edges, func(i, j int) bool { return edges[i] < edges[j] })
	var total float64
	for i := 1; i < len(edges); i++ {
		var weight float64
		for _, interval := range intervals {
			if interval.start <= edges[i-1] && interval.end >= edges[i] {
				weight = max(weight, interval.weight)
			}
		}
		total += float64(edges[i]-edges[i-1]) * weight
	}
	return int64(total)
}

func activityThreadIntervalMs(intervals []activityThreadInterval) int64 {
	if len(intervals) == 0 {
		return 0
	}
	sort.Slice(intervals, func(i, j int) bool {
		if intervals[i].start != intervals[j].start {
			return intervals[i].start < intervals[j].start
		}
		return intervals[i].end < intervals[j].end
	})
	var total int64
	current := intervals[0]
	for _, interval := range intervals[1:] {
		if interval.start > current.end {
			total += current.end - current.start
			current = interval
			continue
		}
		current.end = max(current.end, interval.end)
	}
	return total + current.end - current.start
}

type activityThreadGapOccupancy struct {
	threads map[string][]activityThreadInterval
	apps    map[string][]activityThreadAppInterval
}

func activityThreadGapSources(rows []ActivityRow, memberByActivity map[string]activityThreadMember, viewIndex map[string]int) activityThreadGapOccupancy {
	threadSpans := map[string][]activityThreadInterval{}
	appSpans := map[string][]activityThreadAppInterval{}
	for _, row := range rows {
		span := activityThreadInterval{start: row.StartMs, end: row.EndMs}
		if span.end <= span.start {
			continue
		}
		member := memberByActivity[row.ID]
		if _, ok := viewIndex[member.ThreadID]; ok && member.ThreadID != "" {
			threadSpans[member.ThreadID] = append(threadSpans[member.ThreadID], span)
			continue
		}
		apps := composeParseInt64Map(row.ActiveMsByApp)
		var total int64
		for _, active := range apps {
			total += max(int64(0), active)
		}
		// App counters have no timestamps inside a moment. Attribute the
		// clipped span proportionally rather than crediting every app with
		// the entire moment (which lets a minor app win alphabetically).
		for app, active := range apps {
			if active > 0 && total > 0 && strings.TrimSpace(app) != "" {
				appSpans[app] = append(appSpans[app], activityThreadAppInterval{activityThreadInterval: span, weight: float64(active) / float64(total)})
			}
		}
	}
	return activityThreadGapOccupancy{threads: threadSpans, apps: appSpans}
}

func activityThreadGapLabel(start, end int64, currentThreadID string, rows []ActivityRow, memberByActivity map[string]activityThreadMember, viewIndex map[string]int, titles map[string]string) (string, string) {
	return activityThreadGapSources(rows, memberByActivity, viewIndex).label(start, end, currentThreadID, titles)
}

func (sources activityThreadGapOccupancy) label(start, end int64, currentThreadID string, titles map[string]string) (string, string) {
	if end <= start {
		return "away", ""
	}
	threadSpans := map[string][]activityThreadInterval{}
	appSpans := map[string][]activityThreadAppInterval{}
	for id, spans := range sources.threads {
		if id == currentThreadID {
			continue
		}
		for _, span := range spans {
			clipped := activityThreadInterval{start: max(start, span.start), end: min(end, span.end)}
			if clipped.end > clipped.start {
				threadSpans[id] = append(threadSpans[id], clipped)
			}
		}
	}
	for app, spans := range sources.apps {
		for _, span := range spans {
			clipped := activityThreadInterval{start: max(start, span.start), end: min(end, span.end)}
			if clipped.end > clipped.start {
				appSpans[app] = append(appSpans[app], activityThreadAppInterval{activityThreadInterval: clipped, weight: span.weight})
			}
		}
	}
	threadIDs := make([]string, 0, len(threadSpans))
	for id := range threadSpans {
		threadIDs = append(threadIDs, id)
	}
	sort.Strings(threadIDs)
	bestMs := int64(0)
	label, labelThreadID := "", ""
	for _, id := range threadIDs {
		occupied := activityThreadIntervalMs(threadSpans[id])
		if occupied > bestMs {
			bestMs, label, labelThreadID = occupied, titles[id], id
		}
	}
	apps := make([]string, 0, len(appSpans))
	for app := range appSpans {
		apps = append(apps, app)
	}
	sort.Strings(apps)
	for _, app := range apps {
		occupied := activityThreadAppTimeMs(appSpans[app])
		if occupied > bestMs {
			bestMs, label, labelThreadID = occupied, app, ""
		}
	}
	if label == "" && labelThreadID == "" {
		return "away", ""
	}
	return label, labelThreadID
}

func (a *App) activityThreadResumePoint(ctx context.Context, row ActivityRow) (*ActivityThreadResumePoint, error) {
	if a == nil || a.activity == nil || a.activity.store == nil {
		return nil, nil
	}
	store := a.activity.store
	if len(a.filterComposerModelCandidates(store, []ActivityRow{row})) != 1 {
		return nil, nil
	}
	evidence, err := a.activityThreadDayEvidence(ctx, []ActivityRow{row})
	if err != nil {
		return nil, err
	}
	return activityThreadResumePointFromEvidence(row, evidence), nil
}

func activityThreadResumePointFromEvidence(row ActivityRow, evidence *activityThreadDayEvidence) *ActivityThreadResumePoint {
	priority := map[string]int{"pr": 0, "path": 1, "figure": 2, "repo": 3}
	var best *ActivityThreadResumePoint
	bestPriority, bestSeen := 0, int64(0)
	for _, id := range composeParseStrings(row.RunIDs) {
		if !evidence.allowed[id] {
			continue
		}
		for _, token := range evidence.runs[id].EvidenceTokens {
			rank, ok := priority[token.Kind]
			label := strings.TrimSpace(token.Value)
			if !ok || label == "" || redactThreadOutcomeText(label) != label {
				continue
			}
			clean := sanitizeActivityEvidenceTokens([]ActivityEvidenceToken{token}, false)
			if len(clean) != 1 || clean[0].Kind != token.Kind || clean[0].Value != label {
				continue
			}
			if best == nil || rank < bestPriority || rank == bestPriority && (token.LastSeenMs > bestSeen || token.LastSeenMs == bestSeen && label < best.Label) {
				best = &ActivityThreadResumePoint{Kind: token.Kind, Label: label}
				bestPriority, bestSeen = rank, token.LastSeenMs
			}
		}
	}
	return best
}

// ConfirmActivityThreadLink records user confirmation without changing model review or membership.
func (a *App) ConfirmActivityThreadLink(threadID string) error {
	if a == nil || a.activity == nil || a.activity.store == nil {
		return errors.New("activity store unavailable")
	}
	store := a.activity.store
	ctx := context.Background()
	activityThreadsMu.Lock()
	defer activityThreadsMu.Unlock()
	var day int64
	if err := store.reader().QueryRowContext(ctx, `SELECT day_start_ms FROM activity_threads WHERE id = ?`, threadID).Scan(&day); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("thread not found: %w", err)
		}
		return err
	}
	result, err := store.db.ExecContext(ctx, `UPDATE activity_threads SET user_confirmed = 1, updated_at = ? WHERE id = ? AND user_confirmed != 1`, time.Now().UnixMilli(), threadID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed > 0 {
		activityThreadCacheForget(day)
		a.emitBrowserEvent(activityThreadsUpdatedEvent)
	}
	return nil
}
