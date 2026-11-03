package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"wails-gui/internal/engagement"
	"wails-gui/internal/handoff"
	"wails-gui/storage"
)

// Suggestions with evidence (docs/notifications-design.md section 8). The
// automation extractor already turns repeated work into proposed initiatives
// with their evidence; this producer picks at most one a week, tells the user
// about it, and shows it on Today with the evidence and a dismiss-with-reason.

const (
	// suggestionMinObservations is how many times the pattern must have been
	// seen before it is worth a suggestion.
	suggestionMinObservations = 3
	// suggestionMinDays is how many distinct local days the evidence must span.
	suggestionMinDays = 3
	// suggestionInterval is how often the producer looks for a candidate.
	suggestionInterval = time.Hour
	suggestionStakes   = 0.4
	// suggestionEvidenceMax bounds the evidence shown on the card.
	suggestionEvidenceMax = 4
	suggestionQuoteMax    = 160
	suggestionTitleMax    = 120
	// suggestedInitiativesKey remembers which initiatives were already
	// suggested, so the same one is never suggested twice.
	suggestedInitiativesKey  = "engagement_suggested_initiatives"
	suggestedInitiativesKeep = 100
)

// suggestionDays counts the distinct local days the evidence and the
// observations of an initiative cover.
func suggestionDays(item Initiative, observations []InitiativeObservation, loc *time.Location) int {
	if loc == nil {
		loc = time.Local
	}
	days := map[string]bool{}
	add := func(millis int64) {
		if millis > 0 {
			days[time.UnixMilli(millis).In(loc).Format("2006-01-02")] = true
		}
	}
	for _, evidence := range item.Evidence {
		add(evidence.OccurredAt)
	}
	for _, observation := range observations {
		add(observation.ObservedAt)
		for _, evidence := range observation.Evidence {
			add(evidence.OccurredAt)
		}
	}
	return len(days)
}

// suggestionQualifies reports whether an initiative is a proposed automation
// with enough repeated evidence to suggest.
func suggestionQualifies(item Initiative, days int) bool {
	return item.Status == InitiativeStatusProposed &&
		item.Kind == "automation" &&
		item.ExecutionPlan.Automation != nil &&
		strings.TrimSpace(item.ExecutionPlan.Automation.Prompt) != "" &&
		item.ObservationCount >= suggestionMinObservations &&
		days >= suggestionMinDays
}

// suggestedInitiativeIDs reads the ids already suggested, oldest first.
func (a *App) suggestedInitiativeIDs(ctx context.Context) []string {
	if a == nil || a.store == nil {
		return nil
	}
	raw, err := a.store.GetUIState(ctx, suggestedInitiativesKey)
	if err != nil || strings.TrimSpace(raw) == "" {
		return nil
	}
	var ids []string
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil
	}
	return ids
}

// suggestedInitiatives reads the ids already suggested as a set.
func (a *App) suggestedInitiatives(ctx context.Context) map[string]bool {
	out := map[string]bool{}
	for _, id := range a.suggestedInitiativeIDs(ctx) {
		out[id] = true
	}
	return out
}

