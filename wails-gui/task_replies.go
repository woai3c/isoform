package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"wails-gui/internal/handoff"
)

// Replies while a task runs (tasks-review v1.0.5–v1.0.7) and the steps of a
// stopped run whose effect left it (v1.0.2 §1). The store keeps codes; the
// app owns every word.

const (
	mobileTaskActionReplyUndo     = "reply_undo"
	mobileTaskActionReplyLeaveOut = "reply_leave_out"
	mobileTaskActionReplyUse      = "reply_use"
	mobileTaskActionReplyUseAgain = "reply_use_again"
	mobileTaskActionReplyChange   = "reply_change"
	mobileTaskActionStepSkip      = "step_skip"
	mobileTaskActionStepRedo      = "step_redo"
	mobileTaskActionStepChange    = "step_change"
	mobileTaskActionRunEnded      = "run_ended"
)

// Error texts the apps match on; the words they show are their own.
const (
	taskReplyPickedUpMessage       = "it was already picked up"
	taskRestartNeedsAnswersMessage = "choose for each one above, then start again"
	taskOldRunLiveMessage          = "couldn't confirm the last run ended"
)

var (
	errTaskRestartNeedsAnswers = errors.New(taskRestartNeedsAnswersMessage)
	errTaskOldRunLive          = errors.New(taskOldRunLiveMessage)
)

// MobileTaskReply is one queued reply as both apps read it.
type MobileTaskReply struct {
	ID         string `json:"id"`
	Text       string `json:"text"`
	SentAt     string `json:"sentAt"`
	State      string `json:"state"`
	PickedUpAt string `json:"pickedUpAt,omitempty"`
	LeftOut    bool   `json:"leftOut,omitempty"`
	Choice     string `json:"choice,omitempty"`
	// LeftOutOfRun marks a reply the running attempt started without.
	LeftOutOfRun bool `json:"leftOutOfRun,omitempty"`
}

// MobileTaskEffectStep is one step of a stopped run whose effect left the run.
type MobileTaskEffectStep struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Name   string `json:"name,omitempty"`
	Target string `json:"target,omitempty"`
	At     string `json:"at"`
	App    string `json:"app,omitempty"`
	Proof  string `json:"proof"`
	Answer string `json:"answer,omitempty"`
}

func mobileTaskReplies(entry handoff.Entry) []MobileTaskReply {
	if entry.Thread == nil || len(entry.Thread.Replies) == 0 {
		return nil
	}
	replies := append([]handoff.Reply(nil), entry.Thread.Replies...)
	sort.SliceStable(replies, func(i, j int) bool { return replies[i].SentAt < replies[j].SentAt })
	out := make([]MobileTaskReply, 0, len(replies))
	for _, r := range replies {
		state := r.State
		if state == handoff.ReplyTaking {
			// Handed to the run, not confirmed: still waiting to the person.
			state = handoff.ReplyWaiting
		}
		out = append(out, MobileTaskReply{
			ID: r.ID, Text: r.Text, SentAt: mobileTaskTime(r.SentAt), State: state,
			PickedUpAt: mobileTaskTime(r.PickedUpAt), LeftOut: r.LeftOut, Choice: r.Choice,
			LeftOutOfRun: r.LeftOutOf != "" && r.LeftOutOf == entry.AttemptID && entry.Status == handoff.StatusInProgress,
		})
	}
	return out
}

func mobileTaskEffectSteps(steps []handoff.EffectStep) []MobileTaskEffectStep {
	if len(steps) == 0 {
		return nil
	}
	out := make([]MobileTaskEffectStep, 0, len(steps))
	for _, step := range steps {
		out = append(out, MobileTaskEffectStep{ID: step.ID, Kind: step.Kind, Name: step.Name, Target: step.Target,
			At: mobileTaskTime(step.At), App: step.App, Proof: step.Proof, Answer: step.Answer})
	}
	return out
}

