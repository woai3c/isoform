package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"wails-gui/internal/engagement"
	"wails-gui/internal/handoff"
	"wails-gui/internal/platform/mailheaders"
	"wails-gui/storage"
)

const brainDumpLimit = 12

var brainDumpDraftLinkMu sync.Mutex

// BrainDumpItem is a proposal. Parsing does not authorize execution.
type BrainDumpItem struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Who      string `json:"who"`
	When     string `json:"when,omitempty"`
	Project  string `json:"project,omitempty"`
	Person   string `json:"person,omitempty"`
	NoRunner bool   `json:"noRunner,omitempty"`
}

type BrainDumpResult struct {
	Items []BrainDumpItem `json:"items"`
}

// prepareBrainDumpEntry creates a pending task record. The original text stays
// in the body even when a long fallback line needs a shorter row title.
func prepareBrainDumpEntry(item BrainDumpItem, id string, source handoff.Source, now time.Time) handoff.Entry {
	at := now.UnixMilli()
	title := mobileBriefText(strings.TrimSpace(item.Title), mobileTaskTitleMax)
	encoded, _ := json.Marshal(item)
	fingerprint := sha256.Sum256(encoded)
	entry := newTaskEntryFrom(CreateTaskInput{Title: title, Brief: strings.TrimSpace(item.Title)}, source, &handoff.Origin{Kind: handoff.OriginList}, id, at)
	entry.ListItem = &handoff.ListItem{Who: item.Who, Person: item.Person, When: item.When, Project: item.Project, Original: strings.TrimSpace(item.Title), Fingerprint: hex.EncodeToString(fingerprint[:])}
	entry.Thread.Events = []handoff.Event{{ID: "list-start", Kind: handoff.EventIsoform, At: at, Code: "list_start"}}
	return entry
}

// brainDumpFirstLine words the stored event when the thread is read, so a
// language change also rewords existing tasks.
func brainDumpFirstLine(item *handoff.ListItem, lang string, created time.Time) string {
	if item == nil {
		return ""
	}
	zh := lang == "zh-CN"
	switch item.Who {
	case "draft":
		if zh {
			return "正在起草，写好后放在这里等你发送。"
		}
		return "Drafting it now. It lands here for you to send."
	case "claude_code", "codex":
		name := "Claude Code"
		if item.Who == "codex" {
			name = "Codex"
		}
		if zh {
			return "你电脑上的 " + name + " 在做，结果放在这里给你看。"
		}
		return name + " on your computer is on it. You review the result here."
	case "reminder":
		at, err := time.Parse(time.RFC3339, item.When)
		if err != nil {
			if zh {
				return "这件需要你自己来。"
			}
			return "This one is yours to do."
		}
		at = at.In(time.Local)
		created = created.In(time.Local)
		tomorrow := created.AddDate(0, 0, 1)
		isTomorrow := at.Year() == tomorrow.Year() && at.YearDay() == tomorrow.YearDay()
		if zh {
			day := fmt.Sprintf("%d 月 %d 日", at.Month(), at.Day())
			if isTomorrow {
				day = "明天"
			}
			period, hour := "上午", at.Hour()
			if hour >= 12 {
				period = "下午"
			}
			hour %= 12
			if hour == 0 {
				hour = 12
			}
			return fmt.Sprintf("我%s%s %d:%02d 提醒你。这件需要你自己来。", day, period, hour, at.Minute())
		}
		day := "on " + at.Format("January 2")
		if isTomorrow {
			day = "tomorrow"
		}
		return "I'll remind you " + day + " at " + at.Format("3:04 PM") + ". This one is yours to do."
	}
	return ""
}

// matchBrainDumpDraft reuses an already source-checked draft only when both the
// named person and the full Mail subject identify one unambiguous thread.
func matchBrainDumpDraft(a *App, item BrainDumpItem, drafts []mobileDraftTask) *mobileDraftTask {
	return matchBrainDumpDraftWithDone(a, item, drafts, false)
}

