package handoff

import (
	"errors"
	"strings"
)

// Thread is the task's conversation: where it came from, what Isoform and the
// person said about it, and what came of it. It is optional and additive, so
// records written before threads existed read as an empty thread. The store
// keeps codes and parameters only; the app owns the localized wording.
type Thread struct {
	Origin    *Origin    `json:"origin,omitempty"`
	Events    []Event    `json:"events,omitempty"`
	Outcome   *Outcome   `json:"outcome,omitempty"`
	Leftovers []Leftover `json:"leftovers,omitempty"`
	// OpenedAt is when the person last opened the thread (unix ms); the phone
	// shows an unread dot on a Needs you row updated after it.
	OpenedAt int64 `json:"openedAt,omitempty"`
	// NotNowUntil hides a waiting task from Needs you until then (unix ms).
	// Records written before PutOff carry it; new Not nows write PutOff.
	NotNowUntil int64 `json:"notNowUntil,omitempty"`
	// PutOff is the task's Not now on Needs you.
	PutOff *PutOff `json:"putOff,omitempty"`
}

// Origin kinds: where a task came from.
const (
	OriginUpNext    = "up_next"    // Do it on an Up next offer
	OriginBriefChip = "brief_chip" // a chip in a brief
	OriginChat      = "chat"       // asked in Chat
	OriginList      = "list"       // the brain dump on Tasks
	OriginRoutine   = "routine"    // a routine run
	OriginDesktop   = "desktop"    // started on the computer
)

// Origin records where a task came from and when.
type Origin struct {
	Kind string `json:"kind"`
	At   int64  `json:"at"` // unix ms
	// Label is the chip's or routine's own words, when the origin has them.
	Label string `json:"label,omitempty"`
	// Slot is the brief or routine time the task came from (unix ms), when known.
	Slot int64 `json:"slot,omitempty"`
}

// Event kinds in a thread, in time order.
const (
	EventIsoform = "isoform" // a message from Isoform
	EventYou     = "you"     // a message the person wrote or a choice they tapped
	EventLine    = "line"    // a quiet gray line (a result, "Deleted from Mail › Drafts")
	EventFile    = "file"    // a file the task made
)

// Event is one entry in a thread. Choices are answers Isoform offered as
// buttons; ChosenID is set once the person tapped one, which also appends the
// tap as their own message.
type Event struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	At   int64  `json:"at"` // unix ms
	Text string `json:"text,omitempty"`
	Code string `json:"code,omitempty"` // app-owned wording, rendered on read
	// Params fill a coded message: cause and assistant for a stop.
	Params   map[string]string `json:"params,omitempty"`
	Path     string            `json:"path,omitempty"` // EventFile: the file on the computer
	Choices  []Choice          `json:"choices,omitempty"`
	ChosenID string            `json:"chosenId,omitempty"`
	// ChatSeq is the task chat's sequence number of a reply sent into it, so
	// Isoform's answer to that turn can be read back into the thread.
	ChatSeq int64 `json:"chatSeq,omitempty"`
}

// Choice is one answer button on an Isoform message.
type Choice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Outcome kinds: what came of a finished task.
const (
	OutcomeSent        = "sent"         // Isoform sent it: {person} has {thing}.
	OutcomeYouDid      = "you_did"      // You replied from {app} at {time}.
	OutcomeAlreadyDone = "already_done" // Already done: you {did} at {time}.
	OutcomeHoldPlaced  = "hold_placed"  // On your calendar: {when}.
	OutcomeRoutineSent = "routine_sent" // {audience} has it. Sent {time}.
	OutcomeMarkedDone  = "marked_done"  // You marked it done.
	OutcomeCompleted   = "completed"    // Isoform finished the work: {summary}.
	OutcomeStopped     = "stopped"      // You stopped it at {time}[, after {n} of {m} steps].
)