// taskStoppedAwaitingAnswers counts what a stopped task's Review asks for.
func taskStoppedAwaitingAnswers(entry handoff.Entry) (steps, replies int) {
	if group, _ := handoff.Placement(entry); group != handoff.BoardNeedsYou || entry.Status == handoff.StatusActive {
		return 0, 0
	}
	return entry.Thread.StepsNeedingCheck(), entry.Thread.RepliesNeedingChoice()
}

// queueTaskReply saves a reply sent while the task runs. It is saved before
// the answer says Waiting, and the runner reads it at its next turn.
func (bridge *mobileBridge) queueTaskReply(w http.ResponseWriter, entry handoff.Entry, text, key string) {
	app := bridge.app
	now := mobileGoalsNow().UnixMilli()
	var queueErr error
	saved, err := app.handoffStore.MutateIfStatus(entry.ID, []handoff.Status{entry.Status}, func(e *handoff.Entry) {
		_, queueErr = ensureThread(e).QueueReply(handoff.Reply{ID: uuid.NewString(), Text: text, SentAt: now, Key: key})
		e.Thread.OpenedAt = now
	})
	if queueErr != nil {
		mobileJSON(w, http.StatusConflict, nil, "too many replies are waiting on this task")
		return
	}
	if err != nil {
		mobileJSON(w, http.StatusConflict, nil, "this task changed; refresh and try again")
		return
	}
	if app.telemetry != nil {
		app.telemetry.Capture("tasks.action", map[string]any{"action": "reply_queued", "outcome": "queued"})
	}
	app.emitBrowserEvent("handoff:updated", map[string]any{"id": entry.ID, "status": string(saved.Status)})
	bridge.writeTaskDetail(w, saved)
}

func taskReplyAction(action string) bool {
	switch action {
	case mobileTaskActionReplyUndo, mobileTaskActionReplyLeaveOut, mobileTaskActionReplyUse, mobileTaskActionReplyUseAgain,
		mobileTaskActionReplyChange, mobileTaskActionStepSkip, mobileTaskActionStepRedo, mobileTaskActionStepChange, mobileTaskActionRunEnded:
		return true
	}
	return false
}

// performTaskReplyAction answers Undo, Leave it out, Use it, Use it again,
// Change, Skip it, Do it again and the old-run check. None of them moves the
// task's update time.
func (bridge *mobileBridge) performTaskReplyAction(w http.ResponseWriter, entry handoff.Entry, input taskActionInput) {
	app := bridge.app
	if input.Action == mobileTaskActionRunEnded {
		app.handoffActionMu.Lock()
		live := app.handoffOldRunLiveLocked(entry)
		app.handoffActionMu.Unlock()
		if live {
			mobileJSON(w, http.StatusConflict, nil, taskOldRunLiveMessage)
			return
		}
		bridge.writeTaskDetail(w, entry)
		return
	}
	switch input.Action {
	case mobileTaskActionReplyLeaveOut, mobileTaskActionReplyUse, mobileTaskActionReplyUseAgain, mobileTaskActionReplyChange,
		mobileTaskActionStepSkip, mobileTaskActionStepRedo, mobileTaskActionStepChange:
		// These shape the next run, so they wait for the task to stop.
		if entry.Status == handoff.StatusInProgress {
			mobileJSON(w, http.StatusConflict, nil, "this task changed; refresh and try again")
			return
		}
	}
	telemetry := ""
	var actionErr error
	saved, err := app.handoffStore.MutateThread(entry.ID, func(thread *handoff.Thread) {
		id := input.EventID
		switch input.Action {
		case mobileTaskActionReplyUndo:
			_, actionErr = thread.WithdrawReply(id)
			telemetry = "undone"
		case mobileTaskActionReplyLeaveOut:
			if r, ok := thread.Reply(id); ok && r.State == handoff.ReplyUncertain {
				actionErr = thread.SetReplyChoice(id, handoff.ReplyChoiceLeftOut)
				telemetry = "reply_uncertain_left_out"
			} else {
				actionErr = thread.SetReplyLeftOut(id, true)
				telemetry = "reply_left_out"
			}
		case mobileTaskActionReplyUse:
			actionErr = thread.SetReplyLeftOut(id, false)
			telemetry = "reply_used_again"
		case mobileTaskActionReplyUseAgain:
			actionErr = thread.SetReplyChoice(id, handoff.ReplyChoiceUseAgain)
			telemetry = "reply_uncertain_used_again"
		case mobileTaskActionReplyChange:
			actionErr = thread.SetReplyChoice(id, "")
		case mobileTaskActionStepSkip:
			actionErr = thread.AnswerStep(id, handoff.StepSkip)
			telemetry = "skipped_step"
		case mobileTaskActionStepRedo:
			actionErr = thread.AnswerStep(id, handoff.StepRedo)
			telemetry = "redid_step"
		case mobileTaskActionStepChange:
			actionErr = thread.AnswerStep(id, "")
		}
	})
	if errors.Is(actionErr, handoff.ErrReplyPickedUp) {
		mobileJSON(w, http.StatusConflict, nil, taskReplyPickedUpMessage)
		return
	}
	if actionErr != nil || err != nil {
		mobileJSON(w, http.StatusConflict, nil, "this task changed; refresh and try again")
		return
	}
	if app.telemetry != nil && telemetry != "" {
		if input.Action == mobileTaskActionReplyUndo {
			app.telemetry.Capture("tasks.action", map[string]any{"action": "reply_queued", "outcome": telemetry})
		} else {
			app.telemetry.Capture("tasks.action", map[string]any{"action": telemetry})
		}
	}
	app.emitBrowserEvent("handoff:updated", map[string]any{"id": entry.ID, "status": string(saved.Status)})
	bridge.writeTaskDetail(w, saved)
}

