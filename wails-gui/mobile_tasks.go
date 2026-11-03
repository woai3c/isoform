package main

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"wails-gui/internal/handoff"
)

const (
	mobileTaskGroupNeedsYou = "needs_you"
	mobileTaskGroupRunning  = "running"
	mobileTaskGroupDone     = "done"
	mobileTasksGroupLimit   = 20
	mobileTasksDoneWindow   = 7 * 24 * time.Hour
)

type MobileTaskProgress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

type MobileTaskView struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Status        string `json:"status"`
	Group         string `json:"group"`
	BlockedReason string `json:"blockedReason,omitempty"`
	// StopCause says why a Needs you task stopped without finishing;
	// StoppedBy is "person" on a Done task the person stopped themselves.
	StopCause string `json:"stopCause,omitempty"`
	StoppedBy string `json:"stoppedBy,omitempty"`
	// Assistant names the runner a stop cause is about: claude_code or codex.
	Assistant string              `json:"assistant,omitempty"`
	Runner    string              `json:"runner,omitempty"`
	Progress  *MobileTaskProgress `json:"progress,omitempty"`
	CreatedAt string              `json:"createdAt"`
	UpdatedAt string              `json:"updatedAt"`
	// Outcome is the Done row's sentence (20.7b); absent while the task runs.
	Outcome *MobileTaskOutcome `json:"outcome,omitempty"`
	// Unread marks a Needs you task changed since its thread was last opened.
	Unread   bool   `json:"unread,omitempty"`
	OpenedAt string `json:"openedAt,omitempty"`
	Source   string `json:"source,omitempty"`
	// StepsToCheck and RepliesToChoose make a stopped row say Review (v1.0.2,
	// v1.0.7): steps whose outcome is unknown and replies needing a choice.
	StepsToCheck    int `json:"stepsToCheck,omitempty"`
	RepliesToChoose int `json:"repliesToChoose,omitempty"`
	linkedDraftID   string
	linkedPerson    string
	linkedOriginal  string
}

type MobileTaskCounts struct {
	NeedsYou     int `json:"needsYou"`
	Running      int `json:"running"`
	DoneThisWeek int `json:"doneThisWeek"`
}

type MobileTaskPage struct {
	Tasks  []MobileTaskView `json:"tasks"`
	Counts MobileTaskCounts `json:"counts"`
	// FinishedDeliverables is populated only for the desktop All done view.
	FinishedDeliverables []DeliverableEntry `json:"finishedDeliverables,omitempty"`
	// Threads tells a phone this Mac serves task threads (20.7b), so a Done
	// row without an outcome sentence is left out rather than shown as "Done".
	Threads bool `json:"threads,omitempty"`
	// Scheduled lists the briefs and routines due soon (20.7b).
	Scheduled []MobileScheduledTask `json:"scheduled,omitempty"`
	// WaitingItems is the shared phone waiting list, including actionable
	// chat cards that are not task threads. Omitted until Today has a copy.
	WaitingItems *[]mobileTodayNeed `json:"waitingItems,omitempty"`
	WaitingCount *int               `json:"waitingCount,omitempty"`
	// allWaiting is used only to deduplicate cards beyond the response limit.
	allWaiting []MobileTaskView
}

type MobileTaskStep struct {
	Text  string `json:"text"`
	State string `json:"state"`
	// At is when a done step was confirmed, when the hub recorded it.
	At string `json:"at,omitempty"`
}

type MobileTaskDetail struct {
	MobileTaskView
	StartedAt string           `json:"startedAt,omitempty"`
	Steps     []MobileTaskStep `json:"steps"`
	// The thread (20.7b): absent fields read as an empty thread on older Macs.
	Origin  *MobileTaskOrigin `json:"origin,omitempty"`
	Events  []MobileTaskEvent `json:"events"`
	Actions []string          `json:"actions"`
	// Draft is the reply card of a draft task (20.7d).
	Draft *MobileTaskDraft `json:"draft,omitempty"`
	Hold  *HoldStatus      `json:"hold,omitempty"`
	// HoldCleanup is the one-time ask after the work was marked done before its hold starts.
	HoldCleanup bool `json:"holdCleanup,omitempty"`
	// Replies are the person's queued replies (tasks-review v1.0.5–v1.0.7).
	Replies []MobileTaskReply `json:"replies,omitempty"`
	// EffectSteps are the stopped run's steps whose effect left it (v1.0.2);
	// Skipped are the ones the current run skipped.
	EffectSteps []MobileTaskEffectStep `json:"effectSteps,omitempty"`
	Skipped     []MobileTaskEffectStep `json:"skipped,omitempty"`
}

