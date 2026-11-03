package main

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/internal/proactivevoice"
)

var errTodayReplyDraftUnavailable = errors.New("this reply draft is unavailable")
var errTodayReplyDraftNoRoute = errors.New("TODAY_REPLY_DRAFT_NO_ROUTE")
var todayReplyDraftMu sync.Mutex
var todayReplyRunMu sync.Mutex
var todayReplyRuns = map[string]*todayReplyRun{}

type todayReplyRun struct {
	cancel    context.CancelFunc
	stopped   bool
	committed bool
	at        time.Time
}

// CancelTodayReplyDraft stops a requested draft before it is saved. It never
// sends or deletes an existing draft. The short-lived tombstone handles a Stop
// arriving before the matching asynchronous Open reaches Go.
func (a *App) CancelTodayReplyDraft(id string) error {
	if !mobileValidID(id) {
		return errTodayReplyDraftUnavailable
	}
	todayReplyRunMu.Lock()
	defer todayReplyRunMu.Unlock()
	pruneTodayReplyRuns()
	if run := todayReplyRuns[id]; run != nil {
		if run.committed {
			return errTodayReplyDraftUnavailable
		}
		run.stopped = true
		if run.cancel != nil {
			run.cancel()
		}
	} else if len(todayReplyRuns) < 128 {
		todayReplyRuns[id] = &todayReplyRun{stopped: true, at: time.Now()}
	}
	return nil
}

// Called with todayReplyRunMu held.
func pruneTodayReplyRuns() {
	for id, run := range todayReplyRuns {
		if run.cancel == nil && time.Since(run.at) > 5*time.Minute {
			delete(todayReplyRuns, id)
		}
	}
}

func startTodayReplyRun(id string) (context.Context, func(), error) {
	if !mobileValidID(id) {
		return nil, nil, errTodayReplyDraftUnavailable
	}
	todayReplyRunMu.Lock()
	defer todayReplyRunMu.Unlock()
	pruneTodayReplyRuns()
	if existing := todayReplyRuns[id]; existing != nil {
		if existing.stopped && existing.cancel == nil {
			delete(todayReplyRuns, id)
		}
		return nil, nil, errTodayReplyDraftUnavailable
	}
	if len(todayReplyRuns) >= 128 {
		return nil, nil, errTodayReplyDraftUnavailable
	}
	ctx, cancel := context.WithCancel(context.Background())
	run := &todayReplyRun{cancel: cancel, at: time.Now()}
	todayReplyRuns[id] = run
	return ctx, func() {
		todayReplyRunMu.Lock()
		if todayReplyRuns[id] == run {
			run.cancel = nil
			run.at = time.Now()
		}
		todayReplyRunMu.Unlock()
		cancel()
	}, nil
}

// todayReplyDraftMoment binds a clicked card to exactly one current mail
// thread. A name in model-written prose is never an address or send authority.
func (a *App) todayReplyDraftMoment(ctx context.Context, briefID, cardID, message, label string) (string, error) {
	now := time.Now()
	sources := a.collectTodayChatSources(ctx, now)
	brief, ok := a.todayChatDeliveredBriefFrom(ctx, briefID, &sources)
	if !ok || a.IsClosedEyesModeEnabled() {
		return "", errTodayReplyDraftUnavailable
	}
	card, ok := a.todayChatCardNow(ctx, brief, cardID, sources)
	if !ok || card.Mode != mobileBriefCardSend || card.Kind == mobileBriefCardOpener || strings.TrimSpace(card.Label) != strings.TrimSpace(label) {
		return "", errTodayReplyDraftUnavailable
	}
	subject, err := a.todayChatCardSubject(ctx, brief, card, sources, now)
	if err != nil || strings.TrimSpace(subject.CardMessage) != strings.TrimSpace(message) {
		return "", errTodayReplyDraftUnavailable
	}
	evidence := mobileBriefEvidenceByRef(brief)
	momentID := ""
	for _, ref := range card.Refs {
		source := evidence[ref]
		if source.SourceKind != mobileBriefWaitingReplySource {
			continue
		}
		if momentID != "" && momentID != source.SourceID {
			return "", errTodayReplyDraftUnavailable
		}
		momentID = source.SourceID
	}
	if momentID == "" {
		return "", errTodayReplyDraftNoRoute
	}
	if _, ok := a.mobileBriefWaitingReply(ctx, momentID, now); !ok {
		return "", errTodayReplyDraftUnavailable
	}
	return momentID, nil
}