// handoffOldRunLiveLocked reports whether an earlier run of the task may still
// be working: a turn live in its chat or a run this process still tracks. The
// caller holds handoffActionMu.
func (a *App) handoffOldRunLiveLocked(entry handoff.Entry) bool {
	if a.handoffRuns[entry.ID] != nil {
		return true
	}
	return a.handoffTurnLive(entry.ChatSessionID)
}

// handoffRestartGate is Start again's wait (v1.0.2 §1, v1.0.7 §3): every
// unknown step and every uncertain reply needs an answer, and the old run must
// be confirmed ended. The caller holds handoffActionMu.
func (a *App) handoffRestartGate(entry handoff.Entry) error {
	switch entry.Status {
	case handoff.StatusBlocked, handoff.StatusNeedsReview, handoff.StatusAborted:
	default:
		return nil
	}
	if entry.Thread.RepliesNeedingChoice() > 0 || entry.Thread.StepsNeedingAnswer() > 0 {
		return errTaskRestartNeedsAnswers
	}
	if a.handoffOldRunLiveLocked(entry) {
		return errTaskOldRunLive
	}
	return nil
}

// prepareHandoffRestart shapes a fresh run inside the claim: the replies it
// uses are handed to the new attempt, left-out ones are marked, and the stopped
// run's steps become the ones to skip. It returns the started-again params.
func prepareHandoffRestart(e *handoff.Entry, withReplies bool) map[string]string {
	thread := ensureThread(e)
	if withReplies {
		use, left := thread.RestartReplies()
		thread.TakeReplies(handoffReplyIDs(use), e.AttemptID)
		thread.MarkLeftOutOf(handoffReplyIDs(left), e.AttemptID)
	}
	skip, _ := thread.RestartSteps()
	thread.Skipped, thread.Steps = skip, nil
	var chosen []handoff.EffectStep
	for _, step := range skip {
		if step.Answer == handoff.StepSkip {
			chosen = append(chosen, step)
		}
	}
	switch len(chosen) {
	case 0:
		return nil
	case 1:
		return map[string]string{"skipped": handoffStepLabel(chosen[0]), "skippedCount": "1"}
	}
	return map[string]string{"skippedCount": strconv.Itoa(len(chosen))}
}

func handoffReplyIDs(replies []handoff.Reply) []string {
	ids := make([]string, 0, len(replies))
	for _, r := range replies {
		ids = append(ids, r.ID)
	}
	return ids
}