func mobileTaskDetail(entry handoff.Entry, subjects []string) (MobileTaskDetail, bool) {
	title := strings.TrimSpace(entry.Title)
	if title == "" || !mobileValidID(entry.ID) || mobileTaskHiddenAsMailDuplicate(entry, subjects) {
		return MobileTaskDetail{}, false
	}
	switch entry.Status {
	case handoff.StatusActive, handoff.StatusInProgress, handoff.StatusBlocked,
		handoff.StatusNeedsReview, handoff.StatusCompleted:
	case handoff.StatusAborted:
		// The phone's contract still leaves out a task aborted before stops
		// were recorded (PROTOCOL.md, GET /tasks/{id}); the Mac shows it in
		// Done as "Stopped at {time}."
		if group, _ := handoff.Placement(entry); group == handoff.BoardNone || handoff.StoppedUnrecorded(entry) {
			return MobileTaskDetail{}, false
		}
	default:
		return MobileTaskDetail{}, false
	}
	task := mobileTaskView(entry)
	criteria := entry.Criteria
	if len(criteria) > 12 {
		criteria = criteria[:12]
	}
	steps := make([]MobileTaskStep, 0, len(criteria))
	for i, criterion := range criteria {
		state := "pending"
		switch {
		case entry.Result != nil && (entry.Status == handoff.StatusCompleted || entry.Status == handoff.StatusNeedsReview) &&
			i < len(entry.Result.Criteria):
			if entry.Result.Criteria[i].Met {
				state = "done"
			}
		case entry.Status == handoff.StatusCompleted:
			state = "done"
		case entry.Verification != nil && entry.Verification.Total > 0 && i < entry.Verification.Met:
			state = "done"
		}
		if mobileTaskEchoesMail(criterion, subjects) {
			continue
		}
		step := MobileTaskStep{Text: mobileOfferText(criterion, 200), State: state}
		if state == "done" && entry.Thread != nil && i < len(entry.Thread.StepDoneAt) {
			step.At = mobileTaskTime(entry.Thread.StepDoneAt[i])
		}
		steps = append(steps, step)
	}
	if entry.Status == handoff.StatusInProgress {
		for i := range steps {
			if steps[i].State != "done" {
				steps[i].State = "current"
				break
			}
		}
	}
	startedAt := ""
	switch entry.Status {
	case handoff.StatusInProgress, handoff.StatusNeedsReview, handoff.StatusBlocked, handoff.StatusCompleted:
		if entry.AttemptStartedAt > 0 {
			startedAt = time.UnixMilli(entry.AttemptStartedAt).UTC().Format(time.RFC3339)
		}
	}
	return MobileTaskDetail{
		MobileTaskView: task,
		StartedAt:      startedAt,
		Steps:          steps,
		Events:         []MobileTaskEvent{},
		Actions:        []string{},
	}, true
}

func mobileTaskView(entry handoff.Entry) MobileTaskView {
	task := MobileTaskView{
		ID: entry.ID, Title: mobileOfferText(strings.TrimSpace(entry.Title), 200),
		Status: string(entry.Status), CreatedAt: time.UnixMilli(entry.CreatedAt).UTC().Format(time.RFC3339),
		UpdatedAt: time.UnixMilli(entry.UpdatedAt).UTC().Format(time.RFC3339),
	}
	switch entry.Status {
	case handoff.StatusNeedsReview, handoff.StatusBlocked, handoff.StatusActive:
		task.Group = mobileTaskGroupNeedsYou
	case handoff.StatusInProgress:
		task.Group = mobileTaskGroupRunning
	case handoff.StatusCompleted:
		task.Group = mobileTaskGroupDone
	case handoff.StatusAborted:
		task.Group, _ = handoff.Placement(entry)
	}
	if group, cause := handoff.Placement(entry); group == handoff.BoardNeedsYou && cause != "" {
		task.StopCause = cause
		task.Assistant = entry.StopAssistant
	}
	if handoff.StoppedByYou(entry) {
		task.StoppedBy = handoff.StoppedByPerson
	}
	if entry.Status == handoff.StatusActive && entry.ListItem != nil && entry.ListItem.Who == "reminder" {
		task.Group = "scheduled"
	}
	if entry.Status == handoff.StatusActive && entry.ListItem != nil && entry.ListItem.Who == "draft" && entry.ListItem.DraftTaskID == "" {
		task.Group = mobileTaskGroupRunning
		task.Status = "drafting"
	}
	if entry.Status == handoff.StatusBlocked &&
		(entry.BlockedReason == handoff.BlockedInterrupted || entry.BlockedReason == handoff.BlockedTimeout) {
		task.BlockedReason = entry.BlockedReason
	}
	if entry.Verification != nil && entry.Verification.Total > 0 {
		task.Progress = &MobileTaskProgress{Done: entry.Verification.Met, Total: entry.Verification.Total}
	}
	task.Outcome = mobileTaskOutcome(entry)
	task.Unread = mobileTaskUnread(entry)
	task.StepsToCheck, task.RepliesToChoose = taskStoppedAwaitingAnswers(entry)
	if entry.Thread != nil {
		task.OpenedAt = mobileTaskTime(entry.Thread.OpenedAt)
		if entry.Thread.Origin != nil {
			task.Source = entry.Thread.Origin.Kind
		}
	}
	if entry.ListItem != nil {
		task.linkedDraftID = entry.ListItem.DraftTaskID
		task.linkedPerson = entry.ListItem.Person
		task.linkedOriginal = entry.ListItem.Original
		if listHandoffBackend(&entry) != "" {
			task.Runner = entry.ListItem.Who
		}
	}
	return task
}

