package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
	"wails-gui/internal/engagement"
	"wails-gui/internal/openanswer"
	"wails-gui/internal/persistence"
	"wails-gui/storage"
)

const (
	openAnswerSurfaceKind = "open_answer"
	openAnswerJobPrefix   = "Open answer check "
	openAnswerCheckPeriod = 10 * time.Minute
)

var openAnswerAfterFunc = time.AfterFunc

type openAnswerCheckResult struct {
	Settled bool   `json:"settled"`
	Answer  string `json:"answer"`
	Value   string `json:"value"`
	Source  string `json:"source"`
}

func openAnswerJobName(id string) string { return openAnswerJobPrefix + id }

func openAnswerIDFromJobName(name string) string {
	return strings.TrimPrefix(name, openAnswerJobPrefix)
}

func parseOpenAnswerCheckResult(raw string) (openAnswerCheckResult, bool) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	var result openAnswerCheckResult
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return openAnswerCheckResult{}, false
	}
	seen := map[string]bool{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		key, ok := keyToken.(string)
		if err != nil || !ok || seen[key] {
			return openAnswerCheckResult{}, false
		}
		seen[key] = true
		switch key {
		case "settled":
			err = decoder.Decode(&result.Settled)
		case "answer":
			err = decoder.Decode(&result.Answer)
		case "value":
			err = decoder.Decode(&result.Value)
		case "source":
			err = decoder.Decode(&result.Source)
		default:
			return openAnswerCheckResult{}, false
		}
		if err != nil {
			return openAnswerCheckResult{}, false
		}
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return openAnswerCheckResult{}, false
	}
	if !seen["settled"] || !seen["answer"] || !seen["value"] || !seen["source"] {
		return openAnswerCheckResult{}, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return openAnswerCheckResult{}, false
	}
	return result, true
}

func openAnswerCheckPrompt(answer storage.OpenAnswerRow, language string) string {
	start := time.UnixMilli(answer.WindowStart).In(time.Local)
	end := time.UnixMilli(answer.WindowEnd).In(time.Local)
	lastValue := strings.TrimSpace(answer.LastValue)
	if lastValue == "" {
		lastValue = "none"
	}
	return fmt.Sprintf(`<open-answer-check>
You are checking once whether an answer the user asked for has settled. Do not mention this check, ask questions, or take any other action.
Question: %s
Topic: %s
Expected between %s and %s (%s).
Last known value: %s
Look it up now with the research tools. Reply with only one JSON object and nothing else:
{"settled": true or false, "answer": "one or two sentences in %s, the answer first", "value": "the current number or state in a few words, or empty", "source": "the name of the source you used"}
"settled" is true only when the result or status is final. Never guess: if you cannot find it, use "settled": false with empty "answer" and "value".
</open-answer-check>`,
		answer.Question, answer.Topic, start.Format("Jan 2, 2006 3:04 PM"), end.Format("Jan 2, 2006 3:04 PM"),
		time.Local.String(), lastValue, language)
}

func (a *App) ensureOpenAnswerCheck(answer storage.OpenAnswerRow, excludedJobID string) error {
	return a.ensureOpenAnswerCheckAfter(answer, excludedJobID, time.Time{})
}

func (a *App) ensureOpenAnswerCheckAfter(answer storage.OpenAnswerRow, excludedJobID string, notBefore time.Time) error {
	if a == nil || a.store == nil || answer.State != "open" || answer.WindowStart <= 0 || answer.HeldTransition != "" {
		return nil
	}
	now := time.Now()
	next := openanswer.NextCheck(time.UnixMilli(answer.WindowStart), time.UnixMilli(answer.ExpiresAt),
		time.UnixMilli(answer.LastCheckAt), now)
	if !next.IsZero() && !notBefore.IsZero() && next.Before(notBefore) {
		if !notBefore.Before(time.UnixMilli(answer.ExpiresAt)) {
			next = time.Time{}
		} else {
			next = notBefore
		}
	}
	if next.IsZero() {
		return a.postOpenAnswer(answer.ID, "gave_up", a.openAnswerGaveUpCopy(answer))
	}
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()
	jobs, err := a.store.ListCronJobs(ctx)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		if job.ID == excludedJobID {
			continue
		}
		if job.Name == openAnswerJobName(answer.ID) && job.SurfaceKind == openAnswerSurfaceKind && job.Enabled {
			return nil
		}
	}
	if a.cronScheduler == nil {
		return errors.New("cron scheduler is unavailable")
	}
	schedule, _ := json.Marshal(struct {
		AtMs int64 `json:"at_ms"`
	}{next.UnixMilli()})
	target, _ := json.Marshal(cronTargetConfig{
		Prompt: "open-answer-check",
		Model:  a.lightTaskModel(),
	})
	_, err = a.cronScheduler.AddJob(ctx, CronJobInput{
		Name: openAnswerJobName(answer.ID), Description: "A one-time check for an answer you asked for.",
		ScheduleKind: "at", ScheduleConfig: schedule, TargetKind: "agent", TargetConfig: target,
		DeleteAfterRun: true, SourceSessionID: answer.ChatSessionID, SurfaceKind: openAnswerSurfaceKind,
		RetryPolicy: json.RawMessage(`{}`),
	})
	return err
}