// handoffTakenReplies are the replies handed to the attempt, in order.
func handoffTakenReplies(entry handoff.Entry) []handoff.Reply {
	if entry.Thread == nil {
		return nil
	}
	var out []handoff.Reply
	for _, r := range entry.Thread.Replies {
		if r.State == handoff.ReplyTaking && r.Attempt == entry.AttemptID {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].SentAt < out[j].SentAt })
	return out
}

// handoffRestartPrompt is a fresh run's input: the task, every reply it uses,
// and the steps it must not repeat.
func handoffRestartPrompt(body string, replies []handoff.Reply, skip []handoff.EffectStep) string {
	prompt := strings.TrimRight(body, "\n")
	if len(replies) > 0 {
		prompt += "\n\n## Notes from you\n"
		for _, r := range replies {
			prompt += "\n" + r.Text + "\n"
		}
	}
	if len(skip) > 0 {
		prompt += "\n\n## Already handled\n\nThese steps already happened or the person chose to skip them. Do not do them again:\n"
		for _, step := range skip {
			prompt += "\n- " + handoffStepLabel(step)
		}
		prompt += "\n"
	}
	return prompt
}

// handoffStepLabel is a step's plain name, or what it acted on.
func handoffStepLabel(step handoff.EffectStep) string {
	if name := strings.TrimSpace(step.Name); name != "" {
		return name
	}
	return strings.TrimSpace(step.Target)
}

// confirmHandoffRestartReplies records the new run's confirmation of the
// replies it was given.
func (a *App) confirmHandoffRestartReplies(id string, seq int64) {
	entry, err := a.handoffStore.Get(id)
	if err != nil {
		return
	}
	ids := handoffReplyIDs(handoffTakenReplies(entry))
	if len(ids) == 0 {
		return
	}
	now := time.Now().UnixMilli()
	if _, err := a.handoffStore.MutateThread(id, func(thread *handoff.Thread) { thread.ConfirmReplies(ids, entry.AttemptID, now, seq) }); err != nil {
		log.Printf("[handoff] restart: reply pickup not recorded: %v", err)
		return
	}
	if a.telemetry != nil {
		a.telemetry.Capture("tasks.action", map[string]any{"action": "reply_queued", "outcome": "picked_up"})
	}
}

// taskReplyIdleWait bounds how long a turn's end waits for its chat to settle
// before taking the waiting replies.
var taskReplyIdleWait = 2 * time.Second

// handoffSettleHook runs just before a finished run settles; tests use it to
// land a reply while the run is being checked.
var handoffSettleHook func(id string)

// pickUpTaskReplies is the runner's turn boundary: a run that ended with
// replies waiting starts its next turn with them at once, and each is picked
// up only once that turn has them. It reports whether a turn started, and
// whether any reply was waiting to try.
func (a *App) pickUpTaskReplies(current handoff.Entry) (started, tried bool) {
	if a == nil || a.handoffStore == nil || strings.TrimSpace(current.ChatSessionID) == "" {
		return false, false
	}
	entry, err := a.handoffStore.Get(current.ID)
	if err != nil || entry.Status != handoff.StatusInProgress || entry.AttemptID != current.AttemptID || entry.Thread == nil {
		return false, false
	}
	waiting := entry.Thread.WaitingReplies()
	if len(waiting) == 0 {
		return false, false
	}
	return a.startTaskRepliesTurn(entry, waiting), true
}