func mobileTaskEchoesMail(title string, subjects []string) bool {
	folded := mobileGoalFold(title)
	for _, subject := range subjects {
		if strings.Contains(folded, subject) ||
			(len([]rune(folded)) >= mobileGoalSubjectMinRunes && strings.Contains(subject, folded)) {
			return true
		}
	}
	return false
}

// An explicitly added list task is the person's own request, even when its
// title matches a Mail subject. The mail deduplication applies to derived work.
func mobileTaskHiddenAsMailDuplicate(entry handoff.Entry, subjects []string) bool {
	if entry.Thread != nil && entry.Thread.Origin != nil && entry.Thread.Origin.Kind == handoff.OriginList {
		return false
	}
	return mobileTaskEchoesMail(entry.Title, subjects)
}

func mobileTaskPage(entries []handoff.Entry, subjects []string, now time.Time) MobileTaskPage {
	return mobileTaskPageWithLimit(entries, subjects, now, mobileTasksGroupLimit)
}

func mobileTaskPageWithLimit(entries []handoff.Entry, subjects []string, now time.Time, limit int) MobileTaskPage {
	type groupedTask struct {
		task  MobileTaskView
		order int
	}
	groups := map[string][]groupedTask{
		mobileTaskGroupNeedsYou: {},
		mobileTaskGroupRunning:  {},
		mobileTaskGroupDone:     {},
	}
	counts := MobileTaskCounts{}
	cutoff := now.Add(-mobileTasksDoneWindow)

	for _, entry := range entries {
		title := strings.TrimSpace(entry.Title)
		if title == "" || !mobileValidID(entry.ID) || mobileTaskHiddenAsMailDuplicate(entry, subjects) || handoff.PutOffAway(entry, now) {
			continue
		}
		if entry.Status == handoff.StatusActive && entry.ListItem != nil && entry.ListItem.Who == "reminder" {
			continue
		}
		group, order := "", 0
		switch entry.Status {
		case handoff.StatusNeedsReview:
			group, order = mobileTaskGroupNeedsYou, 0
		case handoff.StatusBlocked:
			group, order = mobileTaskGroupNeedsYou, 1
		case handoff.StatusActive:
			group, order = mobileTaskGroupNeedsYou, 2
			if entry.ListItem != nil && entry.ListItem.Who == "draft" && entry.ListItem.DraftTaskID == "" {
				group = mobileTaskGroupRunning
			}
		case handoff.StatusInProgress:
			group = mobileTaskGroupRunning
		case handoff.StatusCompleted:
			updated := time.UnixMilli(entry.UpdatedAt)
			if updated.Before(cutoff) || updated.After(now) {
				continue
			}
			group = mobileTaskGroupDone
		case handoff.StatusAborted:
			placed, _ := handoff.Placement(entry)
			if handoff.StoppedUnrecorded(entry) {
				placed = handoff.BoardNone
			}
			switch placed {
			case handoff.BoardNeedsYou:
				group, order = mobileTaskGroupNeedsYou, 1
			case handoff.BoardDone:
				updated := time.UnixMilli(entry.UpdatedAt)
				if updated.Before(cutoff) || updated.After(now) {
					continue
				}
				group = mobileTaskGroupDone
			default:
				continue
			}
		default:
			continue
		}

		task := mobileTaskView(entry)
		task.Group = group
		switch group {
		case mobileTaskGroupNeedsYou:
			counts.NeedsYou++
		case mobileTaskGroupRunning:
			counts.Running++
		case mobileTaskGroupDone:
			counts.DoneThisWeek++
		}
		groups[group] = append(groups[group], groupedTask{task: task, order: order})
	}

	groupOrder := []string{mobileTaskGroupNeedsYou, mobileTaskGroupRunning, mobileTaskGroupDone}
	tasks := make([]MobileTaskView, 0)
	for _, group := range groupOrder {
		items := groups[group]
		sort.Slice(items, func(i, j int) bool {
			if group == mobileTaskGroupNeedsYou && items[i].order != items[j].order {
				return items[i].order < items[j].order
			}
			if items[i].task.UpdatedAt != items[j].task.UpdatedAt {
				return items[i].task.UpdatedAt > items[j].task.UpdatedAt
			}
			return items[i].task.ID < items[j].task.ID
		})
		if len(items) > limit {
			items = items[:limit]
		}
		for _, item := range items {
			tasks = append(tasks, item.task)
		}
	}
	return MobileTaskPage{Tasks: tasks, Counts: counts, Threads: true}
}