func matchBrainDumpDraftWithDone(a *App, item BrainDumpItem, drafts []mobileDraftTask, allowDone bool) *mobileDraftTask {
	if item.Who != "draft" || len(strings.Fields(item.Person)) == 0 {
		return nil
	}
	person := mobileGoalFold(item.Person)
	title := mobileGoalFold(item.Title)
	personOnly := title == "reply to "+person || title == "reply to "+person+"." || title == "回复 "+person || title == "回复"+person
	var match *mobileDraftTask
	for i := range drafts {
		draft := &drafts[i]
		if (!allowDone && draft.done()) || draft.Inbound == nil || draft.Card.DraftContent == nil {
			continue
		}
		name := strings.TrimSpace(draft.Inbound.SenderName)
		if store := a.storeForMobile(); store != nil && draft.Card.DraftContent.PrincipalID != "" {
			if principal, err := store.GetPrincipal(context.Background(), draft.Card.DraftContent.PrincipalID); err == nil && strings.TrimSpace(principal.DisplayName) != "" {
				name = principal.DisplayName
			}
		}
		nameFields := strings.Fields(name)
		if len(nameFields) == 0 {
			continue
		}
		if len(strings.Fields(item.Person)) == 1 {
			name = nameFields[0]
		}
		if mobileGoalFold(name) != person {
			continue
		}
		subject := mobileGoalFold(mailheaders.NormalizeSubject(draft.Card.DraftContent.Subject))
		minimum := 3
		if first, _ := utf8.DecodeRuneInString(subject); first > 127 {
			minimum = 2
		}
		if len([]rune(subject)) < minimum || (!personOnly && !brainDumpContainsSubject(title, subject)) {
			continue
		}
		if match != nil {
			return nil
		}
		match = draft
	}
	return match
}

func brainDumpContainsSubject(title, subject string) bool {
	isASCIIWord := func(r rune) bool {
		return r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_'
	}
	for offset := 0; offset < len(title); {
		index := strings.Index(title[offset:], subject)
		if index < 0 {
			return false
		}
		start := offset + index
		end := start + len(subject)
		before, _ := utf8.DecodeLastRuneInString(title[:start])
		after, _ := utf8.DecodeRuneInString(title[end:])
		first, _ := utf8.DecodeRuneInString(subject)
		last, _ := utf8.DecodeLastRuneInString(subject)
		if (!isASCIIWord(first) || !isASCIIWord(before)) && (!isASCIIWord(last) || !isASCIIWord(after)) {
			return true
		}
		offset = start + 1
	}
	return false
}

// brainDumpProject resolves only an exact, unambiguous history id or name.
// Model-supplied paths never become execution folders.
func brainDumpProject(label string, projects []HistoryProject) (HistoryProject, bool) {
	label = strings.TrimSpace(label)
	if label == "" {
		return HistoryProject{}, false
	}
	var found HistoryProject
	matched := false
	for _, project := range projects {
		if !strings.EqualFold(label, project.ID) && !strings.EqualFold(label, project.Name) {
			continue
		}
		if matched {
			return HistoryProject{}, false
		}
		if !filepath.IsAbs(project.Path) {
			return HistoryProject{}, false
		}
		info, err := os.Stat(project.Path)
		if err != nil || !info.IsDir() {
			return HistoryProject{}, false
		}
		found, matched = project, true
	}
	return found, matched
}

const brainDumpProjectEvent = "list-project"

func brainDumpProjectChoiceID(project HistoryProject) string {
	sum := sha256.Sum256([]byte(project.ID + "\x00" + filepath.Clean(project.Path)))
	return "project_" + hex.EncodeToString(sum[:12])
}

func brainDumpProjectChoices(projects []HistoryProject) []handoff.Choice {
	projects = append([]HistoryProject(nil), projects...)
	sort.SliceStable(projects, func(i, j int) bool { return projects[i].LastAt > projects[j].LastAt })
	choices := []handoff.Choice{}
	seen := map[string]bool{}
	for _, project := range projects {
		resolved, ok := brainDumpProject(project.ID, projects)
		if !ok || strings.TrimSpace(project.Name) == "" {
			continue
		}
		id := brainDumpProjectChoiceID(resolved)
		if seen[id] {
			continue
		}
		seen[id] = true
		choices = append(choices, handoff.Choice{ID: id, Label: resolved.Name})
		if len(choices) == 4 {
			break
		}
	}
	return choices
}

func brainDumpChoiceProject(choiceID string, projects []HistoryProject) (HistoryProject, bool) {
	for _, choice := range brainDumpProjectChoices(projects) {
		if choice.ID == choiceID {
			for _, project := range projects {
				if brainDumpProjectChoiceID(project) == choiceID {
					return brainDumpProject(project.ID, projects)
				}
			}
		}
	}
	return HistoryProject{}, false
}

