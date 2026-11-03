package handoff

import (
	"errors"
	"sort"
	"strings"
)

// Reply states (tasks-review v1.0.5–v1.0.7). A reply sent while its task runs
// waits on the thread until the runner takes it into a turn and confirms it.
const (
	ReplyWaiting   = "waiting"   // saved, not yet handed to a run
	ReplyTaking    = "taking"    // being handed to a run; not confirmed yet
	ReplyPickedUp  = "picked_up" // the run confirmed it took the reply
	ReplyUncertain = "uncertain" // the run stopped before confirming it
)

// Choices on an uncertain reply (v1.0.7). None until the person picks one.
const (
	ReplyChoiceLeftOut  = "left_out"
	ReplyChoiceUseAgain = "use_again"
)

// Reply is one of the person's messages queued on a task. The store keeps
// only what the person wrote and its state; the app owns the wording.
type Reply struct {
	ID         string `json:"id"`
	Text       string `json:"text"`
	SentAt     int64  `json:"sentAt"`
	Key        string `json:"key,omitempty"` // dedupes a resent request
	State      string `json:"state"`
	PickedUpAt int64  `json:"pickedUpAt,omitempty"`
	// Attempt is the run that took (or is taking) the reply.
	Attempt string `json:"attempt,omitempty"`
	// LeftOut is the person's Leave it out on a reply the next run would use.
	LeftOut bool `json:"leftOut,omitempty"`
	// Choice answers an uncertain reply: left_out, use_again or none.
	Choice string `json:"choice,omitempty"`
	// LeftOutOf is the run that started without this reply.
	LeftOutOf string `json:"leftOutOf,omitempty"`
}

const (
	replyLimit   = 50
	replyTextMax = 4096
)

var (
	// ErrReplyUnavailable is returned for an unknown reply.
	ErrReplyUnavailable = errors.New("handoff: that reply is no longer on the task")
	// ErrReplyPickedUp is returned by Undo once the run has the reply.
	ErrReplyPickedUp = errors.New("handoff: that reply was already picked up")
	// ErrReplyQueueFull is returned when a task already holds replyLimit replies.
	ErrReplyQueueFull = errors.New("handoff: too many replies are waiting")
)

// QueueReply saves a reply as waiting. A resent request with the same key
// returns the reply already saved.
func (t *Thread) QueueReply(reply Reply) (Reply, error) {
	if reply.Key != "" {
		for _, existing := range t.Replies {
			if existing.Key == reply.Key {
				return existing, nil
			}
		}
	}
	if len(t.Replies) >= replyLimit {
		return Reply{}, ErrReplyQueueFull
	}
	reply.Text = boundText(strings.TrimSpace(reply.Text), replyTextMax)
	reply.State = ReplyWaiting
	reply.PickedUpAt, reply.Attempt, reply.LeftOut, reply.Choice, reply.LeftOutOf = 0, "", false, "", ""
	t.Replies = append(t.Replies, reply)
	return reply, nil
}

func (t *Thread) reply(id string) *Reply {
	for i := range t.Replies {
		if t.Replies[i].ID == id {
			return &t.Replies[i]
		}
	}
	return nil
}

// Reply returns the stored reply with this id.
func (t *Thread) Reply(id string) (Reply, bool) {
	if r := t.reply(id); r != nil {
		return *r, true
	}
	return Reply{}, false
}

// WithdrawReply is Undo: it takes a waiting reply out of the queue. Once a run
// has taken it, Undo fails with ErrReplyPickedUp.
func (t *Thread) WithdrawReply(id string) (Reply, error) {
	for i, r := range t.Replies {
		if r.ID != id {
			continue
		}
		if r.State != ReplyWaiting || r.Attempt != "" || r.PickedUpAt != 0 || r.LeftOutOf != "" {
			return Reply{}, ErrReplyPickedUp
		}
		t.Replies = append(t.Replies[:i:i], t.Replies[i+1:]...)
		return r, nil
	}
	return Reply{}, ErrReplyUnavailable
}

// WaitingReplies are the replies a running task has not read yet, in order.
func (t *Thread) WaitingReplies() []Reply {
	var out []Reply
	for _, r := range t.sortedReplies() {
		if r.State == ReplyWaiting && !r.LeftOut {
			out = append(out, r)
		}
	}
	return out
}

func (t *Thread) sortedReplies() []Reply {
	out := append([]Reply(nil), t.Replies...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].SentAt < out[j].SentAt })
	return out
}