func (a *App) recoverOpenAnswerChecks() {
	if a == nil || a.store == nil {
		return
	}
	ctx := context.Background()
	now := time.Now()
	a.releaseOpenAnswerHolds(ctx, now, false)
	a.learnUnopenedOpenAnswerKinds(ctx, "", now)
	answers, err := a.store.ListOpenAnswers(ctx)
	if err != nil {
		return
	}
	for _, answer := range answers {
		if answer.HeldTransition != "" {
			continue
		}
		if strings.EqualFold(answer.Source, "Mail") && answer.WindowStart == 0 {
			if now.UnixMilli() >= answer.ExpiresAt {
				_ = a.postOpenAnswer(answer.ID, "gave_up", a.openAnswerGaveUpCopy(answer))
			}
			continue
		}
		if answer.WindowStart > 0 {
			_ = a.ensureOpenAnswerCheck(answer, "")
		}
	}
}

func (a *App) deferOpenAnswerCheck(job *storage.CronJobRow, reason string) {
	if a == nil || a.store == nil || job == nil {
		return
	}
	id := openAnswerIDFromJobName(job.Name)
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()
	answer, err := a.store.GetOpenAnswer(ctx, id)
	if err != nil {
		log.Printf("[open-answer] skipped check could not load record: job=%s err=%v", job.ID, err)
		return
	}
	log.Printf("[open-answer] check deferred: job=%s reason=%s", job.ID, reason)
	if err := a.ensureOpenAnswerCheckAfter(answer, job.ID, time.Now().Add(openAnswerCheckPeriod)); err != nil {
		log.Printf("[open-answer] failed to reschedule deferred check: job=%s err=%v", job.ID, err)
	}
}

func (a *App) scheduleNextOpenAnswerCheck(ctx context.Context, id, runningJobID string) {
	fresh, err := a.store.GetOpenAnswer(ctx, id)
	if err != nil {
		log.Printf("[open-answer] failed to reload check: record=%s err=%v", id, err)
		return
	}
	if err := a.ensureOpenAnswerCheck(fresh, runningJobID); err != nil {
		log.Printf("[open-answer] failed to schedule next check: record=%s err=%v", id, err)
	}
}

func (a *App) deleteOpenAnswerCheck(id string) {
	if a == nil || a.store == nil {
		return
	}
	jobs, err := a.store.ListCronJobs(context.Background())
	if err != nil {
		return
	}
	for _, job := range jobs {
		if job.Name == openAnswerJobName(id) && job.SurfaceKind == openAnswerSurfaceKind {
			_ = a.DeleteCronJob(job.ID)
		}
	}
}

func (a *App) openAnswerQuietWindow() engagement.QuietWindow {
	if a == nil {
		return engagement.QuietWindow{}
	}
	if a.engagement != nil {
		return a.engagement.Prefs().Quiet
	}
	if a.store == nil {
		return engagement.QuietWindow{}
	}
	prefs := newEngagementService(a.store, nil, engagementDelivery{})
	ctx, cancel := context.WithTimeout(context.Background(), dbTimeout)
	defer cancel()
	_ = prefs.load(ctx)
	return prefs.Prefs().Quiet
}

func openAnswerWindowStartsDuringQuiet(answer storage.OpenAnswerRow, quiet engagement.QuietWindow, now time.Time) bool {
	return answer.WindowStart > 0 && quiet.Contains(time.UnixMilli(answer.WindowStart).In(now.Location()))
}