// persistBrainDumpSelection is the storage phase of adding checked tasks. The
// caller must finish scheduling and starting the selected work before it can
// report success; persistence alone is not a completed add.
func (a *App) persistBrainDumpSelection(ctx context.Context, key string, items []BrainDumpItem, source handoff.Source, now time.Time) ([]handoff.Entry, error) {
	if err := validateBrainDumpAdd(items, mobileTaskRunner(loadAIProviderConfig().Provider)); err != nil {
		return nil, err
	}
	if a == nil || a.handoffStore == nil {
		return nil, errTaskStoreNotReady
	}
	store := a.storeForMobile()
	if store == nil || key == "" {
		return nil, errors.New("the task store is not ready")
	}
	previous, err := store.GetUIState(ctx, brainDumpBatchKey(key))
	if err != nil {
		return nil, err
	}
	if previous == "" {
		for _, item := range items {
			if item.Who == "reminder" && item.When != "" {
				at, _ := time.Parse(time.RFC3339, item.When) // validated above
				if !at.After(now) {
					return nil, errors.New("the reminder time must be in the future")
				}
			}
		}
	}
	ids, err := reserveBrainDumpBatch(ctx, store, key, items)
	if err != nil {
		return nil, err
	}
	entries := make([]handoff.Entry, len(items))
	batchHash := sha256.Sum256([]byte(key))
	batchID := hex.EncodeToString(batchHash[:])
	var drafts []mobileDraftTask
	var projects []HistoryProject
	for _, item := range items {
		if item.Who == "draft" && drafts == nil {
			drafts, err = a.mobileDraftTasks(ctx, now)
			if err != nil {
				return nil, err
			}
		}
		if (item.Who == "claude_code" || item.Who == "codex") && projects == nil && !a.IsClosedEyesModeEnabled() {
			projects = a.GetAssistantHistory().Projects
		}
	}
	usedDrafts := make(map[string]bool)
	for i, item := range items {
		entries[i] = prepareBrainDumpEntry(item, ids[i], source, now)
		entries[i].ListItem.BatchID = batchID
		if item.Who == "claude_code" || item.Who == "codex" {
			if project, ok := brainDumpProject(item.Project, projects); ok {
				entries[i].Workdir = filepath.Clean(project.Path)
			} else {
				entries[i].Thread.Events = []handoff.Event{{ID: brainDumpProjectEvent, Kind: handoff.EventIsoform, At: now.UnixMilli(), Code: "list_project", Choices: brainDumpProjectChoices(projects)}}
			}
		}
		if draft := matchBrainDumpDraft(a, item, drafts); draft != nil && !usedDrafts[draft.TaskID] {
			entries[i].ListItem.DraftTaskID = draft.TaskID
			usedDrafts[draft.TaskID] = true
		}
	}
	return a.handoffStore.StageNewListBatch(entries)
}

// reserveBrainDumpBatch binds the request key to its exact selection before
// making any side effects. Stable task IDs permit recovery after a lost
// response without creating a second copy of the selected work.
func brainDumpBatchKey(requestKey string) string {
	hash := sha256.Sum256([]byte(requestKey))
	return "brain_dump_batch:" + hex.EncodeToString(hash[:])
}

func brainDumpPayloadHash(items []BrainDumpItem) (string, error) {
	encoded, err := json.Marshal(items)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:]), nil
}

func reserveBrainDumpBatch(ctx context.Context, store *storage.Store, requestKey string, items []BrainDumpItem) ([]string, error) {
	if store == nil || requestKey == "" {
		return nil, errors.New("the task store is not ready")
	}
	value, err := brainDumpPayloadHash(items)
	if err != nil {
		return nil, err
	}
	key := brainDumpBatchKey(requestKey)
	previous, err := store.ReserveUIStateValue(ctx, key, value)
	if err != nil {
		return nil, err
	}
	if previous != value {
		return nil, errors.New("this key was already used for a different task list")
	}
	ids := make([]string, len(items))
	for i, item := range items {
		sum := sha256.Sum256([]byte(requestKey + "\x00" + item.ID))
		ids[i] = "hf_" + hex.EncodeToString(sum[:16])
	}
	return ids, nil
}