// TakeReplies marks replies as being handed to attempt. Only waiting replies
// and, for a fresh run, the ones RestartReplies chose can be taken.
func (t *Thread) TakeReplies(ids []string, attempt string) {
	for _, id := range ids {
		if r := t.reply(id); r != nil {
			r.State, r.Attempt = ReplyTaking, attempt
		}
	}
}

// ConfirmReplies records the run's confirmation that it took the replies: each
// is picked up at at, and gains its message in the thread where it was read.
// seq is the task chat's turn, kept on the last one so its answer reads back.
func (t *Thread) ConfirmReplies(ids []string, attempt string, at, seq int64) {
	for i, id := range ids {
		r := t.reply(id)
		if r == nil || r.State != ReplyTaking || r.Attempt != attempt {
			continue
		}
		r.State, r.PickedUpAt, r.Choice, r.LeftOut, r.LeftOutOf = ReplyPickedUp, at, "", false, ""
		var chatSeq int64
		if i == len(ids)-1 {
			chatSeq = seq
		}
		found := false
		for j := range t.Events {
			if t.Events[j].ID == id {
				found = true
				if chatSeq != 0 {
					t.Events[j].ChatSeq = chatSeq
				}
			}
		}
		if !found {
			t.AppendEvent(Event{ID: id, Kind: EventYou, At: at, Text: r.Text, ChatSeq: chatSeq})
		}
	}
}

// ReturnReplies puts replies a run could not take back in the queue.
func (t *Thread) ReturnReplies(ids []string, attempt string) {
	for _, id := range ids {
		if r := t.reply(id); r != nil && r.State == ReplyTaking && r.Attempt == attempt {
			r.State = ReplyWaiting
			if r.PickedUpAt == 0 {
				r.Attempt = ""
			}
		}
	}
}

// SettleRepliesAtStop runs when a task stops: a reply handed to the run but
// never confirmed may have been acted on, so it becomes uncertain. Waiting and
// confirmed replies stay as they are.
func (t *Thread) SettleRepliesAtStop() {
	for i := range t.Replies {
		if t.Replies[i].State == ReplyTaking {
			t.Replies[i].State, t.Replies[i].Choice = ReplyUncertain, ""
		}
	}
}

// SetReplyLeftOut is Leave it out (true) and Use it (false) on a reply the
// next run would use. An uncertain reply takes SetReplyChoice instead.
func (t *Thread) SetReplyLeftOut(id string, leftOut bool) error {
	r := t.reply(id)
	if r == nil || r.State == ReplyUncertain || r.State == ReplyTaking {
		return ErrReplyUnavailable
	}
	r.LeftOut = leftOut
	return nil
}

// SetReplyChoice answers an uncertain reply; an empty choice is Change.
func (t *Thread) SetReplyChoice(id, choice string) error {
	r := t.reply(id)
	if r == nil || r.State != ReplyUncertain {
		return ErrReplyUnavailable
	}
	switch choice {
	case "", ReplyChoiceLeftOut, ReplyChoiceUseAgain:
	default:
		return ErrReplyUnavailable
	}
	r.Choice = choice
	return nil
}

// RepliesNeedingChoice counts uncertain replies without an answer.
func (t *Thread) RepliesNeedingChoice() int {
	if t == nil {
		return 0
	}
	n := 0
	for _, r := range t.Replies {
		if r.State == ReplyUncertain && r.Choice == "" {
			n++
		}
	}
	return n
}

// RestartReplies splits the thread's replies for a fresh run (v1.0.6–v1.0.7):
// waiting and confirmed replies go in unless left out; uncertain ones only with
// Use it again.
func (t *Thread) RestartReplies() (use, leftOut []Reply) {
	for _, r := range t.sortedReplies() {
		switch r.State {
		case ReplyUncertain:
			if r.Choice == ReplyChoiceUseAgain {
				use = append(use, r)
			} else {
				leftOut = append(leftOut, r)
			}
		case ReplyWaiting, ReplyPickedUp:
			if r.LeftOut {
				leftOut = append(leftOut, r)
			} else {
				use = append(use, r)
			}
		}
	}
	return use, leftOut
}

// MarkLeftOutOf records the run that started without these replies.
func (t *Thread) MarkLeftOutOf(ids []string, attempt string) {
	for _, id := range ids {
		if r := t.reply(id); r != nil {
			r.LeftOutOf = attempt
		}
	}
}
