package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"wails-gui/internal/calendar"
	"wails-gui/internal/pii"
)

// TodayChatPrefill contains current source facts; the renderer supplies all
// user-facing templates through its locale, and keeps the draft editable.
type TodayChatPrefill struct {
	Kind            string   `json:"kind"`
	Title           string   `json:"title"`
	DurationMinutes int      `json:"durationMinutes,omitempty"`
	OccurredAt      int64    `json:"occurredAt,omitempty"`
	StartsAt        int64    `json:"startsAt,omitempty"`
	People          []string `json:"people,omitempty"`
	WaitingSince    int64    `json:"waitingSince,omitempty"`
}

type TodayChatDraft struct {
	TopicID     string                 `json:"topicId,omitempty"`
	Prefill     TodayChatPrefill       `json:"prefill"`
	Counts      TodayChatContextCounts `json:"counts"`
	Fingerprint string                 `json:"fingerprint"`
	// Message is a brief card's exact message; the client sends it as it is.
	Message string `json:"message,omitempty"`
}

// TodayChatDraftResult is one prepared Today action. Error is a stable code,
// never source text: "unavailable" when the item cannot be read now, "paused"
// while observation is paused, "failed" for any other failure such as a
// timeout.
type TodayChatDraftResult struct {
	Draft *TodayChatDraft `json:"draft,omitempty"`
	Error string          `json:"error,omitempty"`
}

const (
	// Today shows at most about forty chat actions: the brief, two offers,
	// three rows in each section and the day's summary.
	todayChatPrepareLimit       = 40
	todayChatPrepareBatchWindow = 20 * time.Second
)