// validateBrainDumpAdd checks the whole selection before any task is saved or
// started. Phone data is untrusted even when it originally came from parse.
func validateBrainDumpAdd(items []BrainDumpItem, runner string) error {
	if len(items) == 0 || len(items) > brainDumpLimit {
		return errors.New("select between 1 and 12 tasks")
	}
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		if !mobileValidID(item.ID) || seen[item.ID] {
			return errors.New("task ids must be valid and unique")
		}
		seen[item.ID] = true
		if strings.TrimSpace(item.Title) == "" || len(item.Title) > mobileChatBriefMax || strings.ContainsAny(item.Title, "\r\n") || len(item.Person) > 200 || len(item.Project) > 1024 || strings.ContainsAny(item.Person+item.Project, "\r\n") {
			return errors.New("invalid task fields")
		}
		if item.NoRunner && item.Who != "reminder" {
			return errors.New("invalid runner fallback")
		}
		switch item.Who {
		case "draft":
			if strings.TrimSpace(item.Person) == "" || item.When != "" {
				return errors.New("a draft needs a person and no reminder time")
			}
		case "claude_code", "codex":
			if item.Who != runner || item.When != "" {
				return errors.New("the task runner is not configured")
			}
		case "reminder":
			if item.When != "" {
				if _, err := time.Parse(time.RFC3339, item.When); err != nil {
					return errors.New("invalid reminder time")
				}
			}
		default:
			return errors.New("unknown task kind")
		}
	}
	return nil
}

// scheduleBrainDumpReminder uses the existing durable reminder delivery path.
// Untimed tasks stay on the list without inventing a notification time.
func (a *App) scheduleBrainDumpReminder(ctx context.Context, taskID string, item BrainDumpItem) (string, error) {
	if item.When == "" {
		return "", nil
	}
	if a == nil || a.engagement == nil || a.engagement.store == nil {
		return "", errors.New("reminders are not available")
	}
	at, err := time.Parse(time.RFC3339, item.When)
	if err != nil {
		return "", errors.New("invalid reminder time")
	}
	key := engagement.DedupeKey(engagement.KindReminder, "list", taskID, at.UTC().Format(time.RFC3339))
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("isoform:brain-dump-reminder:"+key)).String()
	existing, err := a.engagement.store.GetEngagementMoment(ctx, id)
	if err != nil {
		return "", err
	}
	if existing != nil {
		if existing.Kind != string(engagement.KindReminder) || existing.SubjectID != taskID || existing.DedupeKey != key || existing.State == storage.EngagementStateSuppressed || existing.State == storage.EngagementStateExpired {
			return "", errors.New("this reminder could not be scheduled")
		}
		return existing.ID, nil
	}
	if !at.After(a.engagement.now()) {
		return "", errors.New("the reminder time must be in the future")
	}
	row, err := a.engagement.Submit(ctx, engagement.Moment{
		ID: id, Kind: engagement.KindReminder, SubjectID: taskID, DedupeKey: key,
		NotBefore: at, ExpiresAt: at.Add(activityReminderGrace), Stakes: 0.9,
		Params:   map[string]string{"title": engagementTitle(item.Title), "_list_task": taskID},
		Channels: []engagement.Channel{engagement.ChannelDesktop, engagement.ChannelPush},
	})
	if err != nil {
		return "", err
	}
	if row.State == storage.EngagementStateSuppressed {
		return "", errors.New("this reminder could not be scheduled")
	}
	return row.ID, nil
}

// prepareBrainDumpReminders creates the durable reminders while the batch is
// still hidden. If a later item fails, existing reminders remain held and a
// retry reuses them. The caller publishes only after every item is prepared.
func (a *App) prepareBrainDumpReminders(ctx context.Context, entries []handoff.Entry, items []BrainDumpItem) error {
	if len(entries) != len(items) {
		return errors.New("the task list changed")
	}
	for i, item := range items {
		encoded, err := json.Marshal(item)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(encoded)
		if entries[i].ListItem == nil || entries[i].ListItem.Fingerprint != hex.EncodeToString(sum[:]) {
			return errors.New("the task list changed")
		}
	}
	for i, item := range items {
		if item.Who == "reminder" {
			if _, err := a.scheduleBrainDumpReminder(ctx, entries[i].ID, item); err != nil {
				return err
			}
		}
	}
	return nil
}

// stageBrainDumpSelection prepares the saved tasks and their reminders without
// making any of the selection visible. The add operation still has to prepare
// the other kinds and start their work before it can report success.
func (a *App) stageBrainDumpSelection(ctx context.Context, key string, items []BrainDumpItem, source handoff.Source, now time.Time) ([]handoff.Entry, error) {
	entries, err := a.persistBrainDumpSelection(ctx, key, items, source, now)
	if err != nil {
		return nil, err
	}
	if err := a.prepareBrainDumpReminders(ctx, entries, items); err != nil {
		return nil, err
	}
	return entries, nil
}