// OpenTodayReplyDraft prepares or opens a reply tied to the card's real mail
// source. It never sends; the task's Send path repeats its source checks.
func (a *App) OpenTodayReplyDraft(briefID, cardID, message, label, requestID string) (string, error) {
	ctx, finish, err := startTodayReplyRun(requestID)
	if err != nil {
		return "", err
	}
	defer finish()
	todayReplyDraftMu.Lock()
	defer todayReplyDraftMu.Unlock()
	if ctx.Err() != nil {
		return "", errTodayReplyDraftUnavailable
	}
	momentID, err := a.todayReplyDraftMoment(ctx, briefID, cardID, message, label)
	if err != nil {
		return "", err
	}
	if id, err := a.DesktopDraftTaskID(momentID); err == nil {
		todayReplyRunMu.Lock()
		if ctx.Err() != nil {
			todayReplyRunMu.Unlock()
			return "", errTodayReplyDraftUnavailable
		}
		todayReplyRuns[requestID].committed = true
		todayReplyRunMu.Unlock()
		return id, nil
	}
	row, err := a.store.GetEngagementMoment(ctx, momentID)
	if err != nil || row == nil {
		return "", errTodayReplyDraftUnavailable
	}
	main, err := a.ensureMainChat(ctx)
	if err != nil {
		return "", errTodayReplyDraftUnavailable
	}
	card, err := a.makeTodayReplyDraft(ctx, momentFromRow(*row))
	if err != nil {
		return "", errTodayReplyDraftUnavailable
	}
	current, err := a.todayReplyDraftMoment(ctx, briefID, cardID, message, label)
	if err != nil || current != momentID {
		return "", errTodayReplyDraftUnavailable
	}
	mainChatProactiveActionMu.Lock()
	defer mainChatProactiveActionMu.Unlock()
	todayReplyRunMu.Lock()
	if ctx.Err() != nil {
		todayReplyRunMu.Unlock()
		return "", errTodayReplyDraftUnavailable
	}
	posted := a.installTodayReplyDraft(ctx, main.LocalID, card)
	if posted != "" {
		todayReplyRuns[requestID].committed = true
	}
	todayReplyRunMu.Unlock()
	if posted == "" {
		return "", errTodayReplyDraftUnavailable
	}
	id, err := a.DesktopDraftTaskID(momentID)
	if err != nil {
		return "", errTodayReplyDraftUnavailable
	}
	return id, nil
}

// installTodayReplyDraft retains the moment identity when an earlier generic
// notification already exists. The action mutex protects its current state;
// the voice receipt still binds the replacement to the current mail source.
func (a *App) installTodayReplyDraft(ctx context.Context, sessionID string, card mainChatProactive) string {
	row, err := a.store.FindProactiveStreamItem(ctx, sessionID, card.MomentID)
	if err != nil {
		return ""
	}
	if row == nil {
		return a.postProactiveItem(card, "today-reply:"+card.MomentID)
	}
	var root mainChatProactive
	if json.Unmarshal([]byte(row.ToolInput), &root) != nil {
		return ""
	}
	old := findMainChatProactiveCard(&root, card.MomentID)
	if old == nil || old.Kind != card.Kind || old.Draft {
		return ""
	}
	switch old.State {
	case "", "pending", "opened":
	default:
		return ""
	}
	card.State = old.State
	*old = card
	a.bindEngagementCardRead(ctx, &root)
	payload, err := json.Marshal(root)
	if err != nil || a.store.UpdateStreamItemToolInput(ctx, row.ID, string(payload)) != nil {
		return ""
	}
	a.mainChatRowsChanged(sessionID)
	a.emitBrowserEvent(mainChatItemsAppendedEvent, map[string]any{"sessionId": sessionID, "itemId": row.ID, "kind": "system", "source": mainChatSourceEngagement})
	return row.ID
}

// A requested draft has its own retryable run: it must not consume the
// single attempt reserved for an unsolicited engagement notification.
func (a *App) makeTodayReplyDraft(ctx context.Context, moment engagement.Moment) (mainChatProactive, error) {
	now := time.Now()
	lang := a.currentUILanguage()
	source, reason := a.loadProactiveVoiceSource(ctx, moment, now, lang)
	job, addresses, assembly := assembleProactiveVoiceJob(string(moment.Kind), lang, true, now, source)
	if reason != "" || assembly != "" || !job.DraftAllowed {
		return mainChatProactive{}, errTodayReplyDraftUnavailable
	}
	raw, err := a.runProactiveVoice(ctx, job)
	if err != nil {
		return mainChatProactive{}, err
	}
	fresh, reason := a.loadProactiveVoiceSource(ctx, moment, time.Now(), lang)
	freshJob, freshAddresses, assembly := assembleProactiveVoiceJob(string(moment.Kind), lang, true, now, fresh)
	freshJob.MomentID = job.MomentID
	if ctx.Err() != nil || reason != "" || assembly != "" || !reflect.DeepEqual(job, freshJob) || !reflect.DeepEqual(addresses, freshAddresses) || !reflect.DeepEqual(source.Provenance, fresh.Provenance) {
		return mainChatProactive{}, errTodayReplyDraftUnavailable
	}
	output, rejection := proactivevoice.Validate(job, raw, fresh.ContactNames)
	if rejection != "" || output.Abstain {
		return mainChatProactive{}, errTodayReplyDraftUnavailable
	}
	target, sourceKind := engagementChatTarget(moment)
	card := mainChatProactive{MomentID: moment.ID, Kind: string(moment.Kind), State: "pending", Target: target,
		Evidence: []mainChatEvidenceRef{{Kind: sourceKind, ID: moment.SubjectID, Target: target}}}
	if !moment.ExpiresAt.IsZero() {
		card.ExpiresAt = moment.ExpiresAt.UnixMilli()
	}
	if !moment.CreatedAt.IsZero() {
		card.Evidence[0].At = moment.CreatedAt.UTC().Format(time.RFC3339)
	}
	if !applyProactiveVoiceResult(&card, job, output, addresses) {
		return mainChatProactive{}, errTodayReplyDraftUnavailable
	}
	populateMainChatSendDraft(&card, moment, fresh)
	populateMainChatProactiveCopy(&card, nil)
	if !mobileDraftCard(&card) || !mainChatDraftSendable(card.DraftContent) || !a.saveProactiveVoiceReceipt(ctx, moment, now, job, addresses, fresh.Provenance, &card) {
		return mainChatProactive{}, errTodayReplyDraftUnavailable
	}
	return card, nil
}