func (a *App) scheduleOpenAnswerHoldRelease(at time.Time) {
	delay := time.Until(at)
	if delay < 0 {
		delay = 0
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	time.AfterFunc(delay, func() {
		if ctx.Err() == nil {
			a.releaseOpenAnswerHolds(ctx, time.Now(), false)
		}
	})
}

func (a *App) releaseOpenAnswerHolds(ctx context.Context, now time.Time, atBriefStart bool) {
	if a == nil || a.store == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	answers, err := a.store.ListOpenAnswers(ctx)
	if err != nil {
		log.Printf("[open-answer] could not load quiet-hour holds: %v", err)
		return
	}
	quiet := a.openAnswerQuietWindow()
	for _, answer := range answers {
		if err := ctx.Err(); err != nil {
			return
		}
		if answer.HeldTransition == "" {
			continue
		}
		if !atBriefStart && quiet.Contains(now) && !openAnswerWindowStartsDuringQuiet(answer, quiet, now) {
			a.scheduleOpenAnswerHoldRelease(quiet.EndAfter(now))
			continue
		}
		if a.IsClosedEyesModeEnabled() ||
			!openAnswerCandidateTextAllowed(openanswer.Candidate{
				Question: answer.Question, Topic: answer.Topic, Source: answer.Source, WatchedEvent: answer.WatchedEvent,
			}) ||
			!openAnswerCandidateTextAllowed(openanswer.Candidate{
				Question: answer.HeldText, Topic: answer.Topic, Source: answer.Source,
			}) {
			continue
		}
		muted, err := a.store.OpenAnswerKindMuted(ctx, answer.Topic)
		if err != nil || muted {
			continue
		}
		if err := a.postOpenAnswerTransition(ctx, answer.ID, answer.HeldTransition, answer.HeldText, now, true); err != nil {
			log.Printf("[open-answer] could not release quiet-hour hold: record=%s err=%v", answer.ID, err)
		}
	}
}

func (a *App) mobileBriefScheduleDisabled() bool {
	source := a.mobileBriefSource()
	if source == nil {
		source = a.ensureMobileBriefService()
	}
	if source == nil {
		return true
	}
	if concrete, ok := source.(*mobileBriefService); ok && concrete.config != nil {
		return !concrete.config.load().Enabled
	}
	schedule, err := source.Schedule()
	return err == nil && !schedule.Enabled
}

func (a *App) openAnswerGaveUpCopy(answer storage.OpenAnswerRow) string {
	event := decodeOpenAnswerEvent(answer.WatchedEvent)
	if a.currentUILanguageName() == "Simplified Chinese" {
		if answer.WindowStart > 0 {
			return "没找到结果，我先不查了。"
		}
		return fmt.Sprintf("%s 一周都没回复，我先不查了。", event.Who)
	}
	if answer.WindowStart > 0 {
		return "I couldn't find an answer, so I've stopped checking."
	}
	return fmt.Sprintf("%s hasn't replied in a week, so I've stopped checking.", event.Who)
}

func (a *App) checkOpenAnswerMail(now time.Time) {
	if a == nil || a.store == nil || a.mailSync == nil {
		return
	}
	answers, err := a.store.ListOpenAnswers(context.Background())
	if err != nil {
		return
	}
	for _, answer := range answers {
		if answer.HeldTransition != "" || !strings.EqualFold(answer.Source, "Mail") || answer.WindowStart > 0 {
			continue
		}
		if now.UnixMilli() >= answer.ExpiresAt {
			_ = a.postOpenAnswer(answer.ID, "gave_up", a.openAnswerGaveUpCopy(answer))
			continue
		}
		snapshot, err := a.mailSync.openAnswerSnapshot(context.Background(), now)
		if err != nil {
			continue
		}
		binding, err := snapshot.bindReply(answer.MailAnchorMessageID, answer.MailCorrespondent, now)
		if err != nil || binding.reply == nil {
			continue
		}
		event := decodeOpenAnswerEvent(answer.WatchedEvent)
		text := openAnswerMailReplyPostCopy(event, time.UnixMilli(binding.reply.At), a.currentUILanguageName())
		if openAnswerCandidateTextAllowed(openanswer.Candidate{Question: text, Topic: answer.Topic, Source: answer.Source}) {
			_ = a.postOpenAnswer(answer.ID, "settled", text)
		}
	}
}

func (a *App) completeOpenAnswerCheck(id, raw string, now time.Time, runningJobID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	answer, err := a.store.GetOpenAnswer(ctx, id)
	if err != nil || answer.State != "open" || answer.HeldTransition != "" {
		return
	}
	muted, err := a.store.OpenAnswerKindMuted(ctx, answer.Topic)
	if err != nil {
		return
	}
	if muted {
		_ = a.store.CancelOpenAnswer(ctx, id)
		a.deleteOpenAnswerCheck(id)
		a.emitBrowserEvent("open-answers:changed")
		return
	}
	result, valid := parseOpenAnswerCheckResult(strings.TrimSpace(stripResearchMeta(raw)))
	if !valid || a.IsClosedEyesModeEnabled() {
		result = openAnswerCheckResult{}
	}
	if result.Settled && (strings.TrimSpace(result.Answer) == "" || strings.TrimSpace(result.Source) == "") {
		result = openAnswerCheckResult{}
	}
	allowed := func(value string) bool {
		return openAnswerCandidateTextAllowed(openanswer.Candidate{Question: value, Topic: value, Source: answer.Source})
	}
	if !allowed(result.Answer) || !allowed(result.Value) || !allowed(result.Source) {
		result = openAnswerCheckResult{}
	}
	lastValue := answer.LastValue
	if strings.TrimSpace(result.Value) != "" {
		lastValue = strings.TrimSpace(result.Value)
	}
	if err := a.store.RecordOpenAnswerCheck(ctx, id, lastValue, now.UnixMilli()); err != nil {
		return
	}
	decision := openanswer.Decide(openanswer.Record{
		State: openanswer.State(answer.State), Posts: answer.Posts,
		WindowStart: time.UnixMilli(answer.WindowStart), ExpiresAt: time.UnixMilli(answer.ExpiresAt),
		Baseline: answer.LastValue, InterimPosted: answer.Posts > 0,
	}, openanswer.Observation{Settled: result.Settled, NewValue: strings.TrimSpace(result.Value)}, now)
	switch decision {
	case openanswer.PostSettled:
		if a.markOpenAnswerSawIt(ctx, answer, result, now) {
			return
		}
		if strings.TrimSpace(result.Answer) != "" {
			_ = a.postOpenAnswer(id, "settled", strings.TrimSpace(result.Answer))
			return
		}
	case openanswer.PostInterim:
		text := strings.TrimSpace(result.Answer)
		if text == "" {
			text = strings.TrimSpace(result.Value)
		}
		if text != "" {
			_ = a.postOpenAnswer(id, "interim", text)
			a.scheduleNextOpenAnswerCheck(ctx, id, runningJobID)
			return
		}
	case openanswer.PostGaveUp:
		_ = a.postOpenAnswer(id, "gave_up", a.openAnswerGaveUpCopy(answer))
		return
	}
	a.scheduleNextOpenAnswerCheck(ctx, id, runningJobID)
}

func normalizeOpenAnswerObservationText(text string) string {
	return cases.Fold().String(strings.Join(strings.Fields(norm.NFKC.String(text)), " "))
}

func openAnswerValueBounded(text, value string) bool {
	textRunes, valueRunes := []rune(text), []rune(value)
	if len(valueRunes) == 0 || len(valueRunes) > len(textRunes) {
		return false
	}
	for start := 0; start+len(valueRunes) <= len(textRunes); start++ {
		matches := true
		for index, valueRune := range valueRunes {
			if textRunes[start+index] != valueRune {
				matches = false
				break
			}
		}
		if !matches {
			continue
		}
		end := start + len(valueRunes)
		leftContinues := start > 0 && openAnswerValueNeighborContinuesWord(textRunes, start-1, -1, valueRunes[0])
		rightContinues := end < len(textRunes) && openAnswerValueNeighborContinuesWord(textRunes, end, 1, valueRunes[len(valueRunes)-1])
		if !leftContinues && !rightContinues {
			return true
		}
	}
	return false
}

func openAnswerValueNeighborContinuesWord(text []rune, neighborIndex, direction int, edge rune) bool {
	neighbor := text[neighborIndex]
	if openAnswerValueWordRune(neighbor) && openAnswerValueWordRune(edge) {
		return true
	}
	if unicode.IsDigit(edge) && (neighbor == '.' || neighbor == ',') {
		outward := neighborIndex + direction
		return outward >= 0 && outward < len(text) && unicode.IsDigit(text[outward])
	}
	return false
}

func openAnswerValueWordRune(r rune) bool {
	return unicode.IsDigit(r) ||
		unicode.IsLetter(r) && !unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

func openAnswerFrameAt(frame OCRFrameRow, snapshots []ActivitySnapshotRow, segments []ActivitySegmentRow) (int64, bool) {
	for _, snapshot := range snapshots {
		if snapshot.ID == frame.SegmentID {
			return snapshot.Timestamp, snapshot.Timestamp > 0
		}
	}
	for _, segment := range segments {
		if segment.ID == frame.SegmentID {
			if frame.FrameTime < 0 || segment.EndTime > segment.StartTime &&
				frame.FrameTime*1000 > float64(segment.EndTime-segment.StartTime) {
				return 0, false
			}
			at := segment.StartTime + int64(frame.FrameTime*1000)
			return at, at > 0
		}
	}
	return 0, false
}

func (a *App) openAnswerSawItAt(ctx context.Context, answer storage.OpenAnswerRow, value string, now time.Time) (int64, bool) {
	if a == nil || a.IsClosedEyesModeEnabled() || a.activity == nil || a.activity.store == nil ||
		answer.WindowStart <= 0 || strings.EqualFold(answer.Source, "Mail") ||
		utf8.RuneCountInString(strings.TrimSpace(value)) < 3 {
		return 0, false
	}
	start := max(answer.CreatedAt, answer.WindowStart)
	end := now.UnixMilli()
	if start <= 0 || end < start {
		return 0, false
	}
	store := a.activity.store
	sessions, err := store.modelSessionsOverlappingContext(ctx, start, end)
	if err != nil {
		return 0, false
	}
	sessions = a.filterActivityModelSessions(store, sessions)
	normalizedTopic := normalizeOpenAnswerObservationText(answer.Topic)
	if normalizedTopic == "" {
		return 0, false
	}
	normalizedValue := normalizeOpenAnswerObservationText(value)
	if normalizedValue == "" {
		return 0, false
	}
	var earliest int64
	for _, session := range sessions {
		if a.IsClosedEyesModeEnabled() {
			return 0, false
		}
		frames, err := store.GetOCRMatchFrames(session.ID, strings.TrimSpace(value))
		if err != nil || len(frames) == 0 {
			continue
		}
		snapshots, err := store.GetSessionSnapshots(session.ID)
		if err != nil {
			continue
		}
		segments, err := store.GetSessionSegments(session.ID)
		if err != nil {
			continue
		}
		for _, frame := range frames {
			at, ok := openAnswerFrameAt(frame, snapshots, segments)
			normalizedText := normalizeOpenAnswerObservationText(frame.Text)
			if !ok || at < start || at > end ||
				!strings.Contains(normalizedText, normalizedTopic) ||
				!openAnswerValueBounded(normalizedText, normalizedValue) {
				continue
			}
			if earliest == 0 || at < earliest {
				earliest = at
			}
		}
	}
	return earliest, earliest != 0 && !a.IsClosedEyesModeEnabled()
}

func (a *App) markOpenAnswerSawIt(ctx context.Context, answer storage.OpenAnswerRow, result openAnswerCheckResult, now time.Time) bool {
	at, matched := a.openAnswerSawItAt(ctx, answer, strings.TrimSpace(result.Value), now)
	if !matched || a.IsClosedEyesModeEnabled() ||
		!openAnswerCandidateTextAllowed(openanswer.Candidate{Question: answer.Question, Topic: answer.Topic, Source: answer.Source}) {
		return false
	}
	muted, err := a.store.OpenAnswerKindMuted(ctx, answer.Topic)
	if err != nil || muted {
		return false
	}
	if err := a.store.CommitOpenAnswerTransition(ctx, answer.ID, "saw_it", at, nil); err != nil {
		return false
	}
	a.deleteOpenAnswerCheck(answer.ID)
	if item, err := a.store.LoadStreamItem(ctx, answer.AssistantMessageID); err == nil && item != nil {
		a.emitOpenAnswerMessageReload(answer.ChatSessionID, answer.AssistantMessageID, item.SortOrder)
	}
	a.emitBrowserEvent(openAnswersChangedEvent)
	return true
}

func (a *App) postOpenAnswer(id, transition, text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return a.postOpenAnswerTransition(ctx, id, transition, text, time.Now(), false)
}

func (a *App) postOpenAnswerTransition(ctx context.Context, id, transition, text string, now time.Time, releaseHeld bool) error {
	if a == nil || a.store == nil {
		return errors.New("open-answer storage is unavailable")
	}
	answer, err := a.store.GetOpenAnswer(ctx, id)
	if err != nil || answer.State != "open" {
		return err
	}
	if releaseHeld {
		if answer.HeldTransition == "" {
			return nil
		}
		transition, text = answer.HeldTransition, answer.HeldText
	}
	muted, err := a.store.OpenAnswerKindMuted(ctx, answer.Topic)
	if err != nil {
		return err
	}
	if a.IsClosedEyesModeEnabled() || muted ||
		!openAnswerCandidateTextAllowed(openanswer.Candidate{
			Question: answer.Question, Topic: answer.Topic, Source: answer.Source, WatchedEvent: answer.WatchedEvent,
		}) ||
		!openAnswerCandidateTextAllowed(openanswer.Candidate{
			Question: text, Topic: answer.Topic, Source: answer.Source,
		}) {
		return nil
	}
	if !releaseHeld {
		quiet := a.openAnswerQuietWindow()
		if quiet.Contains(now) && !openAnswerWindowStartsDuringQuiet(answer, quiet, now) {
			if err := a.store.HoldOpenAnswerTransition(ctx, id, transition, text, now.UnixMilli()); err != nil {
				return err
			}
			a.deleteOpenAnswerCheck(id)
			a.scheduleOpenAnswerHoldRelease(quiet.EndAfter(now))
			return nil
		}
		if answer.HeldTransition != "" {
			if err := a.store.HoldOpenAnswerTransition(ctx, id, transition, text, now.UnixMilli()); err != nil {
				return err
			}
			answer, err = a.store.GetOpenAnswer(ctx, id)
			if err != nil {
				return err
			}
			transition, text = answer.HeldTransition, answer.HeldText
		}
	}
	item := storage.StreamItemRow{
		ID: uuid.NewString(), Kind: "assistant", Content: strings.TrimSpace(text), RunID: "open-answer:" + id,
		CreatedAt: now.UnixMilli(),
	}
	if err := a.store.CommitOpenAnswerTransition(ctx, id, transition, now.UnixMilli(), &item); err != nil {
		return err
	}
	a.mainChatRowsChanged(answer.ChatSessionID)
	if transition == "settled" || transition == "interim" {
		openAnswerAfterFunc(time.Until(time.UnixMilli(item.CreatedAt).Add(24*time.Hour+time.Minute)), func() {
			a.learnUnopenedOpenAnswerKinds(context.Background(), answer.Topic, time.Now())
		})
	}
	a.emitBrowserEvent(mainChatItemsAppendedEvent, map[string]any{
		"sessionId": answer.ChatSessionID, "itemId": item.ID, "kind": "assistant", "source": "open_answer",
		"sortOrder": item.SortOrder,
	})
	a.emitBrowserEvent("open-answers:changed")
	if openAnswerTransitionNotifies(transition, answer.Delivery) && (!releaseHeld || a.mobileBriefScheduleDisabled()) {
		a.produceAnswerReady(answer, item)
	}
	a.learnUnopenedOpenAnswerKinds(ctx, answer.Topic, now)
	return nil
}

func openAnswerTransitionNotifies(transition, delivery string) bool {
	return transition == "settled" && delivery != "brief" ||
		transition == "interim" && delivery == "around"
}

func decodeOpenAnswerEvent(raw string) persistence.OpenAnswerEvent {
	var event persistence.OpenAnswerEvent
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	_ = decoder.Decode(&event)
	return event
}

func openAnswerMailReplyPostCopy(event persistence.OpenAnswerEvent, at time.Time, language string) string {
	replyAt := at.In(time.Local).Format("3:04 PM")
	if language == "Simplified Chinese" {
		return fmt.Sprintf("%s 回复了「%s」 · %s", event.Who, event.Label, replyAt)
	}
	return fmt.Sprintf("%s replied to %q · %s", event.Who, event.Label, replyAt)
}