func (bridge *mobileBridge) serveTaskList(w http.ResponseWriter) {
	bridge.serveTaskListForDevice(w, mobileDeviceRecord{MobileDeviceView: MobileDeviceView{Scopes: []string{mobileScopeChatRead}}})
}

func (bridge *mobileBridge) serveTaskListForDevice(w http.ResponseWriter, device mobileDeviceRecord) {
	page, err := bridge.mobileTaskList(mobileGoalsNow())
	if err != nil {
		mobileJSON(w, http.StatusServiceUnavailable, nil, errMobileStoreUnavailable.Error())
		return
	}
	if runner := mobileTaskRunner(loadAIProviderConfig().Provider); runner != "" {
		for i := range page.Tasks {
			if page.Tasks[i].Group == mobileTaskGroupRunning && page.Tasks[i].Status != "drafting" && page.Tasks[i].Runner == "" {
				page.Tasks[i].Runner = runner
			}
		}
	}
	bridge.today.mu.RLock()
	if bridge.today.value != nil {
		view := mobileTodayForDevice(*bridge.today.value, device)
		items, count := mobileTaskWaitingItems(page, view.NeedsYou)
		page.WaitingItems, page.WaitingCount = &items, &count
	}
	bridge.today.mu.RUnlock()
	if page.WaitingItems != nil {
		items := *page.WaitingItems
		for !mobileTopicResponseFits(page) && len(items) > 0 {
			items = items[:len(items)-1]
			page.WaitingItems = &items
		}
		if !mobileTopicResponseFits(page) || len(items) == 0 && page.WaitingCount != nil && *page.WaitingCount > 0 {
			mobileJSON(w, http.StatusServiceUnavailable, nil, "the task list is too large to send")
			return
		}
	}
	mobileJSON(w, http.StatusOK, page, "")
}

func mobileTaskWaitingItems(page MobileTaskPage, needs []mobileTodayNeed) ([]mobileTodayNeed, int) {
	items := []mobileTodayNeed{}
	positions := map[string]int{}
	tasks := page.Tasks
	if page.allWaiting != nil {
		tasks = page.allWaiting
	}
	for _, task := range tasks {
		if task.Group != mobileTaskGroupNeedsYou {
			continue
		}
		copy := task
		positions[task.ID] = len(items)
		items = append(items, mobileTodayNeed{ID: task.ID, Task: &copy})
	}
	loaded := len(items)
	for _, need := range needs {
		if need.Card == nil || need.Card.Proactive == nil {
			continue
		}
		card := need.Card.Proactive
		if _, found := positions[mobileDraftTaskID(card.MomentID)]; found {
			continue
		}
		if card.Work != nil {
			if index, found := positions[card.Work.HandoffID]; found && card.Permission == nil {
				_ = index
				continue
			}
		}
		if card.Permission != nil {
			if index, found := positions[card.Permission.HandoffID]; found {
				items[index] = need
				continue
			}
		}
		if card.Target.Kind == mainChatTargetHandoff {
			if _, found := positions[card.Target.ID]; found {
				continue
			}
		}
		items = append(items, need)
	}
	count := len(items)
	if page.Counts.NeedsYou > loaded {
		count += page.Counts.NeedsYou - loaded
	}
	if len(items) > mobileTasksGroupLimit {
		items = items[:mobileTasksGroupLimit]
	}
	return items, count
}