// rememberSuggested records an initiative as suggested, keeping the newest
// ids in the order they were suggested.
func (a *App) rememberSuggested(ctx context.Context, initiativeID string) error {
	if a == nil || a.store == nil {
		return errors.New("store unavailable")
	}
	ids := append(a.suggestedInitiativeIDs(ctx), initiativeID)
	if len(ids) > suggestedInitiativesKeep {
		ids = ids[len(ids)-suggestedInitiativesKeep:]
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	return a.store.SetUIState(ctx, suggestedInitiativesKey, string(raw))
}

// pickSuggestion returns the highest-confidence qualified initiative that was
// not suggested before, with its evidence day count.
func (a *App) pickSuggestion(ctx context.Context, now time.Time) (Initiative, int, bool) {
	if a == nil || a.activity == nil || a.activity.store == nil {
		return Initiative{}, 0, false
	}
	items, err := a.activity.store.ListInitiativesByStatus(string(InitiativeStatusProposed), 50)
	if err != nil {
		log.Printf("[engagement] suggestion candidates: %v", err)
		return Initiative{}, 0, false
	}
	seen := a.suggestedInitiatives(ctx)
	sort.SliceStable(items, func(i, j int) bool { return items[i].Confidence > items[j].Confidence })
	for _, item := range items {
		if seen[item.ID] {
			continue
		}
		observations, _ := a.activity.store.ListInitiativeObservations(item.ID)
		days := suggestionDays(item, observations, now.Location())
		if suggestionQualifies(item, days) {
			return item, days, true
		}
	}
	return Initiative{}, 0, false
}

// checkSuggestionEngagement raises at most one suggestion moment per ISO
// week. The dedupe key names the week, so the pacing rules refuse a second
// one even if this runs again after a restart.
func (a *App) checkSuggestionEngagement(ctx context.Context, now time.Time) {
	if !a.engagementActive() || a.store == nil {
		return
	}
	weekKey := engagement.DedupeKey(engagement.KindSuggestion, engagement.WeekKey(now))
	if exists, err := a.store.EngagementMomentKeyExists(ctx, weekKey); err != nil || exists {
		return
	}
	item, _, ok := a.pickSuggestion(ctx, now)
	if !ok {
		return
	}
	title := engagementTitle(item.ExecutionPlan.Automation.Name)
	if title == "" {
		title = engagementTitle(item.Title)
	}
	if runes := []rune(title); len(runes) > suggestionTitleMax {
		title = strings.TrimSpace(string(runes[:suggestionTitleMax]))
	}
	// The moment expires when the ISO week ends (the coming Monday at 00:00).
	year, month, day := now.Date()
	weekday := int(now.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	weekEnd := time.Date(year, month, day, 0, 0, 0, 0, now.Location()).AddDate(0, 0, 8-weekday)
	if a.submitEngagementMoment("suggestion", engagement.Moment{
		Kind:      engagement.KindSuggestion,
		SubjectID: item.ID,
		DedupeKey: weekKey,
		Stakes:    suggestionStakes,
		ExpiresAt: weekEnd,
		Params:    map[string]string{"title": title, "count": fmt.Sprint(item.ObservationCount)},
	}) {
		if err := a.rememberSuggested(ctx, item.ID); err != nil {
			log.Printf("[engagement] remembering the suggestion: %v", err)
		}
	}
}

// TodaySuggestionView is the suggestion card on Today: the pattern, the
// evidence behind it, and what accepting it creates.
type TodaySuggestionView struct {
	Available bool   `json:"available"`
	ID        string `json:"id"`
	Title     string `json:"title"`
	// Description says what the schedule would do, in the extractor's words.
	Description string `json:"description"`
	// Times is how often the pattern was seen; Days over how many distinct days.
	Times int `json:"times"`
	Days  int `json:"days"`
	// Evidence lists the quotes or summaries the extractor kept, trimmed.
	Evidence []string `json:"evidence"`
	// Schedule is "repeat" when accepting creates an Upcoming entry on a
	// schedule, "once" when the plan has no schedule and accepting creates
	// one-time work.
	Schedule            string `json:"schedule"`
	ScheduleDescription string `json:"scheduleDescription"`
	SuggestedAt         string `json:"suggestedAt"`
}

// GetTodaySuggestion returns this week's suggestion while it is still open:
// the initiative is still proposed and the moment was not resolved.
func (a *App) GetTodaySuggestion() (perfValue TodaySuggestionView, perfErr error) {
	defer logPageBinding("GetTodaySuggestion", time.Now(), &perfValue)
	if a == nil || a.store == nil || a.activity == nil || a.activity.store == nil {
		return TodaySuggestionView{Evidence: []string{}}, nil
	}
	ctx := context.Background()
	moment, err := a.latestSuggestionMoment(ctx)
	if err != nil || moment == nil {
		return TodaySuggestionView{Evidence: []string{}}, err
	}
	item, err := a.activity.store.GetInitiative(moment.SubjectID)
	if err != nil || item.Status != InitiativeStatusProposed || item.ExecutionPlan.Automation == nil {
		return TodaySuggestionView{Evidence: []string{}}, nil
	}
	observations, _ := a.activity.store.ListInitiativeObservations(item.ID)
	plan := item.ExecutionPlan.Automation
	view := TodaySuggestionView{
		Available:   true,
		ID:          item.ID,
		Title:       engagementTitle(firstNonEmpty(plan.Name, item.Title)),
		Description: engagementTitle(firstNonEmpty(plan.Description, item.Description)),
		Times:       item.ObservationCount,
		Days:        suggestionDays(item, observations, time.Local),
		Evidence:    suggestionEvidenceLines(item, observations),
		Schedule:    "once",
		SuggestedAt: time.UnixMilli(moment.CreatedAt).UTC().Format(time.RFC3339),
	}
	if plan.ScheduleKind == "cron" {
		view.Schedule = "repeat"
		view.ScheduleDescription = engagementTitle(plan.ScheduleEvidence)
	}
	return view, nil
}

// latestSuggestionMoment finds the newest open or sent suggestion moment.
func (a *App) latestSuggestionMoment(ctx context.Context) (*storage.EngagementMomentRow, error) {
	var newest *storage.EngagementMomentRow
	for _, state := range []string{storage.EngagementStatePending, storage.EngagementStateScheduled, storage.EngagementStateSent} {
		rows, err := a.store.ListEngagementMoments(ctx, state, 0)
		if err != nil {
			return nil, err
		}
		for i := range rows {
			row := rows[i]
			if row.Kind != string(engagement.KindSuggestion) {
				continue
			}
			if newest == nil || row.CreatedAt > newest.CreatedAt {
				newest = &row
			}
		}
	}
	return newest, nil
}

// suggestionEvidenceLines renders the evidence the card shows: quotes first,
// then summaries, newest first, bounded.
func suggestionEvidenceLines(item Initiative, observations []InitiativeObservation) []string {
	type line struct {
		at   int64
		text string
	}
	var lines []line
	seen := map[string]bool{}
	add := func(evidence InitiativeEvidence) {
		text := strings.Join(strings.Fields(firstNonEmpty(evidence.Quote, evidence.Summary)), " ")
		if text == "" || seen[text] {
			return
		}
		if runes := []rune(text); len(runes) > suggestionQuoteMax {
			text = strings.TrimSpace(string(runes[:suggestionQuoteMax])) + "..."
		}
		seen[text] = true
		lines = append(lines, line{at: evidence.OccurredAt, text: text})
	}
	for _, evidence := range item.Evidence {
		add(evidence)
	}
	for _, observation := range observations {
		for _, evidence := range observation.Evidence {
			add(evidence)
		}
	}
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].at > lines[j].at })
	out := []string{}
	for _, l := range lines {
		if len(out) >= suggestionEvidenceMax {
			break
		}
		out = append(out, l.text)
	}
	return out
}