func (a *App) startTaskRepliesTurn(entry handoff.Entry, waiting []handoff.Reply) bool {
	bridge, err := a.desktopTasks()
	if err != nil {
		return false
	}
	for deadline := time.Now().Add(taskReplyIdleWait); bridge.chatRunning(entry.ChatSessionID) && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}
	store := a.storeForMobile()
	if store == nil {
		return false
	}
	row, found, err := store.MobileChat(bridge.ctx, entry.ChatSessionID)
	if err != nil || !found {
		return false
	}
	ids := handoffReplyIDs(waiting)
	texts := make([]string, 0, len(waiting))
	for _, r := range waiting {
		texts = append(texts, r.Text)
	}
	if _, err := a.handoffStore.MutateIfAttempt(entry.ID, entry.AttemptID, []handoff.Status{handoff.StatusInProgress}, func(e *handoff.Entry) {
		ensureThread(e).TakeReplies(ids, e.AttemptID)
	}); err != nil {
		return false
	}
	// The ended turn's run state goes, so the next turn can register its own.
	a.handoffActionMu.Lock()
	if run := a.handoffRuns[entry.ID]; run != nil && run.attemptID == entry.AttemptID {
		delete(a.handoffRuns, entry.ID)
	}
	a.handoffActionMu.Unlock()
	_, seq, err := bridge.startChatTurn(row, strings.Join(texts, "\n\n"), nil)
	if err != nil {
		log.Printf("[handoff] waiting replies not picked up: %v", err)
		a.handoffStore.MutateThread(entry.ID, func(thread *handoff.Thread) { thread.ReturnReplies(ids, entry.AttemptID) })
		return false
	}
	now := time.Now().UnixMilli()
	saved, err := a.handoffStore.MutateThread(entry.ID, func(thread *handoff.Thread) { thread.ConfirmReplies(ids, entry.AttemptID, now, seq) })
	if err != nil {
		return true
	}
	if a.telemetry != nil {
		for _, r := range waiting {
			a.telemetry.Capture("tasks.action", map[string]any{"action": "reply_queued", "outcome": "picked_up", "ms_to_pickup": now - r.SentAt})
		}
	}
	a.emitBrowserEvent("handoff:updated", map[string]any{"id": entry.ID, "status": string(saved.Status)})
	return true
}

// taskRepliesStopSentence is the sentence a stop by Isoform gains while
// replies wait (v1.0.5 §4, v1.0.7 §2), for the phone's wording.
func taskRepliesStopSentence(entry handoff.Entry, zh bool) string {
	if entry.Thread == nil || entry.StoppedBy != handoff.StoppedBySystem {
		return ""
	}
	uncertain, waiting := 0, 0
	for _, r := range entry.Thread.Replies {
		switch r.State {
		case handoff.ReplyUncertain:
			uncertain++
		case handoff.ReplyWaiting:
			waiting++
		}
	}
	switch {
	case uncertain == 1 && zh:
		return "我没法确认停下之前它有没有照你的一条回复做。请选择要不要再用一次。"
	case uncertain == 1:
		return "I can't tell whether it acted on one of your replies before it stopped. Choose whether to use it again."
	case uncertain > 1 && zh:
		return "我没法确认停下之前它有没有照你的 " + strconv.Itoa(uncertain) + " 条回复做。请逐条选择要不要再用一次。"
	case uncertain > 1:
		return "I can't tell whether it acted on " + strconv.Itoa(uncertain) + " of your replies before it stopped. Choose for each one whether to use it again."
	case waiting == 1 && zh:
		return "你的回复保留着，重新开始时会用上。"
	case waiting == 1:
		return "Your reply is kept, and goes in when you start again."
	case waiting > 1 && zh:
		return "你的 " + strconv.Itoa(waiting) + " 条回复保留着，重新开始时会用上。"
	case waiting > 1:
		return "Your " + strconv.Itoa(waiting) + " replies are kept, and go in when you start again."
	}
	return ""
}

var handoffPushCommand = regexp.MustCompile(`(^|[\s;&|])git\s+(-\S+\s+)*push(\s|$)`)