// addBrainDumpPrepared publishes the checked selection before starting any
// resolved code handoffs through the same path as the phone's Do it action.
func (bridge *mobileBridge) addBrainDumpPrepared(ctx context.Context, key string, items []BrainDumpItem, source handoff.Source, now time.Time) ([]string, error) {
	a := bridge.app
	var drafts []mobileDraftTask
	usedDrafts := make(map[string]bool)
	matchedIDs := make([]string, len(items))
	for i, item := range items {
		switch item.Who {
		case "reminder":
		case "draft":
			if drafts == nil {
				var err error
				drafts, err = a.mobileDraftTasks(ctx, now)
				if err != nil {
					return nil, err
				}
			}
			matched := matchBrainDumpDraft(a, item, drafts)
			if matched == nil || usedDrafts[matched.TaskID] {
				continue
			}
			usedDrafts[matched.TaskID] = true
			matchedIDs[i] = matched.TaskID
		case "claude_code", "codex":
			if mobileTaskRunner(loadAIProviderConfig().Provider) != item.Who {
				return nil, errors.New("the task runner is not configured")
			}
		default:
			return nil, errors.New("this task kind is not available yet")
		}
	}
	entries, err := a.stageBrainDumpSelection(ctx, key, items, source, now)
	if err != nil {
		return nil, err
	}
	for i, item := range items {
		if item.Who == "draft" && matchedIDs[i] != "" && (entries[i].ListItem == nil || entries[i].ListItem.DraftTaskID != matchedIDs[i]) {
			return nil, errors.New("a checked reply draft is no longer available")
		}
	}
	if err := a.handoffStore.PublishListBatch(entries); err != nil {
		return nil, err
	}
	ids := make([]string, len(entries))
	for i, entry := range entries {
		ids[i] = entry.ID
		if current, err := a.handoffStore.Get(entry.ID); err == nil {
			if listHandoffBackend(&current) != "" && current.Status == handoff.StatusActive && current.Workdir != "" {
				if mobileTaskRunner(loadAIProviderConfig().Provider) != current.ListItem.Who {
					a.noteBrainDumpStartFailure(current.ID, now)
				} else if _, err := bridge.startHandoff(current.ID); err != nil {
					if latest, getErr := a.handoffStore.Get(current.ID); getErr != nil || latest.Status == handoff.StatusActive {
						a.noteBrainDumpStartFailure(current.ID, now)
					}
				}
			}
			current, _ = a.handoffStore.Get(entry.ID)
			a.emitBrowserEvent("handoff:updated", map[string]any{"id": entry.ID, "status": string(current.Status)})
		}
	}
	return ids, nil
}

// reconcileBrainDumpDrafts links only one unambiguous, still source-checked
// draft to each open list request. No message is sent during reconciliation.
func (a *App) reconcileBrainDumpDrafts(ctx context.Context, now time.Time) error {
	brainDumpDraftLinkMu.Lock()
	defer brainDumpDraftLinkMu.Unlock()
	entries, err := a.handoffStore.List()
	if err != nil {
		return err
	}
	drafts, err := a.mobileDraftTasks(ctx, now)
	if err != nil {
		return err
	}
	used := map[string]bool{}
	for _, entry := range entries {
		if entry.ListItem != nil && entry.ListItem.DraftTaskID != "" {
			used[entry.ListItem.DraftTaskID] = true
		}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].CreatedAt < entries[j].CreatedAt })
	for _, entry := range entries {
		if entry.Status != handoff.StatusActive || entry.ListItem == nil || entry.ListItem.Who != "draft" || entry.ListItem.DraftTaskID != "" {
			continue
		}
		item := BrainDumpItem{Who: "draft", Person: entry.ListItem.Person, Title: entry.ListItem.Original}
		match := matchBrainDumpDraft(a, item, drafts)
		if match == nil || used[match.TaskID] {
			continue
		}
		saved, err := a.handoffStore.MutateIfStatus(entry.ID, []handoff.Status{handoff.StatusActive}, func(current *handoff.Entry) {
			if current.ListItem != nil && current.ListItem.DraftTaskID == "" {
				current.ListItem.DraftTaskID = match.TaskID
			}
		})
		if err != nil {
			return err
		}
		if saved.ListItem != nil && saved.ListItem.DraftTaskID == match.TaskID {
			used[match.TaskID] = true
			a.emitBrowserEvent("handoff:updated", map[string]any{"id": saved.ID, "status": string(saved.Status)})
		}
	}
	return nil
}

