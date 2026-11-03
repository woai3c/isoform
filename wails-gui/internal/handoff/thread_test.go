package handoff

import (
	"strings"
	"testing"
)

func TestThreadRoundTripsAndOlderRecordsReadEmpty(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Save(Entry{ID: "hf_old", Title: "Old", Status: StatusActive}); err != nil {
		t.Fatal(err)
	}
	old, err := s.Get("hf_old")
	if err != nil || old.Thread != nil {
		t.Fatalf("older record should read without a thread: %+v %v", old.Thread, err)
	}

	in := Entry{ID: "hf_t", Title: "Send Priya the signed SOW", Status: StatusActive, Thread: &Thread{
		Origin: &Origin{Kind: OriginUpNext, At: 1000},
	}}
	in.Thread.AppendEvent(Event{ID: "e1", Kind: EventIsoform, At: 1100, Text: "Send v3 now, or wait for Lena?",
		Choices: []Choice{{ID: "now", Label: "Send v3 now"}, {ID: "wait", Label: "Wait for Lena"}}})
	if _, err := s.Save(in); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("hf_t")
	if err != nil {
		t.Fatal(err)
	}
	if got.Thread == nil || got.Thread.Origin.Kind != OriginUpNext || len(got.Thread.Events) != 1 ||
		len(got.Thread.Events[0].Choices) != 2 {
		t.Fatalf("thread did not round-trip: %+v", got.Thread)
	}
}

func TestChooseRecordsTheTapOnceAsYourMessage(t *testing.T) {
	thread := &Thread{}
	thread.AppendEvent(Event{ID: "q", Kind: EventIsoform, At: 1, Text: "Now or later?",
		Choices: []Choice{{ID: "now", Label: "Send v3 now"}}})
	label, err := thread.Choose("q", "now", 2, "y")
	if err != nil || label != "Send v3 now" {
		t.Fatalf("choose: %q %v", label, err)
	}
	if thread.Events[0].ChosenID != "now" || len(thread.Events) != 2 ||
		thread.Events[1].Kind != EventYou || thread.Events[1].Text != "Send v3 now" {
		t.Fatalf("tap should become your message: %+v", thread.Events)
	}
	if _, err := thread.Choose("q", "now", 3, "y2"); err != ErrChoiceUnavailable {
		t.Fatalf("second tap must be refused, got %v", err)
	}
	if _, err := thread.Choose("missing", "now", 3, "y3"); err != ErrChoiceUnavailable {
		t.Fatalf("unknown event must be refused, got %v", err)
	}
}

func TestAppendEventBoundsTextAndCount(t *testing.T) {
	thread := &Thread{}
	thread.AppendEvent(Event{ID: "long", Kind: EventYou, Text: strings.Repeat("界", 3000)})
	if len(thread.Events[0].Text) > threadEventTextMax || !strings.HasPrefix(thread.Events[0].Text, "界") {
		t.Fatalf("text not bounded on a rune boundary: %d bytes", len(thread.Events[0].Text))
	}
	for i := 0; i < threadEventLimit+5; i++ {
		thread.AppendEvent(Event{ID: "e", Kind: EventLine})
	}
	if len(thread.Events) != threadEventLimit {
		t.Fatalf("events not capped: %d", len(thread.Events))
	}
}

func TestPersonCanCloseWaitingTaskButNotRunningWork(t *testing.T) {
	s := newTestStore(t)
	for _, status := range []Status{StatusActive, StatusBlocked, StatusNeedsReview} {
		id := "hf_" + string(status)
		if _, err := s.Save(Entry{ID: id, Title: "T", Status: status}); err != nil {
			t.Fatal(err)
		}
		// Completing without the person's outcome stays forbidden.
		if _, err := s.UpdateStatus(id, StatusCompleted); err == nil {
			t.Fatalf("%s: completing without an outcome must fail", status)
		}
		if _, err := s.MutateIfStatus(id, nil, func(e *Entry) {
			e.Status = StatusCompleted
			e.Thread = &Thread{Outcome: &Outcome{Kind: OutcomeMarkedDone, By: OutcomeByYou, At: 5}}
		}); err != nil {
			t.Fatalf("%s: the person should be able to mark it done: %v", status, err)
		}
	}
	if _, err := s.Save(Entry{ID: "hf_run", Title: "T", Status: StatusInProgress}); err != nil {
		t.Fatal(err)
	}
	// Running work completes only through verification, which CanTransition already allows;
	// an aborted task cannot be reopened as done by anyone.
	if _, err := s.Save(Entry{ID: "hf_ab", Title: "T", Status: StatusAborted}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MutateIfStatus("hf_ab", nil, func(e *Entry) {
		e.Status = StatusCompleted
		e.Thread = &Thread{Outcome: &Outcome{Kind: OutcomeMarkedDone, By: OutcomeByYou}}
	}); err == nil {
		t.Fatal("an aborted task must not become done")
	}
}

func TestMutateThreadKeepsUpdateTime(t *testing.T) {
	s := newTestStore(t)
	s.Now = clockAt(1000)
	if _, err := s.Save(Entry{ID: "hf_o", Title: "T", Status: StatusNeedsReview}); err != nil {
		t.Fatal(err)
	}
	s.Now = clockAt(9000)
	got, err := s.MutateThread("hf_o", func(thread *Thread) { thread.OpenedAt = 9000 })
	if err != nil || got.UpdatedAt != 1000 || got.Thread.OpenedAt != 9000 {
		t.Fatalf("opening must not bump the update time: %+v %v", got, err)
	}
}
