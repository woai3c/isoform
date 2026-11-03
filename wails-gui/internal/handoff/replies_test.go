package handoff

import (
	"errors"
	"testing"
)

func TestReplyQueueWaitsPicksUpAndUndoes(t *testing.T) {
	thread := &Thread{}
	first, err := thread.QueueReply(Reply{ID: "r1", Text: " Use the blue deck ", SentAt: 10, Key: "k1"})
	if err != nil || first.State != ReplyWaiting || first.Text != "Use the blue deck" {
		t.Fatalf("queued = %+v, %v", first, err)
	}
	if again, _ := thread.QueueReply(Reply{ID: "other", Text: "dup", SentAt: 11, Key: "k1"}); again.ID != "r1" || len(thread.Replies) != 1 {
		t.Fatalf("a resent key must return the saved reply: %+v", thread.Replies)
	}
	thread.QueueReply(Reply{ID: "r2", Text: "And cc Dan", SentAt: 12})
	if got := thread.WaitingReplies(); len(got) != 2 || got[0].ID != "r1" || got[1].ID != "r2" {
		t.Fatalf("waiting = %+v", got)
	}
	if removed, err := thread.WithdrawReply("r2"); err != nil || removed.Text != "And cc Dan" {
		t.Fatalf("undo = %+v, %v", removed, err)
	}
	thread.TakeReplies([]string{"r1"}, "a1")
	if _, err := thread.WithdrawReply("r1"); !errors.Is(err, ErrReplyPickedUp) {
		t.Fatalf("undo of a reply being taken = %v", err)
	}
	thread.ConfirmReplies([]string{"r1"}, "a1", 50, 7)
	r, _ := thread.Reply("r1")
	if r.State != ReplyPickedUp || r.PickedUpAt != 50 {
		t.Fatalf("confirmed = %+v", r)
	}
	if len(thread.Events) != 1 || thread.Events[0].ID != "r1" || thread.Events[0].Kind != EventYou || thread.Events[0].ChatSeq != 7 {
		t.Fatalf("a picked-up reply joins the thread where it was read: %+v", thread.Events)
	}
	if _, err := thread.WithdrawReply("r1"); !errors.Is(err, ErrReplyPickedUp) {
		t.Fatalf("undo after pickup = %v", err)
	}
}

func TestReplyTakenButUnconfirmedAtStopIsUncertainAndNeedsAChoice(t *testing.T) {
	thread := &Thread{}
	thread.QueueReply(Reply{ID: "send", Text: "Send the deck to Dan", SentAt: 1})
	thread.QueueReply(Reply{ID: "wait", Text: "Keep it short", SentAt: 2})
	thread.TakeReplies([]string{"send"}, "a1")
	thread.SettleRepliesAtStop()
	if r, _ := thread.Reply("send"); r.State != ReplyUncertain {
		t.Fatalf("unconfirmed reply = %+v", r)
	}
	if thread.RepliesNeedingChoice() != 1 {
		t.Fatal("an uncertain reply needs a choice")
	}
	use, left := thread.RestartReplies()
	if len(use) != 1 || use[0].ID != "wait" || len(left) != 1 || left[0].ID != "send" {
		t.Fatalf("an uncertain reply is never used again without Use it again: use %+v left %+v", use, left)
	}
	if err := thread.SetReplyLeftOut("send", false); err == nil {
		t.Fatal("an uncertain reply takes a choice, not the quiet toggle")
	}
	thread.SetReplyChoice("send", ReplyChoiceUseAgain)
	thread.SetReplyLeftOut("wait", true)
	use, left = thread.RestartReplies()
	if len(use) != 1 || use[0].ID != "send" || len(left) != 1 || left[0].ID != "wait" {
		t.Fatalf("use %+v left %+v", use, left)
	}
	thread.SetReplyChoice("send", "")
	if thread.RepliesNeedingChoice() != 1 {
		t.Fatal("Change brings the choice back")
	}
}

func TestEffectStepsGateAndSplitForStartAgain(t *testing.T) {
	thread := &Thread{Steps: []EffectStep{
		{ID: "a", Kind: StepWriteFile, Proof: ProofDone},
		{ID: "b", Kind: StepWriteFile, Proof: ProofNotDone},
		{ID: "c", Kind: StepRunCommand, Proof: ProofUnknown},
	}}
	if thread.StepsNeedingAnswer() != 1 || thread.StepsNeedingCheck() != 1 {
		t.Fatal("only the unknown step needs checking")
	}
	if err := thread.AnswerStep("a", StepRedo); err == nil {
		t.Fatal("a proven step takes no answer")
	}
	if err := thread.AnswerStep("c", StepSkip); err != nil || thread.StepsNeedingAnswer() != 0 {
		t.Fatalf("answer = %v", err)
	}
	skip, redo := thread.RestartSteps()
	if len(skip) != 2 || skip[0].ID != "a" || skip[1].ID != "c" || len(redo) != 1 || redo[0].ID != "b" {
		t.Fatalf("skip %+v redo %+v", skip, redo)
	}
}