func (a *App) noteBrainDumpStartFailure(id string, now time.Time) {
	_, _ = a.handoffStore.MutateThread(id, func(thread *handoff.Thread) {
		for _, event := range thread.Events {
			if event.Code == "list_start_failed" {
				return
			}
		}
		thread.AppendEvent(handoff.Event{ID: "list-start-failed", Kind: handoff.EventIsoform, Code: "list_start_failed", At: now.UnixMilli()})
	})
}

// chooseBrainDumpProject persists the selected current history folder before
// invoking the ordinary phone handoff start. The surrounding choice lock
// serializes retries and different taps.
func (bridge *mobileBridge) chooseBrainDumpProject(w http.ResponseWriter, entry handoff.Entry, choiceID string, now int64) {
	a := bridge.app
	if a.IsClosedEyesModeEnabled() || mobileTaskRunner(loadAIProviderConfig().Provider) != entry.ListItem.Who {
		mobileJSON(w, http.StatusConflict, nil, "the task runner is not available")
		return
	}
	project, ok := brainDumpChoiceProject(choiceID, a.GetAssistantHistory().Projects)
	if !ok {
		mobileJSON(w, http.StatusConflict, nil, "that project is no longer available")
		return
	}
	if entry.Status == handoff.StatusInProgress && entry.Workdir == filepath.Clean(project.Path) {
		bridge.writeTaskDetail(w, entry)
		return
	}
	var choiceErr error
	saved, err := a.handoffStore.MutateIfStatus(entry.ID, []handoff.Status{handoff.StatusActive}, func(e *handoff.Entry) {
		thread := ensureThread(e)
		for _, event := range thread.Events {
			if event.ID == brainDumpProjectEvent && event.ChosenID == choiceID && e.Workdir == filepath.Clean(project.Path) {
				return
			}
		}
		_, choiceErr = thread.Choose(brainDumpProjectEvent, choiceID, now, uuid.NewString())
		if choiceErr != nil {
			return
		}
		e.Workdir = filepath.Clean(project.Path)
		thread.AppendEvent(handoff.Event{ID: "list-start", Kind: handoff.EventIsoform, At: now, Code: "list_start"})
	})
	if err != nil || choiceErr != nil {
		mobileJSON(w, http.StatusConflict, nil, "that choice is no longer open")
		return
	}
	if _, err := bridge.startHandoff(saved.ID); err != nil {
		a.noteBrainDumpStartFailure(saved.ID, time.UnixMilli(now))
		mobileJSON(w, http.StatusServiceUnavailable, nil, "the task could not start; try again")
		return
	}
	saved, err = a.handoffStore.Get(saved.ID)
	if err != nil {
		mobileJSON(w, http.StatusServiceUnavailable, nil, "the task could not be read")
		return
	}
	bridge.writeTaskDetail(w, saved)
}

func (bridge *mobileBridge) serveBrainDumpAdd(w http.ResponseWriter, r *http.Request, device mobileDeviceRecord) {
	var input struct {
		IdempotencyKey string          `json:"idempotencyKey"`
		Items          []BrainDumpItem `json:"items"`
	}
	if !mobileDecode(w, r, &input) {
		return
	}
	key, valid := mobileKey(device.DeviceID, input.IdempotencyKey)
	if !valid {
		mobileJSON(w, http.StatusBadRequest, nil, "an idempotency key of 1 to 128 bytes is required")
		return
	}
	if bridge.app == nil {
		mobileJSON(w, http.StatusServiceUnavailable, nil, errMobileStoreUnavailable.Error())
		return
	}
	store := bridge.app.storeForMobile()
	if store == nil {
		mobileJSON(w, http.StatusServiceUnavailable, nil, errMobileStoreUnavailable.Error())
		return
	}
	prior, err := store.GetUIState(r.Context(), brainDumpBatchKey(key))
	if err != nil {
		mobileJSON(w, http.StatusServiceUnavailable, nil, errMobileStoreUnavailable.Error())
		return
	}
	hash, err := brainDumpPayloadHash(input.Items)
	if err != nil || (prior != "" && prior != hash) {
		mobileJSON(w, http.StatusConflict, nil, "this key was already used for a different task list")
		return
	}
	bridge.runReceipted(w, device, key, r.Method+" "+r.URL.Path, func(w http.ResponseWriter) {
		if err := validateBrainDumpAdd(input.Items, mobileTaskRunner(loadAIProviderConfig().Provider)); err != nil {
			mobileJSON(w, http.StatusBadRequest, nil, err.Error())
			return
		}
		ids, err := bridge.addBrainDumpPrepared(r.Context(), key, input.Items, handoff.SourceMobile, time.Now())
		if err != nil {
			mobileJSON(w, http.StatusConflict, nil, err.Error())
			return
		}
		mobileJSON(w, http.StatusCreated, map[string][]string{"ids": ids}, "")
	})
}