// Thread event codes the app words on read.
const (
	EventCodeStoppedNoAssistant = "stopped_no_assistant" // I started at {time}, but no AI is signed in...
	EventCodeStopped            = "stopped"              // I stopped at {time} because {cause}...
	EventCodeStartedAgain       = "started_again"        // Started again · {time}
)

// Outcome authors.
const (
	OutcomeByIsoform = "isoform"
	OutcomeByYou     = "you"
)

// Outcome is what came of a task, built from the event that ended it. The app
// renders it as one localized sentence; a Done task without one shows no row.
type Outcome struct {
	Kind   string            `json:"kind"`
	By     string            `json:"by"`
	At     int64             `json:"at"` // unix ms
	Params map[string]string `json:"params,omitempty"`
}

// Leftover kinds and states: what Isoform put outside Isoform for a task.
const (
	LeftoverMailDraft    = "mail_draft"
	LeftoverCalendarHold = "calendar_hold"

	LeftoverPlaced  = "placed"
	LeftoverAsked   = "asked"
	LeftoverKept    = "kept"
	LeftoverRemoved = "removed"
)

// Leftover is one thing a task left in another app, so Isoform can offer once
// to remove it. It deletes only what it made, and only on the person's tap.
type Leftover struct {
	Kind  string `json:"kind"`
	Ref   string `json:"ref"`
	Label string `json:"label,omitempty"`
	State string `json:"state"`
}

const (
	threadEventLimit    = 200
	threadEventTextMax  = 4096
	threadChoiceLimit   = 4
	threadChoiceTextMax = 120
)

// AppendEvent adds an event to the thread, keeping at most the newest
// threadEventLimit events. Text is trimmed and bounded.
func (t *Thread) AppendEvent(event Event) {
	event.Text = boundText(strings.TrimSpace(event.Text), threadEventTextMax)
	if len(event.Choices) > threadChoiceLimit {
		event.Choices = event.Choices[:threadChoiceLimit]
	}
	for i := range event.Choices {
		event.Choices[i].Label = boundText(strings.TrimSpace(event.Choices[i].Label), threadChoiceTextMax)
	}
	t.Events = append(t.Events, event)
	if len(t.Events) > threadEventLimit {
		t.Events = append([]Event(nil), t.Events[len(t.Events)-threadEventLimit:]...)
	}
}

// ErrChoiceUnavailable is returned when a choice is unknown or already answered.
var ErrChoiceUnavailable = errors.New("handoff: that choice is no longer open")

// Choose records the person's tap on one of an Isoform message's answers: the
// buttons go and the tap becomes their own message. It returns the label.
func (t *Thread) Choose(eventID, choiceID string, at int64, youEventID string) (string, error) {
	for i := range t.Events {
		event := &t.Events[i]
		if event.ID != eventID || event.Kind != EventIsoform {
			continue
		}
		if event.ChosenID != "" {
			return "", ErrChoiceUnavailable
		}
		for _, choice := range event.Choices {
			if choice.ID == choiceID {
				event.ChosenID = choiceID
				t.AppendEvent(Event{ID: youEventID, Kind: EventYou, At: at, Text: choice.Label})
				return choice.Label, nil
			}
		}
		return "", ErrChoiceUnavailable
	}
	return "", ErrChoiceUnavailable
}

func boundText(text string, max int) string {
	if len(text) <= max {
		return text
	}
	cut := max
	for cut > 0 && !utf8Start(text[cut]) {
		cut--
	}
	return text[:cut]
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

// canUserComplete reports whether the person may close a waiting task as done
// themselves (Mark as done, or "you already did it"). Only verification may
// complete running work; a waiting task needs only the person's word.
func canUserComplete(from Status) bool {
	switch from {
	case StatusActive, StatusBlocked, StatusNeedsReview, StatusStale:
		return true
	}
	return false
}

// closedByYou reports whether the entry carries the person's own outcome.
func closedByYou(e Entry) bool {
	return e.Thread != nil && e.Thread.Outcome != nil && e.Thread.Outcome.By == OutcomeByYou
}