// handoffEffectSteps reads the stopped attempt's steps whose effect left the
// run and whose outcome the record does not have: a file written outside the
// task's folder, or a push. Before asking, it looks for proof on the computer.
func (a *App) handoffEffectSteps(entry handoff.Entry) []handoff.EffectStep {
	if a == nil || a.store == nil || strings.TrimSpace(entry.ChatSessionID) == "" || entry.AttemptStartedAt == 0 {
		return nil
	}
	page, err := a.store.LoadStreamItemsLatest(context.Background(), entry.ChatSessionID, 400)
	if err != nil {
		return nil
	}
	results := map[string]bool{}
	for _, item := range page.Items {
		if item.Kind == "result" && item.ToolUseID != "" {
			results[item.ToolUseID] = true
		}
	}
	var steps []handoff.EffectStep
	for _, item := range page.Items {
		if item.Kind != "tool" || item.CreatedAt < entry.AttemptStartedAt || item.ToolUseID == "" || results[item.ToolUseID] {
			continue
		}
		if step, ok := handoffEffectStep(entry.Workdir, item.ToolName, item.ToolInput); ok {
			step.ID, step.At = item.ToolUseID, item.CreatedAt
			steps = append(steps, step)
		}
		if len(steps) == 8 {
			break
		}
	}
	return steps
}

// handoffEffectStep classifies one tool call with no recorded result.
func handoffEffectStep(workdir, tool, rawInput string) (handoff.EffectStep, bool) {
	var input struct {
		FilePath    string `json:"file_path"`
		Content     string `json:"content"`
		OldString   string `json:"old_string"`
		NewString   string `json:"new_string"`
		Command     string `json:"command"`
		Description string `json:"description"`
	}
	if json.Unmarshal([]byte(rawInput), &input) != nil {
		return handoff.EffectStep{}, false
	}
	switch tool {
	case "Write", "Edit":
		path := filepath.Clean(input.FilePath)
		if !filepath.IsAbs(path) || strings.TrimSpace(workdir) == "" || handoffPathWithin(filepath.Clean(workdir), path) {
			return handoff.EffectStep{}, false
		}
		// A file on this computer is its own proof: it is checked, never asked
		// about (tasks-review v1.0.12).
		step := handoff.EffectStep{Kind: handoff.StepWriteFile, Target: path, App: "Finder", Proof: handoff.ProofNotDone}
		if data, err := os.ReadFile(path); err == nil {
			text := string(data)
			switch tool {
			case "Write":
				if text == input.Content {
					step.Proof = handoff.ProofDone
				}
			case "Edit":
				if input.NewString != "" && strings.Contains(text, input.NewString) &&
					(input.OldString == "" || !strings.Contains(text, input.OldString) || strings.Contains(input.NewString, input.OldString)) {
					step.Proof = handoff.ProofDone
				}
			}
		}
		return step, true
	case "Bash":
		command := strings.TrimSpace(input.Command)
		if !handoffPushCommand.MatchString(command) {
			return handoff.EffectStep{}, false
		}
		return handoff.EffectStep{Kind: handoff.StepRunCommand, Name: mobileOfferText(input.Description, 120),
			Target: mobileOfferText(command, 200), Proof: handoff.ProofUnknown}, true
	}
	return handoff.EffectStep{}, false
}

func handoffPathWithin(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// taskStoppedForReplies reports a stopped task whose replies wait for Start
// again or Start over instead of starting a turn.
func taskStoppedForReplies(entry handoff.Entry) bool {
	if handoff.StoppedByYou(entry) {
		return true
	}
	group, cause := handoff.Placement(entry)
	return group == handoff.BoardNeedsYou && cause != ""
}

// recordHandoffStepsDone stamps when each met step was first confirmed, so
// the thread can place finished steps among its messages.
func recordHandoffStepsDone(e *handoff.Entry, now int64) {
	if e.Verification == nil {
		return
	}
	thread := ensureThread(e)
	for len(thread.StepDoneAt) < e.Verification.Met {
		thread.StepDoneAt = append(thread.StepDoneAt, now)
	}
}

// RevealTaskStep opens Finder on a stopped task's file step with the file
// selected ("Check in Finder").
func (a *App) RevealTaskStep(taskID, stepID string) error {
	entry, err := a.handoffStore.Get(taskID)
	if err != nil {
		return err
	}
	if entry.Thread != nil {
		for _, step := range entry.Thread.Steps {
			if step.ID == stepID && step.Kind == handoff.StepWriteFile {
				return revealInFileManager(step.Target)
			}
		}
	}
	return fmt.Errorf("task %s has no file step %s", taskID, stepID)
}