// brainDumpReminderSource binds a notification to the person's actual list
// task. It can inspect a staged task for preparation; delivery is held until
// the batch is published.
func (a *App) brainDumpReminderSource(ctx context.Context, m engagement.Moment) (handoff.Entry, bool, error) {
	if a == nil || a.handoffStore == nil || ctx.Err() != nil || m.Kind != engagement.KindReminder || m.Params["_list_task"] != m.SubjectID || m.SubjectID == "" {
		return handoff.Entry{}, false, errors.New("list reminder source is unavailable")
	}
	entry, published, err := a.handoffStore.ListBatchEntry(m.SubjectID)
	if err != nil {
		return handoff.Entry{}, false, err
	}
	if entry.Status != handoff.StatusActive || entry.ListItem.Who != "reminder" || entry.ListItem.Original == "" {
		return handoff.Entry{}, published, errors.New("list reminder is no longer active")
	}
	at, err := time.Parse(time.RFC3339, entry.ListItem.When)
	if err != nil || m.DedupeKey != engagement.DedupeKey(engagement.KindReminder, "list", entry.ID, at.UTC().Format(time.RFC3339)) || !engagementParamsMatch(m, map[string]string{"title": engagementTitle(entry.ListItem.Original)}, "title") {
		return handoff.Entry{}, published, errors.New("list reminder changed")
	}
	return entry, published, nil
}

func (a *App) holdStagedBrainDumpReminder(ctx context.Context, m engagement.Moment) (bool, error) {
	if m.Kind != engagement.KindReminder || m.Params["_list_task"] == "" || m.Params["_list_task"] != m.SubjectID {
		return false, nil
	}
	if a == nil || a.handoffStore == nil || ctx.Err() != nil {
		return false, errors.New("list reminder store is unavailable")
	}
	_, published, err := a.handoffStore.ListBatchEntry(m.SubjectID)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return !published, nil
}

// Reject the entire response on invalid input, so a partial model answer
// cannot silently drop or alter tasks.
func decodeBrainDump(raw, runner string, loc *time.Location) ([]BrainDumpItem, error) {
	var result BrainDumpResult
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&result); err != nil {
		return nil, err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, errors.New("unexpected content after task list")
	}
	if len(result.Items) == 0 || len(result.Items) > brainDumpLimit {
		return nil, errors.New("invalid task count")
	}
	for i := range result.Items {
		item := &result.Items[i]
		item.NoRunner = false
		item.Title = strings.TrimSpace(item.Title)
		item.Person = strings.TrimSpace(item.Person)
		item.Project = strings.TrimSpace(item.Project)
		if item.Title == "" || len(item.Title) > mobileTaskTitleMax || strings.ContainsAny(item.Title, "\r\n") || len(item.Person) > 200 || len(item.Project) > 1024 || strings.ContainsAny(item.Person+item.Project, "\r\n") {
			return nil, errors.New("invalid task fields")
		}
		switch item.Who {
		case "code":
			if item.When != "" {
				return nil, errors.New("code work cannot have a reminder time")
			}
			if runner == "" {
				item.Who, item.NoRunner, item.Project = "reminder", true, ""
			} else {
				item.Who = runner
			}
		case "draft":
			if item.Person == "" || item.When != "" {
				return nil, errors.New("draft requires a person")
			}
		case "claude_code", "codex":
			if item.Who != runner || item.When != "" {
				return nil, errors.New("task runner is not configured")
			}
		case "reminder":
			if item.When != "" {
				at, err := time.Parse(time.RFC3339, item.When)
				if err != nil {
					return nil, errors.New("invalid reminder time")
				}
				item.When = at.In(loc).Format(time.RFC3339)
			}
		default:
			return nil, errors.New("invalid task kind")
		}
		item.ID = uuid.NewString()
	}
	return result.Items, nil
}

