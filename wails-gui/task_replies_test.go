package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wails-gui/internal/handoff"
	"wails-gui/storage"
)

type taskReplyRig struct {
	*handoffTestRig
	bridge *mobileBridge
	turns  *mobileTestTurns
	id     string
}

// newTaskReplyRig starts a task whose first turn holds until cancelled.
func newTaskReplyRig(t *testing.T) *taskReplyRig {
	t.Helper()
	rig := newHandoffTestRig(t)
	entry, err := rig.app.createTaskEntry(CreateTaskInput{Title: "Make the deck", Brief: "hold"}, handoff.SourceDesktop)
	if err != nil {
		t.Fatal(err)
	}
	bridge, err := newMobileBridge(rig.app, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.Stop)
	rig.app.desktopTaskBridge = bridge
	turns := &mobileTestTurns{}
	mobileChatRunnerOverride = mobileTestMessageOverride(turns)
	t.Cleanup(func() { mobileTestResetChatRunner(t, bridge) })
	if _, err := bridge.startHandoff(entry.ID); err != nil {
		t.Fatal(err)
	}
	mobileTestWaitFor(t, "the task's first turn", func() bool { return turns.count() == 1 })
	return &taskReplyRig{handoffTestRig: rig, bridge: bridge, turns: turns, id: entry.ID}
}