// PrepareTodayChatDraft is a bounded read, independent of the active chat turn.
// Opening a card neither reserves a turn nor writes its sources into history.
func (a *App) PrepareTodayChatDraft(ref TodayChatContextRef) (TodayChatDraft, error) {
	ctx := context.Background()
	if a != nil && a.ctx != nil {
		ctx = a.ctx
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	item, sources, err := a.loadTodayChatContextItem(ctx, ref)
	if err != nil {
		return TodayChatDraft{}, err
	}
	return a.prepareLoadedTodayChatDraft(ctx, item, sources)
}

// PrepareTodayChatDrafts prepares every action Today shows before the user
// clicks one, reading the sources once for all of them. Like the single
// draft it writes nothing; Send repeats every read-time gate.
func (a *App) PrepareTodayChatDrafts(refs []TodayChatContextRef) []TodayChatDraftResult {
	results := make([]TodayChatDraftResult, len(refs))
	if a == nil || len(refs) == 0 {
		return results
	}
	ctx := context.Background()
	if a.ctx != nil {
		ctx = a.ctx
	}
	ctx, cancel := context.WithTimeout(ctx, todayChatPrepareBatchWindow)
	defer cancel()
	if a.IsClosedEyesModeEnabled() {
		// Observation is paused: read nothing, prepare nothing.
		for i := range results {
			results[i].Error = "paused"
		}
		return results
	}
	now := time.Now()
	sources := a.collectTodayChatSources(ctx, now)
	sources.deferCollectors = true
	for i, ref := range refs {
		if i >= todayChatPrepareLimit || ctx.Err() != nil {
			results[i].Error = "failed"
			continue
		}
		if !todayChatRefWithinBounds(ref) {
			results[i].Error = "unavailable"
			continue
		}
		a.addTodayChatActivityByID(ctx, &sources, ref)
		item, err := a.resolveTodayChatItem(ctx, ref, sources, now)
		if err == nil {
			var draft TodayChatDraft
			if draft, err = a.prepareLoadedTodayChatDraft(ctx, item, sources); err == nil {
				results[i].Draft = &draft
				continue
			}
		}
		results[i].Error = "failed"
		switch {
		case ctx.Err() != nil:
		case errors.Is(err, errTodayChatObservationPaused):
			results[i].Error = "paused"
		case errors.Is(err, errTodayChatItemUnavailable):
			results[i].Error = "unavailable"
		}
	}
	return results
}

func (a *App) prepareLoadedTodayChatDraft(ctx context.Context, item todayChatItem, sources todayChatSources) (TodayChatDraft, error) {
	result, err := a.buildLoadedTodayChatContext(ctx, item, sources)
	if err != nil {
		return TodayChatDraft{}, err
	}
	prefill := todayChatPreparedPrefill(item)
	return TodayChatDraft{Prefill: prefill, Counts: result.Counts, Fingerprint: todayChatPrefillFingerprint(prefill), Message: item.CardMessage, TopicID: item.topicID}, ctx.Err()
}

func todayChatPreparedPrefill(item todayChatItem) TodayChatPrefill {
	prefill := item.Prefill
	if prefill.Kind == "" {
		prefill.Kind = "generic"
	}
	prefill.Title = todayChatItemLabel(item)
	prefill.People = todayChatSafePeople(prefill.People)
	return prefill
}

// todayChatItemLabel is the label a generated draft shows. Isoform's own text
// about the user's item keeps its dates and years; a captured title keeps the
// full redaction, codes included.
func todayChatItemLabel(item todayChatItem) string {
	if item.OwnedLabel {
		return truncateAtRuneBoundary(todayChatOwnedText(item.Label), todayChatItemTokens)
	}
	return todayChatSafeLabel(item.Label)
}

// Only source-derived visible draft facts participate. Counts may change
// independently; neither this fingerprint nor any client text is evidence.
func todayChatPrefillFingerprint(prefill TodayChatPrefill) string {
	data, _ := json.Marshal(prefill)
	return agencyPayloadHash(data)
}

func todayChatSafeLabel(label string) string {
	label, _ = pii.RedactText(label)
	label = pii.RedactCodes(label)
	return truncateAtRuneBoundary(label, todayChatItemTokens)
}

func todayChatSafePeople(people []string) []string {
	var safe []string
	for _, person := range people {
		safe = append(safe, todayChatSafeLabel(person))
	}
	return safe
}

func todayChatSourcePrefill(kind string, record todayChatRecord, now time.Time) TodayChatPrefill {
	prefill := TodayChatPrefill{Kind: "generic"}
	if record.At > 0 && record.At <= now.UnixMilli() {
		prefill.OccurredAt = record.At
	}
	switch kind {
	case "activity", "summary", "summary_week":
		prefill.Kind = "activity"
		prefill.DurationMinutes = record.DurationMinutes
	case "meeting", mobileBriefCalendarSource:
		prefill.Kind = "meeting"
	case "purpose":
		prefill.Kind = "purpose"
	case "handoff", "history":
		prefill.Kind = "open_loop"
	case "deliverable", mobileBriefFileSource:
		prefill.Kind = "file"
	case "find", "news", mobileBriefWebSource:
		prefill.Kind = "news"
	}
	return prefill
}

func todayChatPurposePrefill(detail PurposeOpenLoopDetail) TodayChatPrefill {
	prefill := TodayChatPrefill{Kind: "purpose"}
	if detail.Current.LifecycleState == PurposeStateWaiting {
		prefill.Kind = "open_loop"
		prefill.WaitingSince = todayChatPurposeWaitingSince(detail)
	}
	return prefill
}

// OccurredAt on a revision is not when waiting began. Only a complete chain
// back to the transition into waiting can support a "waiting since" claim.
func todayChatPurposeWaitingSince(detail PurposeOpenLoopDetail) int64 {
	current := detail.Current
	if current.LifecycleState != PurposeStateWaiting {
		return 0
	}
	byID := map[string]PurposeOpenLoopEventRecord{}
	for _, event := range detail.Events {
		byID[event.ID] = event
	}
	seen := map[string]bool{current.ID: true}
	at := current.OccurredAt
	for current.PredecessorEventID != "" {
		previous, ok := byID[current.PredecessorEventID]
		if !ok || seen[previous.ID] || previous.OccurredAt > current.OccurredAt {
			return 0
		}
		seen[previous.ID] = true
		if previous.LifecycleState != PurposeStateWaiting {
			break
		}
		at, current = previous.OccurredAt, previous
	}
	return at
}

// The existing calendar collector already re-reads current event state. Read
// attendee fields from that same live row, never parse its rendered detail or
// recover them from a cached brief label.
func todayChatCalendarPrefill(event calendar.Event, now time.Time) TodayChatPrefill {
	prefill := todayChatSourcePrefill(mobileBriefCalendarSource, todayChatRecord{At: event.Start.UnixMilli()}, now)
	if event.Start.After(now) && !event.AllDay {
		prefill.StartsAt = event.Start.UnixMilli()
	}
	for _, attendee := range event.Attendees {
		if attendee.IsMe || attendee.Status == calendar.StatusDeclined {
			continue
		}
		if name := strings.TrimSpace(attendee.Name); name != "" {
			prefill.People = append(prefill.People, name)
			if len(prefill.People) == mobileBriefAttendeeLimit {
				break
			}
		}
	}
	return prefill
}