func brainDumpFallback(text string) []BrainDumpItem {
	items := make([]BrainDumpItem, 0, brainDumpLimit)
	for _, line := range strings.Split(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		items = append(items, BrainDumpItem{ID: uuid.NewString(), Title: line, Who: "reminder"})
		if len(items) == brainDumpLimit {
			break
		}
	}
	return items
}

func parseBrainDumpResponse(text, raw, runner string, loc *time.Location) []BrainDumpItem {
	items, err := decodeBrainDump(raw, runner, loc)
	if err != nil {
		return brainDumpFallback(text)
	}
	return items
}

// ParseBrainDump uses the same tool-free ephemeral route as other one-shot calls.
func (a *App) ParseBrainDump(text string) (BrainDumpResult, error) {
	return a.parseBrainDump(context.Background(), text)
}

// AddBrainDump is the desktop binding for the same checked-list operation.
func (a *App) AddBrainDump(key string, items []BrainDumpItem) ([]string, error) {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 128 {
		return nil, errors.New("an idempotency key of 1 to 128 bytes is required")
	}
	if err := validateBrainDumpAdd(items, mobileTaskRunner(loadAIProviderConfig().Provider)); err != nil {
		return nil, err
	}
	bridge, err := a.desktopTasks()
	if err != nil {
		return nil, err
	}
	return bridge.addBrainDumpPrepared(bridge.ctx, "desktop:"+key, items, handoff.SourceDesktop, time.Now())
}

func (a *App) parseBrainDump(ctx context.Context, text string) (BrainDumpResult, error) {
	text = strings.TrimSpace(text)
	if text == "" || len(text) > mobileChatBriefMax {
		return BrainDumpResult{}, errors.New("the list must be between 1 byte and 8 KiB")
	}
	lines := 0
	for _, line := range strings.Split(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines++
		}
	}
	if lines > brainDumpLimit {
		return BrainDumpResult{}, errors.New("the list must contain at most 12 lines")
	}
	runner := mobileTaskRunner(loadAIProviderConfig().Provider)
	now := time.Now()
	encoded, _ := json.Marshal(text)
	type projectChoice struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	projects := []projectChoice{}
	if !a.IsClosedEyesModeEnabled() {
		for _, project := range a.GetAssistantHistory().Projects {
			projects = append(projects, projectChoice{ID: project.ID, Name: project.Name})
		}
	}
	encodedProjects, _ := json.Marshal(projects)
	prompt := fmt.Sprintf("Turn the quoted list into at most 12 tasks, one per idea. Return only strict JSON: {\"items\":[{\"title\":\"...\",\"who\":\"draft|code|reminder\",\"when\":\"optional RFC3339\",\"project\":\"optional\",\"person\":\"optional\"}]}. Do not include an id. Use draft only for a message to a named person and include person. Use code for work a coding assistant should do. Use reminder for the person's own tasks; omit when if no time is stated. For code work set project to a project ID only when the user's list clearly identifies one of these recent projects: %s; otherwise omit it. Use this local time and offset for relative dates: %s. Do not invent details. List: %s", encodedProjects, now.Format(time.RFC3339), encoded)
	raw, err := a.runEphemeralClaudeModelContext(ctx, prompt, a.lightTaskModel(), 60*time.Second)
	if err != nil {
		if ctx.Err() != nil {
			return BrainDumpResult{}, ctx.Err()
		}
		raw = ""
	}
	return BrainDumpResult{Items: parseBrainDumpResponse(text, raw, runner, now.Location())}, nil
}

func (bridge *mobileBridge) serveBrainDumpParse(w http.ResponseWriter, r *http.Request, device mobileDeviceRecord) {
	var input struct {
		IdempotencyKey string `json:"idempotencyKey"`
		Text           string `json:"text"`
	}
	if !mobileDecode(w, r, &input) {
		return
	}
	key, valid := mobileKey(device.DeviceID, input.IdempotencyKey)
	if !valid {
		mobileJSON(w, http.StatusBadRequest, nil, "an idempotency key of 1 to 128 bytes is required")
		return
	}
	bridge.runReceipted(w, device, key, r.Method+" "+r.URL.Path, func(w http.ResponseWriter) {
		result, err := bridge.app.parseBrainDump(r.Context(), input.Text)
		if err != nil {
			mobileJSON(w, http.StatusBadRequest, nil, err.Error())
			return
		}
		mobileJSON(w, http.StatusOK, result, "")
	})
}