func (r *taskReplyRig) get(t *testing.T) handoff.Entry {
	t.Helper()
	entry, err := r.handoffs.Get(r.id)
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

func (r *taskReplyRig) endTurn(t *testing.T) {
	t.Helper()
	chat := r.get(t).ChatSessionID
	r.bridge.cancelChatTurn(chat)
	mobileTestWaitFor(t, "the turn to end", func() bool { return !r.bridge.chatRunning(chat) })
}

func (r *taskReplyRig) prompt(i int) string {
	r.turns.mu.Lock()
	defer r.turns.mu.Unlock()
	return r.turns.prompts[i]
}

func TestTaskReplyWhileRunningWaitsThenIsPickedUpAtTheNextTurn(t *testing.T) {
	rig := newTaskReplyRig(t)
	detail, err := rig.app.ReplyToDesktopTask(rig.id, "Use the blue deck")
	if err != nil {
		t.Fatalf("a reply while the task runs must wait, not be refused: %v", err)
	}
	if len(detail.Replies) != 1 || detail.Replies[0].State != handoff.ReplyWaiting || detail.Replies[0].Text != "Use the blue deck" {
		t.Fatalf("replies = %+v", detail.Replies)
	}
	if rig.turns.count() != 1 {
		t.Fatal("a waiting reply must not start a turn while one runs")
	}
	second, err := rig.app.ReplyToDesktopTask(rig.id, "And cc Dan")
	if err != nil || len(second.Replies) != 2 {
		t.Fatalf("second reply = %+v, %v", second.Replies, err)
	}
	undone, err := rig.app.ActOnDesktopTask(rig.id, DesktopTaskActionInput{Action: mobileTaskActionReplyUndo, EventID: second.Replies[1].ID})
	if err != nil || len(undone.Replies) != 1 {
		t.Fatalf("Undo = %+v, %v", undone.Replies, err)
	}
	rig.endTurn(t)
	rig.app.markHandoffAttemptEnded(rig.get(t), true, "Done.", nil)
	mobileTestWaitFor(t, "the waiting reply's turn", func() bool { return rig.turns.count() == 2 })
	if rig.prompt(1) != "Use the blue deck" {
		t.Fatalf("pickup turn = %q", rig.prompt(1))
	}
	saved := rig.get(t)
	if saved.Status != handoff.StatusInProgress {
		t.Fatalf("a run that ends with replies waiting goes back to Running, got %s", saved.Status)
	}
	reply := saved.Thread.Replies[0]
	if reply.State != handoff.ReplyPickedUp || reply.PickedUpAt == 0 {
		t.Fatalf("picked up = %+v", reply)
	}
	if _, err := rig.app.ActOnDesktopTask(rig.id, DesktopTaskActionInput{Action: mobileTaskActionReplyUndo, EventID: reply.ID}); err == nil || !strings.Contains(err.Error(), taskReplyPickedUpMessage) {
		t.Fatalf("Undo after pickup = %v", err)
	}
}

func TestTaskReplyQueuedDuringTheCheckIsStillPickedUp(t *testing.T) {
	rig := newTaskReplyRig(t)
	rig.endTurn(t)
	current := rig.get(t)
	// The reply lands while the finished run is being checked, after the
	// turn-boundary pickup has looked.
	handoffSettleHook = func(id string) {
		rig.handoffs.MutateThread(id, func(thread *handoff.Thread) {
			thread.QueueReply(handoff.Reply{ID: "late", Text: "One more thing", SentAt: 1})
		})
	}
	t.Cleanup(func() { handoffSettleHook = nil })
	rig.app.markHandoffAttemptEnded(current, true, "Done.", nil)
	mobileTestWaitFor(t, "the late reply's turn", func() bool { return rig.turns.count() == 2 })
	if saved := rig.get(t); saved.Status != handoff.StatusInProgress {
		t.Fatalf("nothing waits forever on a finished task: %s", saved.Status)
	}
}

func TestTaskStopKeepsRepliesAndStartOverWaitsForUncertainChoice(t *testing.T) {
	rig := newTaskReplyRig(t)
	waiting, err := rig.app.ReplyToDesktopTask(rig.id, "Keep it short")
	if err != nil {
		t.Fatal(err)
	}
	attempt := rig.get(t).AttemptID
	rig.handoffs.MutateThread(rig.id, func(thread *handoff.Thread) {
		thread.QueueReply(handoff.Reply{ID: "send-dan", Text: "Send the deck to Dan", SentAt: 2})
		thread.TakeReplies([]string{"send-dan"}, attempt)
	})
	if err := rig.app.AbortHandoff(rig.id); err != nil {
		t.Fatal(err)
	}
	stopped := rig.get(t)
	if r, _ := stopped.Thread.Reply(waiting.Replies[0].ID); r.State != handoff.ReplyWaiting {
		t.Fatalf("Stop keeps a waiting reply: %+v", r)
	}
	if r, _ := stopped.Thread.Reply("send-dan"); r.State != handoff.ReplyUncertain {
		t.Fatalf("an unconfirmed reply is uncertain after Stop: %+v", r)
	}
	if view := handoffEntryView(stopped); view.RepliesToChoose != 0 {
		// The person stopped it: the task is in Done with Start over, not a Review row.
		t.Fatalf("a task you stopped has no Needs you row: %+v", view)
	}
	rig.endTurn(t)
	if _, err := rig.app.StartHandoffAgain(rig.id); !errors.Is(err, errTaskRestartNeedsAnswers) {
		t.Fatalf("Start over must wait for the uncertain reply's answer: %v", err)
	}
	if _, err := rig.app.ActOnDesktopTask(rig.id, DesktopTaskActionInput{Action: mobileTaskActionReplyUseAgain, EventID: "send-dan"}); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.app.ActOnDesktopTask(rig.id, DesktopTaskActionInput{Action: mobileTaskActionReplyLeaveOut, EventID: waiting.Replies[0].ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.app.StartHandoffAgain(rig.id); err != nil {
		t.Fatal(err)
	}
	mobileTestWaitFor(t, "the fresh run", func() bool { return rig.turns.count() == 2 })
	prompt := rig.prompt(1)
	if !strings.Contains(prompt, "Send the deck to Dan") || strings.Contains(prompt, "Keep it short") {
		t.Fatalf("a fresh run uses every reply except the left-out ones: %q", prompt)
	}
	restarted := rig.get(t)
	if r, _ := restarted.Thread.Reply("send-dan"); r.State != handoff.ReplyPickedUp || r.PickedUpAt == 0 {
		t.Fatalf("a reply used again is picked up once the run confirms it: %+v", r)
	}
	views := mobileTaskReplies(restarted)
	for _, view := range views {
		if view.ID == waiting.Replies[0].ID && (!view.LeftOutOfRun || !view.LeftOut) {
			t.Fatalf("a left-out reply reads Left out of this run: %+v", view)
		}
	}
}

func TestTaskStoppedByIsoformWithAnUncertainReplyAsksForReview(t *testing.T) {
	entry := handoff.Entry{Status: handoff.StatusBlocked, BlockedReason: handoff.BlockedInterrupted, Thread: &handoff.Thread{}}
	entry.Thread.QueueReply(handoff.Reply{ID: "r", Text: "Send it", SentAt: 1})
	entry.Thread.TakeReplies([]string{"r"}, "a")
	recordSystemStop(&entry, handoff.CauseRestarted, "", 2)
	if view := handoffEntryView(entry); view.RepliesToChoose != 1 {
		t.Fatalf("the row says One reply needs a choice and Review: %+v", view)
	}
	if got := taskRepliesStopSentence(entry, false); !strings.Contains(got, "one of your replies") {
		t.Fatalf("stop sentence = %q", got)
	}
	waitingOnly := handoff.Entry{StoppedBy: handoff.StoppedBySystem, Thread: &handoff.Thread{}}
	waitingOnly.Thread.QueueReply(handoff.Reply{ID: "a", Text: "x", SentAt: 1})
	waitingOnly.Thread.QueueReply(handoff.Reply{ID: "b", Text: "y", SentAt: 2})
	if got := taskRepliesStopSentence(waitingOnly, true); got != "你的 2 条回复保留着，重新开始时会用上。" {
		t.Fatalf("zh stop sentence = %q", got)
	}
}

func TestTaskStartAgainWaitsUntilTheOldRunIsConfirmedEnded(t *testing.T) {
	rig := newHandoffTestRig(t)
	if _, err := rig.handoffs.Save(handoff.Entry{ID: "old-run", Title: "Old run", Body: "Do it", Status: handoff.StatusAborted, StoppedBy: handoff.StoppedBySystem, StopCause: handoff.CauseRestarted, ChatSessionID: "chat-live"}); err != nil {
		t.Fatal(err)
	}
	rig.app.mu.Lock()
	if rig.app.activeTags == nil {
		rig.app.activeTags = map[string]bool{}
	}
	rig.app.activeTags["chat-live"] = true
	rig.app.mu.Unlock()
	if _, err := rig.app.acceptHandoff("old-run", nil); !errors.Is(err, errTaskOldRunLive) {
		t.Fatalf("Start again while a worker may still run = %v", err)
	}
	bridge, err := newMobileBridge(rig.app, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.Stop)
	rig.app.desktopTaskBridge = bridge
	if _, err := rig.app.ActOnDesktopTask("old-run", DesktopTaskActionInput{Action: mobileTaskActionRunEnded}); err == nil || !strings.Contains(err.Error(), taskOldRunLiveMessage) {
		t.Fatalf("the check must say it couldn't confirm: %v", err)
	}
	rig.app.mu.Lock()
	delete(rig.app.activeTags, "chat-live")
	rig.app.mu.Unlock()
	if _, err := rig.app.ActOnDesktopTask("old-run", DesktopTaskActionInput{Action: mobileTaskActionRunEnded}); err != nil {
		t.Fatalf("the ended run confirms: %v", err)
	}
}

func TestTaskEffectStepsLookForProofBeforeAsking(t *testing.T) {
	workdir, outside := t.TempDir(), t.TempDir()
	same := filepath.Join(outside, "faq.md")
	os.WriteFile(same, []byte("Pricing FAQ"), 0o600)
	changed := filepath.Join(outside, "other.md")
	os.WriteFile(changed, []byte("something else"), 0o600)
	for _, tc := range []struct {
		name, tool, input, proof string
		counted                  bool
	}{
		{"written, proof it happened", "Write", `{"file_path":"` + same + `","content":"Pricing FAQ"}`, handoff.ProofDone, true},
		{"never written, proof it didn't", "Write", `{"file_path":"` + filepath.Join(outside, "missing.md") + `","content":"x"}`, handoff.ProofNotDone, true},
		{"a file that says otherwise is proof it didn't", "Write", `{"file_path":"` + changed + `","content":"x"}`, handoff.ProofNotDone, true},
		{"an edit that is in the file", "Edit", `{"file_path":"` + same + `","old_string":"Pricing","new_string":"Pricing FAQ"}`, handoff.ProofDone, true},
		{"an edit that isn't", "Edit", `{"file_path":"` + changed + `","old_string":"else","new_string":"new"}`, handoff.ProofNotDone, true},
		{"inside the task's folder", "Write", `{"file_path":"` + filepath.Join(workdir, "a.md") + `","content":"x"}`, "", false},
		{"a push", "Bash", `{"command":"git push origin main","description":"Push the branch"}`, handoff.ProofUnknown, true},
		{"a read", "Bash", `{"command":"git status"}`, "", false},
		{"a read tool", "Read", `{"file_path":"` + same + `"}`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			step, ok := handoffEffectStep(workdir, tc.tool, tc.input)
			if ok != tc.counted || ok && step.Proof != tc.proof {
				t.Fatalf("step = %+v, counted %v", step, ok)
			}
		})
	}
}

func TestTaskUnknownPushNeedsAnAnswerAndASkippedStepNeverRuns(t *testing.T) {
	rig := newTaskReplyRig(t)
	running := rig.get(t)
	if _, err := rig.app.store.AppendStreamItem(context.Background(), running.ChatSessionID, storage.StreamItemRow{
		ID: "tool-push", Kind: "tool", ToolName: "Bash", ToolUseID: "toolu_push",
		ToolInput: `{"command":"git push origin main","description":"Push the branch"}`,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.app.store.AppendStreamItem(context.Background(), running.ChatSessionID, storage.StreamItemRow{
		ID: "tool-done", Kind: "tool", ToolName: "Bash", ToolUseID: "toolu_done", ToolInput: `{"command":"git push origin other"}`,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.app.store.AppendStreamItem(context.Background(), running.ChatSessionID, storage.StreamItemRow{
		ID: "result-done", Kind: "result", ToolName: "Bash", ToolUseID: "toolu_done", Content: "pushed",
	}); err != nil {
		t.Fatal(err)
	}
	rig.app.markHandoffBlocked(rig.id, handoff.BlockedInterrupted)
	rig.endTurn(t)
	stopped := rig.get(t)
	if len(stopped.Thread.Steps) != 1 || stopped.Thread.Steps[0].Name != "Push the branch" || stopped.Thread.Steps[0].Proof != handoff.ProofUnknown {
		t.Fatalf("only the step with no recorded outcome needs checking: %+v", stopped.Thread.Steps)
	}
	if view := mobileTaskView(stopped); view.StepsToCheck != 1 {
		t.Fatalf("the row says One step needs checking with Review: %+v", view)
	}
	if _, err := rig.app.StartHandoffAgain(rig.id); !errors.Is(err, errTaskRestartNeedsAnswers) {
		t.Fatalf("Start again stays off until each step has an answer: %v", err)
	}
	if _, err := rig.app.ActOnDesktopTask(rig.id, DesktopTaskActionInput{Action: mobileTaskActionStepSkip, EventID: "toolu_push"}); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.app.StartHandoffAgain(rig.id); err != nil {
		t.Fatal(err)
	}
	mobileTestWaitFor(t, "the fresh run", func() bool { return rig.turns.count() == 2 })
	if prompt := rig.prompt(1); !strings.Contains(prompt, "Do not do them again") || !strings.Contains(prompt, "Push the branch") {
		t.Fatalf("a skipped step never runs: %q", prompt)
	}
	restarted := rig.get(t)
	if len(restarted.Thread.Skipped) != 1 || len(restarted.Thread.Steps) != 0 {
		t.Fatalf("skipped = %+v steps = %+v", restarted.Thread.Skipped, restarted.Thread.Steps)
	}
	last := restarted.Thread.Events[len(restarted.Thread.Events)-1]
	for _, event := range restarted.Thread.Events {
		if event.Code == handoff.EventCodeStartedAgain {
			last = event
		}
	}
	if last.Params["skipped"] != "Push the branch" {
		t.Fatalf("Started again names what was skipped: %+v", last)
	}
}

func TestTaskReplyToAStoppedTaskWaitsForStartAgain(t *testing.T) {
	rig := newHandoffTestRig(t)
	if _, err := rig.handoffs.Save(handoff.Entry{ID: "stopped", Title: "Prepare the report", Body: "Do it", Status: handoff.StatusAborted, StoppedBy: handoff.StoppedBySystem, StopCause: handoff.CauseLoopGuard}); err != nil {
		t.Fatal(err)
	}
	bridge, err := newMobileBridge(rig.app, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.Stop)
	rig.app.desktopTaskBridge = bridge
	if _, err := rig.app.ReplyToDesktopTask("stopped", "Use last quarter's numbers"); err != nil {
		t.Fatalf("a reply to a stopped task: %v", err)
	}
	entry, err := rig.handoffs.Get("stopped")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Status != handoff.StatusAborted || entry.Thread == nil || len(entry.Thread.WaitingReplies()) != 1 || entry.ChatSessionID != "" {
		t.Fatalf("the reply waits for Start again and starts nothing: %+v", entry)
	}
	prepareHandoffRestart(&entry, true)
	if got := handoffTakenReplies(entry); len(got) != 1 || got[0].Text != "Use last quarter's numbers" {
		t.Fatalf("Start again takes the reply: %+v", got)
	}
}

func TestTaskStepsDoneAtStampsEachMetStepOnce(t *testing.T) {
	entry := handoff.Entry{ID: "deck", Title: "Make the deck", Status: handoff.StatusInProgress, Criteria: []string{"Outline", "Market slide", "Team slide"}, Verification: &handoff.Verification{Met: 1, Total: 3}}
	recordHandoffStepsDone(&entry, 100)
	entry.Verification.Met = 2
	recordHandoffStepsDone(&entry, 200)
	recordHandoffStepsDone(&entry, 300)
	if got := entry.Thread.StepDoneAt; len(got) != 2 || got[0] != 100 || got[1] != 200 {
		t.Fatalf("StepDoneAt = %v", got)
	}
	detail, _ := mobileTaskDetail(entry, nil)
	if detail.Steps[0].At == "" || detail.Steps[1].At == "" || detail.Steps[2].At != "" {
		t.Fatalf("steps = %+v", detail.Steps)
	}
}