// mobileTaskList is shared by Tasks and the phone's Today projection so that
// the waiting rows, their order and their count come from the same list.
func (bridge *mobileBridge) mobileTaskList(now time.Time) (MobileTaskPage, error) {
	if bridge.app == nil || bridge.app.handoffStore == nil {
		return MobileTaskPage{}, errMobileStoreUnavailable
	}
	if err := bridge.app.reconcileBrainDumpDrafts(bridge.ctx, now); err != nil {
		return MobileTaskPage{}, err
	}
	entries, err := bridge.app.handoffStore.List()
	if err != nil {
		return MobileTaskPage{}, err
	}
	for i, entry := range entries {
		if !strings.HasPrefix(entry.ID, "hold.") || entry.Thread == nil || len(entry.Thread.Leftovers) != 1 || entry.Thread.Leftovers[0].Kind != handoff.LeftoverCalendarHold {
			continue
		}
		if status, statusErr := bridge.app.GetHoldStatus(entry.Thread.Leftovers[0].Ref); statusErr == nil {
			bridge.syncHoldOfferTask(status)
			if updated, getErr := bridge.app.handoffStore.Get(entry.ID); getErr == nil {
				entries[i] = bridge.app.mobileHoldTaskProjection(updated)
			}
		}
	}
	subjects, err := mobileGoalSubjects(bridge.app)
	if err != nil {
		return MobileTaskPage{}, err
	}
	page := mobileTaskPage(entries, subjects, now)
	drafts, err := bridge.app.mobileDraftTasks(bridge.ctx, now)
	if err != nil {
		return MobileTaskPage{}, err
	}
	page = mobileTaskPageWithDrafts(page, drafts, bridge.app, now)
	full := mobileTaskPageWithDrafts(mobileTaskPageWithLimit(entries, subjects, now, len(entries)), drafts, bridge.app, now)
	page.allWaiting = make([]MobileTaskView, 0, full.Counts.NeedsYou)
	for _, task := range full.Tasks {
		if task.Group == mobileTaskGroupNeedsYou {
			page.allWaiting = append(page.allWaiting, task)
		}
	}
	page.Scheduled = append(mobileScheduledTasks(bridge.app.GetUpcoming(), subjects, now), mobileListReminders(entries)...)
	sort.SliceStable(page.Scheduled, func(i, j int) bool {
		left, leftErr := time.Parse(time.RFC3339, page.Scheduled[i].At)
		right, rightErr := time.Parse(time.RFC3339, page.Scheduled[j].At)
		if leftErr != nil {
			return false
		}
		if rightErr != nil {
			return true
		}
		return left.Before(right)
	})
	return page, nil
}

func (bridge *mobileBridge) serveTaskDetail(w http.ResponseWriter, id string) {
	if bridge.app != nil && strings.HasPrefix(id, mobileDraftTaskPrefix) {
		bridge.serveDraftTaskDetail(w, id)
		return
	}
	if bridge.app == nil || bridge.app.handoffStore == nil {
		mobileJSON(w, http.StatusServiceUnavailable, nil, errMobileStoreUnavailable.Error())
		return
	}
	if !mobileValidID(id) {
		mobileJSON(w, http.StatusNotFound, nil, "this task is no longer on your computer")
		return
	}
	if err := bridge.app.reconcileBrainDumpDrafts(bridge.ctx, mobileGoalsNow()); err != nil {
		mobileJSON(w, http.StatusServiceUnavailable, nil, errMobileStoreUnavailable.Error())
		return
	}
	entry, err := bridge.app.handoffStore.Get(id)
	if err != nil {
		mobileJSON(w, http.StatusNotFound, nil, "this task is no longer on your computer")
		return
	}
	subjects, err := mobileGoalSubjects(bridge.app)
	if err != nil {
		mobileJSON(w, http.StatusServiceUnavailable, nil, errMobileStoreUnavailable.Error())
		return
	}
	detail, visible := bridge.app.mobileTaskThreadDetail(bridge.ctx, entry, subjects)
	if !visible {
		mobileJSON(w, http.StatusNotFound, nil, "this task is no longer on your computer")
		return
	}
	mobileJSON(w, http.StatusOK, detail, "")
}