// TodaySuggestionDecision is what the user did with the card.
type TodaySuggestionDecision struct {
	ID string `json:"id"`
	// Decision is add_to_upcoming, do_once, not_now or dismiss.
	Decision string `json:"decision"`
	// Reason accompanies dismiss; it is stored as initiative feedback.
	Reason string `json:"reason"`
}

var errSuggestionDecision = errors.New("the decision must be add_to_upcoming, do_once, not_now or dismiss")

// ResolveTodaySuggestion acts on the card: add_to_upcoming creates the
// schedule through the existing approval path, do_once creates one-time work
// from the plan, not_now defers, dismiss closes it with the reason. The
// suggestion moment gets an acted receipt with the decision, so the weekly
// line and the kind's counts see it.
func (a *App) ResolveTodaySuggestion(input TodaySuggestionDecision) (TodaySuggestionView, error) {
	return a.resolveTodaySuggestionOn(input, engagement.ChannelDesktop)
}

func (a *App) resolveTodaySuggestionOn(input TodaySuggestionDecision, channel engagement.Channel) (TodaySuggestionView, error) {
	if a == nil || a.activity == nil || a.activity.store == nil {
		return TodaySuggestionView{}, errors.New("activity store is unavailable")
	}
	ctx := context.Background()
	decision := strings.ToLower(strings.TrimSpace(input.Decision))
	item, err := a.activity.store.GetInitiative(strings.TrimSpace(input.ID))
	if err != nil {
		return TodaySuggestionView{}, err
	}
	switch decision {
	case "add_to_upcoming":
		if _, err := a.ApproveInitiative(item.ID); err != nil {
			return TodaySuggestionView{}, err
		}
	case "do_once":
		if err := a.startOneTimeWorkFromInitiative(item); err != nil {
			return TodaySuggestionView{}, err
		}
	case "not_now":
		if _, err := a.ReviewInitiative(ReviewInitiativeInput{InitiativeID: item.ID, Decision: "not_now"}); err != nil {
			return TodaySuggestionView{}, err
		}
	case "dismiss":
		if _, err := a.ReviewInitiative(ReviewInitiativeInput{InitiativeID: item.ID, Decision: "wrong", Reason: strings.TrimSpace(input.Reason)}); err != nil {
			return TodaySuggestionView{}, err
		}
	default:
		return TodaySuggestionView{}, errSuggestionDecision
	}
	a.receiptSuggestion(ctx, item.ID, decision, channel)
	return a.GetTodaySuggestion()
}