func (bridge *mobileBridge) serveTaskStop(w http.ResponseWriter, r *http.Request, device mobileDeviceRecord, id string) {
	var input struct {
		IdempotencyKey string `json:"idempotencyKey"`
	}
	if !mobileDecode(w, r, &input) {
		return
	}
	key, valid := mobileKey(device.DeviceID, input.IdempotencyKey)
	if !valid || !mobileValidID(id) {
		mobileJSON(w, http.StatusBadRequest, nil, "a valid task id and idempotency key are required")
		return
	}
	bridge.runReceipted(w, device, key, r.Method+" "+r.URL.Path, func(w http.ResponseWriter) {
		if bridge.app == nil || bridge.app.handoffStore == nil {
			mobileJSON(w, http.StatusServiceUnavailable, nil, errMobileStoreUnavailable.Error())
			return
		}
		entry, err := bridge.app.handoffStore.Get(id)
		if err != nil {
			mobileJSON(w, http.StatusNotFound, nil, "this task is no longer on your computer")
			return
		}
		subjects, err := mobileGoalSubjects(bridge.app)
		if err != nil {
			mobileJSON(w, http.StatusServiceUnavailable, nil, errMobileStoreUnavailable.Error())
			return
		}
		if _, visible := mobileTaskDetail(entry, subjects); !visible {
			mobileJSON(w, http.StatusNotFound, nil, "this task is no longer on your computer")
			return
		}
		switch entry.Status {
		case handoff.StatusActive, handoff.StatusInProgress, handoff.StatusBlocked, handoff.StatusNeedsReview:
		default:
			mobileJSON(w, http.StatusConflict, nil, "this task can no longer be stopped")
			return
		}
		if err := bridge.app.abortHandoff(id, ""); err != nil {
			mobileJSON(w, http.StatusConflict, nil, "this task could not be stopped; refresh and try again")
			return
		}
		updated, err := bridge.app.handoffStore.Get(id)
		if err != nil {
			mobileJSON(w, http.StatusNotFound, nil, "this task is no longer on your computer")
			return
		}
		task := mobileTaskView(updated)
		task.Group = "stopped"
		mobileJSON(w, http.StatusOK, task, "")
	})
}

// mobileTaskPageWithDrafts adds reply drafts: waiting ones lead Needs you, and
// those sent or answered in the last week join Done with their outcome.
func mobileTaskPageWithDrafts(page MobileTaskPage, drafts []mobileDraftTask, a *App, now time.Time) MobileTaskPage {
	needs, done := []MobileTaskView{}, []MobileTaskView{}
	cutoff := now.Add(-mobileDraftDoneWindow).UTC().Format(time.RFC3339)
	linked := make(map[string]int)
	for i, task := range page.Tasks {
		if task.linkedDraftID != "" {
			linked[task.linkedDraftID] = i
		}
	}
	for _, draft := range drafts {
		view := draft.view(a)
		if index, found := linked[draft.TaskID]; found {
			task := &page.Tasks[index]
			item := BrainDumpItem{Who: "draft", Person: task.linkedPerson, Title: task.linkedOriginal}
			if view.Group == mobileTaskGroupNeedsYou && matchBrainDumpDraft(a, item, []mobileDraftTask{draft}) != nil {
				task.Status = view.Status
				continue
			}
			if task.Group == mobileTaskGroupDone && draft.done() && matchBrainDumpDraftWithDone(a, item, []mobileDraftTask{draft}, true) != nil {
				continue
			}
		}
		switch {
		case view.Group == mobileTaskGroupNeedsYou:
			needs = append(needs, view)
		case view.UpdatedAt >= cutoff:
			done = append(done, view)
		}
	}
	if len(needs) == 0 && len(done) == 0 {
		return page
	}
	doneCount := len(done)
	tasks := make([]MobileTaskView, 0, len(page.Tasks)+len(needs)+len(done))
	tasks = append(tasks, needs...)
	for _, task := range page.Tasks {
		if task.Group == mobileTaskGroupDone && len(done) > 0 {
			tasks = append(tasks, done...)
			done = nil
		}
		tasks = append(tasks, task)
	}
	tasks = append(tasks, done...)
	page.Tasks = tasks
	page.Counts.NeedsYou += len(needs)
	page.Counts.DoneThisWeek += doneCount
	return page
}