// applySuggestionReceipt turns a decision made on the phone's notice (an acted
// receipt with the action id) into the same initiative change the Today card
// makes, so the card does not linger after the user already answered.
func (a *App) applySuggestionReceipt(ctx context.Context, momentID, state, action string) {
	if a == nil || a.store == nil || a.activity == nil || a.activity.store == nil || state != storage.EngagementReceiptActed {
		return
	}
	moment, err := a.store.GetEngagementMoment(ctx, momentID)
	if err != nil || moment == nil || moment.Kind != string(engagement.KindSuggestion) || moment.SubjectID == "" {
		return
	}
	var decision string
	switch action {
	case "not_now":
		decision = "not_now"
	case "dismiss":
		decision = "wrong"
	default:
		return
	}
	item, err := a.activity.store.GetInitiative(moment.SubjectID)
	if err != nil || item.Status != InitiativeStatusProposed {
		return
	}
	if _, err := a.ReviewInitiative(ReviewInitiativeInput{InitiativeID: item.ID, Decision: decision}); err != nil {
		log.Printf("[engagement] applying the phone's suggestion answer: %v", err)
	}
}

// receiptSuggestion records the interaction. Only completed linked work earns
// an acted receipt; accepting a suggestion merely starts or schedules it.
func (a *App) receiptSuggestion(ctx context.Context, initiativeID, decision string, channel engagement.Channel) {
	if a.engagement == nil || a.store == nil {
		return
	}
	moment, err := a.latestSuggestionMoment(ctx)
	if err != nil || moment == nil || moment.SubjectID != initiativeID {
		return
	}
	if moment.State != storage.EngagementStateSent {
		// The user acted from Today before the notice went out; the open
		// moment is retired so it is never delivered after the fact.
		_ = a.engagement.Retire(ctx, moment.DedupeKey, "handled on Today")
		return
	}
	state := storage.EngagementReceiptOpened
	if decision == "wrong" || decision == "dismiss" {
		state = storage.EngagementReceiptDismissed
	}
	if err := a.engagement.Receipt(ctx, moment.ID, string(channel), state, decision, time.Time{}); err != nil {
		log.Printf("[engagement] suggestion receipt: %v", err)
	}
}

// startOneTimeWorkFromInitiative turns the plan into one-time work and marks
// the initiative approved with the work as its execution.
func (a *App) startOneTimeWorkFromInitiative(item Initiative) error {
	if item.Status != InitiativeStatusProposed || item.ExecutionPlan.Automation == nil {
		return errors.New("this suggestion is no longer open")
	}
	if a.handoffStore == nil {
		return errors.New("one-time work is not available yet")
	}
	plan := item.ExecutionPlan.Automation
	now := time.Now().UnixMilli()
	id := "hf_" + uuid.NewString()
	entry := handoff.Entry{
		ID: id, Title: engagementTitle(firstNonEmpty(plan.Name, item.Title)), Status: handoff.StatusActive,
		Source: handoff.SourceTriage, SourceID: item.ID, Workdir: strings.TrimSpace(plan.ProjectDir),
		Body: strings.TrimSpace(plan.Prompt), CreatedAt: now, UpdatedAt: now,
	}
	saved, err := a.handoffStore.Save(entry)
	if err != nil {
		return err
	}
	// The approval carries the same judgment check as a schedule (confirmed
	// judgment or a qualified manifest); a refusal takes the work back.
	if _, err := a.activity.store.RecordInitiativeApproval(item.ID, "handoff:"+saved.ID); err != nil {
		_ = a.handoffStore.Delete(saved.ID)
		return err
	}
	a.emitBrowserEvent("handoff:updated", map[string]any{"id": saved.ID, "status": string(saved.Status)})
	a.emitBrowserEvent("handoff:created", map[string]any{"id": saved.ID, "title": saved.Title})
	return nil
}
